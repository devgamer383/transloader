package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MultiDownloadConfig controls the multi-connection worker engine
type MultiDownloadConfig struct {
	MaxConnections int               // e.g. 4, 8, 16
	CustomHeaders  map[string]string // e.g. User-Agent, Cookie, Referer, Authorization
	ChunkSizeMin   int64             // Minimum chunk size (e.g. 2MB)
}

// InspectURL examines whether the URL supports byte range requests and determines total size & filename
type InspectResult struct {
	TotalSize       int64
	SupportsRange   bool
	Filename        string
	ContentType     string
	ResolvedURL     string
	InitialResponse *http.Response
}

// InspectURL sends an initial check to determine remote file metadata
func InspectURL(ctx context.Context, rawURL string, headers map[string]string, client *http.Client) (*InspectResult, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, err
	}

	// Request small range to probe for 206 Partial Content support
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "*/*")

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}

	res := &InspectResult{
		ResolvedURL: resp.Request.URL.String(),
		ContentType: resp.Header.Get("Content-Type"),
	}

	cd := resp.Header.Get("Content-Disposition")
	res.Filename = ExtractFilename(res.ResolvedURL, cd, res.ContentType)

	if resp.StatusCode == http.StatusPartialContent {
		// Server supports range requests
		res.SupportsRange = true
		cr := resp.Header.Get("Content-Range")
		if cr != "" {
			parts := strings.Split(cr, "/")
			if len(parts) == 2 {
				if total, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64); err == nil {
					res.TotalSize = total
				}
			}
		}
		resp.Body.Close()
		return res, nil
	}

	// If server returned 200 OK (didn't accept Range 0-0 or returned whole body)
	if resp.StatusCode == http.StatusOK {
		res.TotalSize = resp.ContentLength
		if resp.Header.Get("Accept-Ranges") == "bytes" && res.TotalSize > 0 {
			res.SupportsRange = true
		}
		// Pass response along to avoid re-requesting if single stream
		res.InitialResponse = resp
		return res, nil
	}

	resp.Body.Close()
	return nil, fmt.Errorf("server returned error status: %s", resp.Status)
}

// DownloadMultiConnection downloads a file using multiple concurrent HTTP Range connections
func DownloadMultiConnection(
	ctx context.Context,
	rawURL string,
	destDir string,
	customFilename string,
	cfg MultiDownloadConfig,
	httpClient *http.Client,
	onProgress func(p DirectDownloadProgress),
) (string, int64, error) {

	if cfg.MaxConnections <= 0 {
		cfg.MaxConnections = 8 // Default 8 concurrent connections
	}
	if cfg.ChunkSizeMin <= 0 {
		cfg.ChunkSizeMin = 2 * 1024 * 1024 // 2 MB minimum per chunk
	}

	if httpClient == nil {
		httpClient = &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 32,
				IdleConnTimeout:     90 * time.Second,
			},
		}
	}

	// 1. Inspect URL for range support and total size
	inspect, err := InspectURL(ctx, rawURL, cfg.CustomHeaders, httpClient)
	if err != nil {
		return "", 0, err
	}

	filename := customFilename
	if filename == "" {
		filename = inspect.Filename
	}
	filename = SanitizeFilename(filename)

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", 0, fmt.Errorf("failed creating download directory: %w", err)
	}

	finalPath := MakeUniquePath(filepath.Join(destDir, filename))

	// 2. Decide if multi-connection is feasible
	numWorkers := cfg.MaxConnections
	if !inspect.SupportsRange || inspect.TotalSize <= cfg.ChunkSizeMin {
		numWorkers = 1
	} else {
		// Calculate how many connections make sense
		maxPossible := int(inspect.TotalSize / cfg.ChunkSizeMin)
		if maxPossible < numWorkers {
			numWorkers = maxPossible
		}
		if numWorkers < 1 {
			numWorkers = 1
		}
	}

	// If single connection fallback
	if numWorkers == 1 {
		return DownloadDirectFile(ctx, rawURL, destDir, filename, cfg.CustomHeaders, inspect.InitialResponse, httpClient, onProgress)
	}

	// Clean up initial inspection response if not used
	if inspect.InitialResponse != nil {
		inspect.InitialResponse.Body.Close()
	}

	// 3. Pre-create the output file with sparse/empty size
	out, err := os.OpenFile(finalPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		return "", 0, fmt.Errorf("failed creating file: %w", err)
	}
	defer out.Close()

	if err := out.Truncate(inspect.TotalSize); err != nil {
		// Non-fatal if truncate fails, file will still write at offsets
	}

	// 4. Calculate chunk ranges
	totalSize := inspect.TotalSize
	chunkSize := totalSize / int64(numWorkers)

	type chunk struct {
		start int64
		end   int64
	}

	chunks := make([]chunk, numWorkers)
	for i := 0; i < numWorkers; i++ {
		start := int64(i) * chunkSize
		end := start + chunkSize - 1
		if i == numWorkers-1 {
			end = totalSize - 1 // Last chunk takes remainder
		}
		chunks[i] = chunk{start: start, end: end}
	}

	var downloadedTotal atomic.Int64
	var lastDownloaded int64
	lastUpdate := time.Now()
	var currentSpeed int64
	var smoothedSpeed float64
	const alpha = 0.3 // EWMA smoothing factor for rock-solid speed measurement

	progressCtx, cancelProgress := context.WithCancel(ctx)
	defer cancelProgress()

	// Progress ticker
	go func() {
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-progressCtx.Done():
				return
			case <-ticker.C:
				current := downloadedTotal.Load()
				now := time.Now()
				elapsed := now.Sub(lastUpdate).Seconds()

				if elapsed > 0 {
					delta := current - lastDownloaded
					instantSpeed := float64(delta) / elapsed
					if smoothedSpeed == 0 {
						smoothedSpeed = instantSpeed
					} else {
						smoothedSpeed = (alpha * instantSpeed) + ((1.0 - alpha) * smoothedSpeed)
					}
					currentSpeed = int64(smoothedSpeed)
					lastDownloaded = current
					lastUpdate = now
				}

				var percent float64
				var eta int64
				if totalSize > 0 {
					percent = (float64(current) / float64(totalSize)) * 100
					if currentSpeed > 0 {
						eta = (totalSize - current) / currentSpeed
					}
				}

				if onProgress != nil {
					onProgress(DirectDownloadProgress{
						DownloadedBytes:  current,
						TotalBytes:       totalSize,
						ProgressPercent:  percent,
						SpeedBytesPerSec: currentSpeed,
						ETASeconds:       eta,
					})
				}
			}
		}
	}()

	// 5. Worker goroutines for parallel Range downloads
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()

	var wg sync.WaitGroup
	errChan := make(chan error, numWorkers)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		c := chunks[i]

		go func(workerID int, chk chunk) {
			defer wg.Done()

			req, err := http.NewRequestWithContext(workerCtx, "GET", inspect.ResolvedURL, nil)
			if err != nil {
				select {
				case errChan <- fmt.Errorf("worker %d req error: %w", workerID, err):
				default:
				}
				cancelWorkers()
				return
			}

			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", chk.start, chk.end))
			req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
			req.Header.Set("Accept", "*/*")

			for k, v := range cfg.CustomHeaders {
				req.Header.Set(k, v)
			}

			resp, err := httpClient.Do(req)
			if err != nil {
				select {
				case errChan <- fmt.Errorf("worker %d connection error: %w", workerID, err):
				default:
				}
				cancelWorkers()
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
				select {
				case errChan <- fmt.Errorf("worker %d bad status: %s", workerID, resp.Status):
				default:
				}
				cancelWorkers()
				return
			}

			buf := make([]byte, 64*1024)
			offset := chk.start

			for {
				select {
				case <-workerCtx.Done():
					return
				default:
				}

				n, readErr := resp.Body.Read(buf)
				if n > 0 {
					_, writeErr := out.WriteAt(buf[:n], offset)
					if writeErr != nil {
						select {
						case errChan <- fmt.Errorf("worker %d write error: %w", workerID, writeErr):
						default:
						}
						cancelWorkers()
						return
					}
					offset += int64(n)
					downloadedTotal.Add(int64(n))
				}

				if readErr != nil {
					if readErr == io.EOF {
						break
					}
					select {
					case errChan <- fmt.Errorf("worker %d read error: %w", workerID, readErr):
					default:
					}
					cancelWorkers()
					return
				}
			}
		}(i, c)
	}

	wg.Wait()
	cancelProgress()

	select {
	case err := <-errChan:
		out.Close()
		os.Remove(finalPath)
		return "", 0, err
	default:
	}

	if ctx.Err() != nil {
		out.Close()
		os.Remove(finalPath)
		return "", 0, ctx.Err()
	}

	// Final progress update
	if onProgress != nil {
		onProgress(DirectDownloadProgress{
			DownloadedBytes:  totalSize,
			TotalBytes:       totalSize,
			ProgressPercent:  100,
			SpeedBytesPerSec: 0,
			ETASeconds:       0,
		})
	}

	return finalPath, totalSize, nil
}
