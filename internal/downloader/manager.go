package downloader

import (
	"context"
	"fmt"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"transloader/internal/config"

	"github.com/google/uuid"
)

// Manager coordinates download jobs, concurrency limits, file listings, and real-time SSE broadcasts
type Manager struct {
	cfg         *config.Config
	tasks       map[string]*Task
	tasksMu     sync.RWMutex
	subscribers map[chan *Task]struct{}
	subMu       sync.Mutex
	sem         chan struct{}
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewManager creates an initialized Manager instance
func NewManager(cfg *config.Config) *Manager {
	ctx, cancel := context.WithCancel(context.Background())

	m := &Manager{
		cfg:         cfg,
		tasks:       make(map[string]*Task),
		subscribers: make(map[chan *Task]struct{}),
		sem:         make(chan struct{}, cfg.MaxConcurrent),
		ctx:         ctx,
		cancel:      cancel,
	}

	// Ensure download directory exists
	_ = os.MkdirAll(cfg.DownloadDir, 0755)

	// Start auto-cleanup worker
	go StartCleanupWorker(ctx, cfg.DownloadDir, cfg.RetentionHours, func(filename string) {
		m.BroadcastDeleted(filename)
	})

	return m
}

// Close terminates manager background routines
func (m *Manager) Close() {
	m.cancel()
}

// Subscribe opens a channel for SSE task updates
func (m *Manager) Subscribe() (chan *Task, func()) {
	ch := make(chan *Task, 64)

	m.subMu.Lock()
	m.subscribers[ch] = struct{}{}
	m.subMu.Unlock()

	unsubscribe := func() {
		m.subMu.Lock()
		delete(m.subscribers, ch)
		close(ch)
		m.subMu.Unlock()
	}

	return ch, unsubscribe
}

// Broadcast sends task update to all active SSE subscribers
func (m *Manager) Broadcast(t *Task) {
	m.subMu.Lock()
	defer m.subMu.Unlock()

	for ch := range m.subscribers {
		select {
		case ch <- t:
		default:
			// Non-blocking drop if consumer is slow
		}
	}
}

// BroadcastDeleted notifies subscribers that a file was removed
func (m *Manager) BroadcastDeleted(filename string) {
	fakeTask := &Task{
		ID:           "deleted",
		Filename:     filename,
		Status:       "deleted",
		ErrorMessage: filename,
	}
	m.Broadcast(fakeTask)
}

// StartDownload enqueues a new download job
func (m *Manager) StartDownload(req DownloadRequest) (*Task, error) {
	rawURL := strings.TrimSpace(req.URL)
	if rawURL == "" {
		return nil, fmt.Errorf("URL cannot be empty")
	}

	// Determine source type
	sourceType := "direct"
	if req.ForceYtDlp || IsMediaURL(rawURL) {
		sourceType = "media"
	} else if IsGoogleDriveURL(rawURL) {
		sourceType = "gdrive"
	}

	taskID := uuid.New().String()
	taskCtx, cancel := context.WithCancel(m.ctx)

	conns := req.Connections
	if conns <= 0 {
		conns = 8
	} else if conns > 16 {
		conns = 16
	}

	task := &Task{
		ID:               taskID,
		URL:              rawURL,
		Filename:         req.Filename,
		Connections:      conns,
		Status:           StatusPending,
		DownloadedFormat: "0 B",
		TotalFormatted:   "Calculating...",
		SpeedFormatted:   "0 B/s",
		SourceType:       sourceType,
		CreatedAt:        time.Now(),
		CancelFunc:       cancel,
	}

	if task.Filename == "" {
		task.Filename = "Initializing..."
	}

	m.tasksMu.Lock()
	m.tasks[taskID] = task
	m.tasksMu.Unlock()

	m.Broadcast(task)

	// Launch in background
	go m.runTask(taskCtx, task, req)

	return task, nil
}

func (m *Manager) runTask(ctx context.Context, task *Task, req DownloadRequest) {
	// Acquire concurrency slot
	select {
	case <-ctx.Done():
		m.markTaskFailed(task, "Cancelled before start")
		return
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	}

	m.tasksMu.Lock()
	task.Status = StatusDownloading
	m.tasksMu.Unlock()
	m.Broadcast(task)

	slog.Info("Beginning transload job", "id", task.ID, "url", task.URL, "type", task.SourceType)

	var finalPath string
	var totalSize int64
	var err error

	switch task.SourceType {
	case "media":
		// Ensure yt-dlp is available
		ytdlpBin, binErr := EnsureYtDlp(ctx, m.cfg.YtDlpPath, m.cfg.AutoInstallYtDlp)
		if binErr != nil {
			m.markTaskFailed(task, fmt.Sprintf("yt-dlp setup error: %v", binErr))
			return
		}

		finalPath, err = DownloadWithYtDlp(ctx, ytdlpBin, task.URL, m.cfg.DownloadDir, req.Filename, func(p YtDlpProgress) {
			m.tasksMu.Lock()
			task.ProgressPercent = p.Percent
			task.SpeedFormatted = p.SpeedStr
			task.TotalFormatted = p.TotalStr
			task.ETASeconds = p.ETASeconds
			m.tasksMu.Unlock()
			m.Broadcast(task)
		})

		if err == nil {
			if fi, statErr := os.Stat(finalPath); statErr == nil {
				totalSize = fi.Size()
			}
		}

	case "gdrive":
		directURL, resp, client, gdErr := ResolveGoogleDriveDirectURL(ctx, task.URL)
		if gdErr != nil {
			m.markTaskFailed(task, fmt.Sprintf("Google Drive resolution failed: %v", gdErr))
			return
		}

		// Download using multi-connection parallel chunk worker
		cfg := MultiDownloadConfig{
			MaxConnections: task.Connections,
			CustomHeaders:  req.Headers,
		}

		finalPath, totalSize, err = DownloadMultiConnection(
			ctx,
			directURL,
			m.cfg.DownloadDir,
			req.Filename,
			cfg,
			client,
			func(p DirectDownloadProgress) {
				m.updateTaskProgress(task, p)
			},
		)
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}

	default: // direct HTTP/HTTPS
		cfg := MultiDownloadConfig{
			MaxConnections: task.Connections,
			CustomHeaders:  req.Headers,
		}

		finalPath, totalSize, err = DownloadMultiConnection(
			ctx,
			task.URL,
			m.cfg.DownloadDir,
			req.Filename,
			cfg,
			nil,
			func(p DirectDownloadProgress) {
				m.updateTaskProgress(task, p)
			},
		)
	}

	if err != nil {
		if ctx.Err() != nil {
			m.markTaskCancelled(task)
		} else {
			m.markTaskFailed(task, err.Error())
		}
		return
	}

	// Completion
	now := time.Now()
	filename := filepath.Base(finalPath)

	m.tasksMu.Lock()
	task.Status = StatusCompleted
	task.FilePath = finalPath
	task.Filename = filename
	task.DownloadedBytes = totalSize
	task.TotalBytes = totalSize
	task.ProgressPercent = 100
	task.DownloadedFormat = FormatBytes(totalSize)
	task.TotalFormatted = FormatBytes(totalSize)
	task.SpeedFormatted = "0 B/s"
	task.ETASeconds = 0
	task.CompletedAt = &now
	if m.cfg.RetentionHours > 0 {
		exp := now.Add(time.Duration(m.cfg.RetentionHours) * time.Hour)
		task.ExpiresAt = &exp
	}
	m.tasksMu.Unlock()

	slog.Info("Transload job completed successfully", "id", task.ID, "filename", filename, "size", FormatBytes(totalSize))
	m.Broadcast(task)
}

func (m *Manager) updateTaskProgress(task *Task, p DirectDownloadProgress) {
	m.tasksMu.Lock()
	task.DownloadedBytes = p.DownloadedBytes
	task.TotalBytes = p.TotalBytes
	task.ProgressPercent = p.ProgressPercent
	task.SpeedBytesPerSec = p.SpeedBytesPerSec
	task.SpeedFormatted = FormatBytes(p.SpeedBytesPerSec) + "/s"
	task.DownloadedFormat = FormatBytes(p.DownloadedBytes)
	if p.TotalBytes > 0 {
		task.TotalFormatted = FormatBytes(p.TotalBytes)
	} else {
		task.TotalFormatted = "Unknown"
	}
	task.ETASeconds = p.ETASeconds
	m.tasksMu.Unlock()

	m.Broadcast(task)
}

func (m *Manager) markTaskFailed(task *Task, errMsg string) {
	m.tasksMu.Lock()
	task.Status = StatusFailed
	task.ErrorMessage = errMsg
	m.tasksMu.Unlock()
	slog.Error("Transload job failed", "id", task.ID, "error", errMsg)
	m.Broadcast(task)
}

func (m *Manager) markTaskCancelled(task *Task) {
	m.tasksMu.Lock()
	task.Status = StatusCancelled
	task.ErrorMessage = "Download cancelled by user"
	m.tasksMu.Unlock()
	slog.Warn("Transload job cancelled", "id", task.ID)
	m.Broadcast(task)
}

// CancelDownload stops an active task
func (m *Manager) CancelDownload(taskID string) error {
	m.tasksMu.RLock()
	task, exists := m.tasks[taskID]
	m.tasksMu.RUnlock()

	if !exists {
		return fmt.Errorf("task not found: %s", taskID)
	}

	if task.Status != StatusPending && task.Status != StatusDownloading {
		return fmt.Errorf("task is not running (status: %s)", task.Status)
	}

	if task.CancelFunc != nil {
		task.CancelFunc()
	}

	return nil
}

// ListTasks returns all current and recently processed tasks
func (m *Manager) ListTasks() []*Task {
	m.tasksMu.RLock()
	defer m.tasksMu.RUnlock()

	result := make([]*Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		result = append(result, t)
	}
	return result
}

// ListFiles scans the downloads folder and returns metadata for all downloaded files
func (m *Manager) ListFiles() ([]FileItem, error) {
	entries, err := os.ReadDir(m.cfg.DownloadDir)
	if err != nil {
		return nil, err
	}

	var files []FileItem

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		ext := strings.ToLower(filepath.Ext(entry.Name()))
		mimeType := mime.TypeByExtension(ext)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		var expiresAt time.Time
		var expiresIn string
		if m.cfg.RetentionHours > 0 {
			expiresAt = info.ModTime().Add(time.Duration(m.cfg.RetentionHours) * time.Hour)
			remaining := time.Until(expiresAt)
			if remaining > 0 {
				expiresIn = fmt.Sprintf("%dh %dm", int(remaining.Hours()), int(remaining.Minutes())%60)
			} else {
				expiresIn = "Expiring soon"
			}
		} else {
			expiresIn = "Persistent"
		}

		item := FileItem{
			Name:          entry.Name(),
			Size:          info.Size(),
			SizeFormatted: FormatBytes(info.Size()),
			ModTime:       info.ModTime(),
			Extension:     ext,
			MimeType:      mimeType,
			DownloadURL:   fmt.Sprintf("/files/%s", entry.Name()),
			ExpiresAt:     expiresAt,
			ExpiresIn:     expiresIn,
		}

		files = append(files, item)
	}

	return files, nil
}

// DeleteFile deletes a downloaded file by name
func (m *Manager) DeleteFile(filename string) error {
	cleanName := filepath.Base(filename)
	targetPath := filepath.Join(m.cfg.DownloadDir, cleanName)

	if err := os.Remove(targetPath); err != nil {
		return err
	}

	m.BroadcastDeleted(cleanName)
	return nil
}

// DeleteAllFiles purges all files in the download directory
func (m *Manager) DeleteAllFiles() error {
	entries, err := os.ReadDir(m.cfg.DownloadDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			_ = os.Remove(filepath.Join(m.cfg.DownloadDir, entry.Name()))
			m.BroadcastDeleted(entry.Name())
		}
	}
	return nil
}

// GetStats returns current disk, memory, and task statistics
func (m *Manager) GetStats() SystemStats {
	diskStats := GetDiskStats(m.cfg.DownloadDir)
	memStats := GetMemoryStats()

	m.tasksMu.RLock()
	activeCount := 0
	for _, t := range m.tasks {
		if t.Status == StatusPending || t.Status == StatusDownloading {
			activeCount++
		}
	}
	m.tasksMu.RUnlock()

	entries, _ := os.ReadDir(m.cfg.DownloadDir)
	completedCount := 0
	for _, e := range entries {
		if !e.IsDir() {
			completedCount++
		}
	}

	return SystemStats{
		Disk:           diskStats,
		Memory:         memStats,
		ActiveTasks:    activeCount,
		CompletedFiles: completedCount,
	}
}
