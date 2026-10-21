package downloader

import (
	"context"
	"fmt"
	"time"
)

// TaskStatus represents the lifecycle state of a download job
type TaskStatus string

const (
	StatusPending     TaskStatus = "pending"
	StatusDownloading TaskStatus = "downloading"
	StatusCompleted   TaskStatus = "completed"
	StatusFailed      TaskStatus = "failed"
	StatusCancelled   TaskStatus = "cancelled"
)

// Task represents an active or finished transload operation
type Task struct {
	ID                string             `json:"id"`
	URL               string             `json:"url"`
	Filename          string             `json:"filename"`
	FilePath          string             `json:"file_path,omitempty"`
	Status            TaskStatus         `json:"status"`
	Connections       int                `json:"connections"`
	TotalBytes        int64              `json:"total_bytes"`
	DownloadedBytes   int64              `json:"downloaded_bytes"`
	ProgressPercent   float64            `json:"progress_percent"`
	SpeedBytesPerSec  int64              `json:"speed_bytes_sec"`
	SpeedFormatted    string             `json:"speed_formatted"`
	DownloadedFormat  string             `json:"downloaded_formatted"`
	TotalFormatted    string             `json:"total_formatted"`
	ETASeconds        int64              `json:"eta_seconds"`
	ErrorMessage      string             `json:"error_message,omitempty"`
	SourceType        string             `json:"source_type"` // "direct", "gdrive", "media"
	CreatedAt         time.Time          `json:"created_at"`
	CompletedAt       *time.Time         `json:"completed_at,omitempty"`
	ExpiresAt         *time.Time         `json:"expires_at,omitempty"`
	CancelFunc        context.CancelFunc `json:"-"`
}

// DownloadRequest represents a user request submitted via the Web UI or API
type DownloadRequest struct {
	URL         string            `json:"url"`
	Filename    string            `json:"filename,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Connections int               `json:"connections,omitempty"`
	ForceYtDlp  bool              `json:"force_ytdlp,omitempty"`
}

// FileItem represents a transloaded file stored in the ephemeral cache
type FileItem struct {
	Name          string    `json:"name"`
	Size          int64     `json:"size"`
	SizeFormatted string    `json:"size_formatted"`
	ModTime       time.Time `json:"mod_time"`
	Extension     string    `json:"extension"`
	MimeType      string    `json:"mime_type"`
	DownloadURL   string    `json:"download_url"`
	ExpiresAt     time.Time `json:"expires_at"`
	ExpiresIn     string    `json:"expires_in"`
}

// DiskStats holds filesystem storage statistics for Heroku dyno monitoring
type DiskStats struct {
	TotalBytes     uint64  `json:"total_bytes"`
	FreeBytes      uint64  `json:"free_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	UsedPercent    float64 `json:"used_percent"`
	TotalFormatted string  `json:"total_formatted"`
	FreeFormatted  string  `json:"free_formatted"`
	UsedFormatted  string  `json:"used_formatted"`
}

// MemoryStats holds runtime memory statistics for low-overhead validation
type MemoryStats struct {
	AllocBytes     uint64 `json:"alloc_bytes"`
	SysBytes       uint64 `json:"sys_bytes"`
	NumGC          uint32 `json:"num_gc"`
	AllocFormatted string `json:"alloc_formatted"`
}

// SystemStats aggregates storage and runtime stats
type SystemStats struct {
	Disk           DiskStats   `json:"disk"`
	Memory         MemoryStats `json:"memory"`
	ActiveTasks    int         `json:"active_tasks"`
	CompletedFiles int         `json:"completed_files"`
}

// FormatBytes formats byte count into human-readable representation
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// FormatBytesUint64 formats uint64 byte count into human-readable representation
func FormatBytesUint64(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
