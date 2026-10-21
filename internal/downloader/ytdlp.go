package downloader

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	mediaURLPatterns = regexp.MustCompile(`(?i)(youtube\.com|youtu\.be|twitter\.com|x\.com|instagram\.com|tiktok\.com|reddit\.com|vimeo\.com|facebook\.com|fb\.watch|twitch\.tv|soundcloud\.com|bilibili\.com|dailymotion\.com)`)
	ytProgressRegex  = regexp.MustCompile(`\[download\]\s+([\d\.]+)%\s+of\s+~?([\d\.]+[A-Za-z]+)\s+at\s+([\d\.]+[A-Za-z]+/s)\s+ETA\s+([\d:]+)`)
	ytDestRegex      = regexp.MustCompile(`\[(?:download|Merger)\]\s+Destination:\s+(.+)`)
	ytAlreadyRegex   = regexp.MustCompile(`\[download\]\s+(.+)\s+has already been downloaded`)

	ytdlpMu sync.Mutex
)

// YtDlpProgress carries real-time progress parsed from yt-dlp stdout
type YtDlpProgress struct {
	Percent    float64
	TotalStr   string
	SpeedStr   string
	ETAStr     string
	ETASeconds int64
}

// IsMediaURL checks if a given URL is a known streaming media site
func IsMediaURL(rawURL string) bool {
	return mediaURLPatterns.MatchString(rawURL)
}

// EnsureYtDlp verifies that yt-dlp binary is available, or downloads the standalone release
func EnsureYtDlp(ctx context.Context, customPath string, autoInstall bool) (string, error) {
	ytdlpMu.Lock()
	defer ytdlpMu.Unlock()

	// 1. Check custom path
	if customPath != "" {
		if info, err := os.Stat(customPath); err == nil && !info.IsDir() {
			return customPath, nil
		}
	}

	// 2. Check system PATH
	if sysPath, err := exec.LookPath("yt-dlp"); err == nil {
		return sysPath, nil
	}

	if !autoInstall {
		return "", fmt.Errorf("yt-dlp binary not found in PATH or at %s", customPath)
	}

	// 3. Auto-download static binary
	destPath := customPath
	if destPath == "" {
		destPath = filepath.Join("bin", "yt-dlp")
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return "", fmt.Errorf("failed to create bin directory: %w", err)
	}

	slog.Info("yt-dlp not found; downloading standalone Linux binary from GitHub releases...", "dest", destPath)

	downloadURL := "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp"
	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Transloader-Worker)")

	client := &http.Client{Timeout: 3 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download yt-dlp: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading yt-dlp returned HTTP status %d", resp.StatusCode)
	}

	tmpFile := destPath + ".tmp"
	out, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to open output file: %w", err)
	}

	_, err = io.Copy(out, resp.Body)
	out.Close()
	if err != nil {
		os.Remove(tmpFile)
		return "", fmt.Errorf("failed writing yt-dlp binary: %w", err)
	}

	if err := os.Rename(tmpFile, destPath); err != nil {
		return "", fmt.Errorf("failed moving yt-dlp binary to final destination: %w", err)
	}

	if err := os.Chmod(destPath, 0755); err != nil {
		return "", fmt.Errorf("failed setting executable permissions on yt-dlp: %w", err)
	}

	slog.Info("Successfully installed standalone yt-dlp binary", "path", destPath)
	return destPath, nil
}

// DownloadWithYtDlp executes yt-dlp on the target URL, reporting live progress
func DownloadWithYtDlp(ctx context.Context, ytdlpBin, rawURL, destDir, customName string, onProgress func(p YtDlpProgress)) (string, error) {
	outTemplate := filepath.Join(destDir, "%(title)s [%(id)s].%(ext)s")
	if customName != "" {
		if !strings.Contains(customName, ".") {
			customName += ".%(ext)s"
		}
		outTemplate = filepath.Join(destDir, customName)
	}

	args := []string{
		"--newline",
		"--no-warnings",
		"--no-check-certificates",
		"--concurrent-fragments", "4",
		"-o", outTemplate,
		rawURL,
	}

	cmd := exec.CommandContext(ctx, ytdlpBin, args...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start yt-dlp: %w", err)
	}

	var finalDest string
	scanner := bufio.NewScanner(stdoutPipe)

	for scanner.Scan() {
		line := scanner.Text()

		// Match destination file
		if m := ytDestRegex.FindStringSubmatch(line); len(m) > 1 {
			finalDest = strings.TrimSpace(m[1])
		} else if m := ytAlreadyRegex.FindStringSubmatch(line); len(m) > 1 {
			finalDest = strings.TrimSpace(m[1])
		}

		// Match progress line
		if m := ytProgressRegex.FindStringSubmatch(line); len(m) > 4 {
			percent, _ := strconv.ParseFloat(m[1], 64)
			totalStr := m[2]
			speedStr := m[3]
			etaStr := m[4]

			etaSecs := parseETAToSeconds(etaStr)

			if onProgress != nil {
				onProgress(YtDlpProgress{
					Percent:    percent,
					TotalStr:   totalStr,
					SpeedStr:   speedStr,
					ETAStr:     etaStr,
					ETASeconds: etaSecs,
				})
			}
		}
	}

	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("yt-dlp download failed: %w", err)
	}

	if finalDest == "" {
		// Fallback: search destDir for recently modified file
		entries, err := os.ReadDir(destDir)
		if err == nil && len(entries) > 0 {
			var newest os.DirEntry
			var newestTime time.Time
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				info, err := e.Info()
				if err == nil && info.ModTime().After(newestTime) {
					newest = e
					newestTime = info.ModTime()
				}
			}
			if newest != nil {
				finalDest = filepath.Join(destDir, newest.Name())
			}
		}
	}

	if finalDest == "" {
		return "", fmt.Errorf("yt-dlp completed but failed to locate downloaded file")
	}

	return finalDest, nil
}

func parseETAToSeconds(eta string) int64 {
	parts := strings.Split(eta, ":")
	if len(parts) == 2 {
		min, _ := strconv.ParseInt(parts[0], 10, 64)
		sec, _ := strconv.ParseInt(parts[1], 10, 64)
		return min*60 + sec
	} else if len(parts) == 3 {
		hr, _ := strconv.ParseInt(parts[0], 10, 64)
		min, _ := strconv.ParseInt(parts[1], 10, 64)
		sec, _ := strconv.ParseInt(parts[2], 10, 64)
		return hr*3600 + min*60 + sec
	}
	return 0
}
