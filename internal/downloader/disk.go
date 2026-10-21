package downloader

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"
)

// GetMemoryStats reports Go runtime memory statistics
func GetMemoryStats() MemoryStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return MemoryStats{
		AllocBytes:     m.Alloc,
		SysBytes:       m.Sys,
		NumGC:          m.NumGC,
		AllocFormatted: FormatBytesUint64(m.Alloc),
	}
}

// StartCleanupWorker periodically inspects the download directory and prunes expired files
func StartCleanupWorker(ctx context.Context, dir string, retentionHours int, onFileDeleted func(filename string)) {
	if retentionHours <= 0 {
		slog.Info("Automatic file retention cleanup is disabled (retentionHours <= 0)")
		return
	}

	retentionDuration := time.Duration(retentionHours) * time.Hour
	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()

	slog.Info("Started background ephemeral disk cleanup worker", "retention_hours", retentionHours)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanExpiredFiles(dir, retentionDuration, onFileDeleted)
			// Enforce emergency cleanup if disk exceeds 90%
			enforceDiskThreshold(dir, 90.0, onFileDeleted)
		}
	}
}

func cleanExpiredFiles(dir string, maxAge time.Duration, onDeleted func(string)) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if now.Sub(info.ModTime()) > maxAge {
			filePath := filepath.Join(dir, entry.Name())
			if err := os.Remove(filePath); err == nil {
				slog.Info("Cleaned up expired file from ephemeral cache", "file", entry.Name(), "age", now.Sub(info.ModTime()).String())
				if onDeleted != nil {
					onDeleted(entry.Name())
				}
			}
		}
	}
}

// enforceDiskThreshold deletes oldest files if disk usage exceeds the threshold
func enforceDiskThreshold(dir string, maxUsedPercent float64, onDeleted func(string)) {
	stats := GetDiskStats(dir)
	if stats.UsedPercent < maxUsedPercent {
		return
	}

	slog.Warn("Disk usage exceeded safe threshold, purging oldest files", "used_percent", stats.UsedPercent, "threshold", maxUsedPercent)

	type fileWithTime struct {
		name    string
		modTime time.Time
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var files []fileWithTime
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, fileWithTime{name: entry.Name(), modTime: info.ModTime()})
	}

	// Sort oldest first
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})

	for _, f := range files {
		filePath := filepath.Join(dir, f.name)
		if err := os.Remove(filePath); err == nil {
			slog.Info("Emergency purged oldest file to free disk space", "file", f.name)
			if onDeleted != nil {
				onDeleted(f.name)
			}
		}

		// Re-check
		currentStats := GetDiskStats(dir)
		if currentStats.UsedPercent < (maxUsedPercent - 5.0) {
			break
		}
	}
}
