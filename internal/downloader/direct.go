package downloader

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var cdRegex = regexp.MustCompile(`(?i)filename\*?=(?:UTF-8'')?["']?([^"';\r\n]+)`)

// DirectDownloadProgress carries progress information during HTTP streaming
type DirectDownloadProgress struct {
	DownloadedBytes  int64
	TotalBytes       int64
	ProgressPercent  float64
	SpeedBytesPerSec int64
	ETASeconds       int64
}

// DownloadDirectFile streams the target URL to a file on disk
func DownloadDirectFile(
	ctx context.Context,
	rawURL string,
	destDir string,
	customFilename string,
	customHeaders map[string]string,
	existingResp *http.Response,
	httpClient *http.Client,
	onProgress func(p DirectDownloadProgress),
) (string, int64, error) {

	client := httpClient
	if client == nil {
		client = &http.Client{
			Timeout: 0, // No global timeout for long transfers
		}
	}

	var resp *http.Response
	var err error

	if existingResp != nil {
		resp = existingResp
	} else {
		req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
		if err != nil {
			return "", 0, fmt.Errorf("invalid request: %w", err)
		}

		// Set default headers
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "*/*")

		// Apply custom headers if provided
		for k, v := range customHeaders {
			req.Header.Set(k, v)
		}

		resp, err = client.Do(req)
		if err != nil {
			return "", 0, fmt.Errorf("failed to connect to URL: %w", err)
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("server returned error HTTP status: %s", resp.Status)
	}

	totalBytes := resp.ContentLength
	if totalBytes <= 0 {
		// Try parsing from Content-Range header
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			parts := strings.Split(cr, "/")
			if len(parts) == 2 {
				if t, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64); err == nil {
					totalBytes = t
				}
			}
		}
	}

	// Determine filename
	filename := customFilename
	if filename == "" {
		filename = ExtractFilename(rawURL, resp.Header.Get("Content-Disposition"), resp.Header.Get("Content-Type"))
	}
	filename = SanitizeFilename(filename)

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", 0, fmt.Errorf("failed creating download directory: %w", err)
	}

	finalPath := filepath.Join(destDir, filename)
	// If file already exists with same name, make it unique
	finalPath = MakeUniquePath(finalPath)

	out, err := os.Create(finalPath)
	if err != nil {
		return "", 0, fmt.Errorf("failed creating output file: %w", err)
	}
	defer out.Close()

	// Stream with progress reporting
	buf := make([]byte, 64*1024) // 64KB chunk buffer
	var downloaded int64
	startTime := time.Now()
	lastUpdate := startTime
	var lastDownloaded int64
	var currentSpeed int64
	var smoothedSpeed float64
	const alpha = 0.3

	for {
		select {
		case <-ctx.Done():
			out.Close()
			os.Remove(finalPath) // Clean up partial download
			return "", 0, ctx.Err()
		default:
		}

		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := out.Write(buf[:n]); writeErr != nil {
				out.Close()
				os.Remove(finalPath)
				return "", 0, fmt.Errorf("failed writing to disk: %w", writeErr)
			}
			downloaded += int64(n)

			now := time.Now()
			elapsedSinceLast := now.Sub(lastUpdate)

			// Update speed and progress every 300ms or on completion
			if elapsedSinceLast >= 300*time.Millisecond || readErr != nil {
				deltaBytes := downloaded - lastDownloaded
				if elapsedSinceLast > 0 {
					instantSpeed := float64(deltaBytes) / elapsedSinceLast.Seconds()
					if smoothedSpeed == 0 {
						smoothedSpeed = instantSpeed
					} else {
						smoothedSpeed = (alpha * instantSpeed) + ((1.0 - alpha) * smoothedSpeed)
					}
					currentSpeed = int64(smoothedSpeed)
				}

				lastUpdate = now
				lastDownloaded = downloaded

				var percent float64
				var eta int64
				if totalBytes > 0 {
					percent = (float64(downloaded) / float64(totalBytes)) * 100
					if currentSpeed > 0 {
						remaining := totalBytes - downloaded
						eta = remaining / currentSpeed
					}
				}

				if onProgress != nil {
					onProgress(DirectDownloadProgress{
						DownloadedBytes:  downloaded,
						TotalBytes:       totalBytes,
						ProgressPercent:  percent,
						SpeedBytesPerSec: currentSpeed,
						ETASeconds:       eta,
					})
				}
			}
		}

		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			out.Close()
			os.Remove(finalPath)
			return "", 0, fmt.Errorf("stream read error: %w", readErr)
		}
	}

	return finalPath, downloaded, nil
}

// ExtractFilename extracts filename from Content-Disposition, URL path, or MIME type
func ExtractFilename(rawURL, cd, ct string) string {
	if cd != "" {
		if m := cdRegex.FindStringSubmatch(cd); len(m) > 1 {
			val := strings.Trim(m[1], `"' `)
			if unquoted, err := url.QueryUnescape(val); err == nil && unquoted != "" {
				return unquoted
			}
			return val
		}
	}

	if u, err := url.Parse(rawURL); err == nil {
		base := filepath.Base(u.Path)
		if base != "" && base != "." && base != "/" && strings.Contains(base, ".") {
			if unquoted, err := url.QueryUnescape(base); err == nil && unquoted != "" {
				return unquoted
			}
			return base
		}
	}

	// Try MIME extension
	if ct != "" {
		mediaType, _, _ := mime.ParseMediaType(ct)
		exts, _ := mime.ExtensionsByType(mediaType)
		if len(exts) > 0 {
			return fmt.Sprintf("file_%d%s", time.Now().Unix(), exts[0])
		}
	}

	return fmt.Sprintf("download_%d.bin", time.Now().Unix())
}

// SanitizeFilename removes illegal filesystem characters
func SanitizeFilename(name string) string {
	re := regexp.MustCompile(`[\\/*?:"<>|]`)
	cleaned := re.ReplaceAllString(name, "_")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return "transload_file.bin"
	}
	return cleaned
}

// MakeUniquePath appends (1), (2), etc. if the file path already exists
func MakeUniquePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}

	dir := filepath.Dir(path)
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(filepath.Base(path), ext)

	for i := 1; i < 10000; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return path
}
