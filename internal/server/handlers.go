package server

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"transloader/internal/downloader"

	"github.com/go-chi/chi/v5"
)

var startTime = time.Now()

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	htmlData, err := content.ReadFile("templates/index.html")
	if err != nil {
		http.Error(w, "Failed to load index template", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(htmlData)
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	filePath := strings.TrimPrefix(r.URL.Path, "/")
	data, err := content.ReadFile(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	ext := filepath.Ext(filePath)
	mimeType := mime.TypeByExtension(ext)
	if mimeType != "" {
		w.Header().Set("Content-Type", mimeType)
	}

	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	stats := s.manager.GetStats()
	resp := map[string]any{
		"status":         "ok",
		"uptime_seconds": int(time.Since(startTime).Seconds()),
		"active_tasks":   stats.ActiveTasks,
		"cached_files":   stats.CompletedFiles,
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetStats(w http.ResponseWriter, r *http.Request) {
	stats := s.manager.GetStats()
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	tasks := s.manager.ListTasks()
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	files, err := s.manager.ListFiles()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, files)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	var req downloader.DownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON request", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.URL) == "" {
		http.Error(w, "URL is required", http.StatusBadRequest)
		return
	}

	task, err := s.manager.StartDownload(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusAccepted, task)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	if err := s.manager.CancelDownload(taskID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Task cancelled"})
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	rawParam := chi.URLParam(r, "name")
	if rawParam == "" {
		rawParam = chi.URLParam(r, "*")
	}

	unescaped, err := url.PathUnescape(rawParam)
	if err != nil {
		unescaped = rawParam
	}
	if strings.Contains(unescaped, "+") {
		if q, err := url.QueryUnescape(unescaped); err == nil {
			unescaped = q
		}
	}

	if err := s.manager.DeleteFile(unescaped); err != nil {
		if err2 := s.manager.DeleteFile(rawParam); err2 != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "File deleted"})
}

func (s *Server) handleDeleteAllFiles(w http.ResponseWriter, r *http.Request) {
	if err := s.manager.DeleteAllFiles(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "All files purged"})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch, unsubscribe := s.manager.Subscribe()
	defer unsubscribe()

	// Initial ping
	_, _ = fmt.Fprintf(w, ": ping\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			_, _ = fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case task, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(task)
			if err == nil {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		}
	}
}

// handleDownloadFile delivers the file directly as an attachment with full RFC 7233 byte-range support
func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	rawParam := chi.URLParam(r, "name")
	if rawParam == "" {
		rawParam = chi.URLParam(r, "*")
	}

	unescaped, err := url.PathUnescape(rawParam)
	if err != nil {
		unescaped = rawParam
	}
	if strings.Contains(unescaped, "+") {
		if q, err := url.QueryUnescape(unescaped); err == nil {
			unescaped = q
		}
	}

	cleanName := filepath.Base(unescaped)
	filePath := filepath.Join(s.cfg.DownloadDir, cleanName)

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		// Fallback: check if rawParam literally exists
		rawPath := filepath.Join(s.cfg.DownloadDir, filepath.Base(rawParam))
		if fi, errRaw := os.Stat(rawPath); errRaw == nil {
			filePath = rawPath
			fileInfo = fi
			cleanName = filepath.Base(rawParam)
		} else {
			http.NotFound(w, r)
			return
		}
	}

	if fileInfo.IsDir() {
		http.Error(w, "Cannot download directory", http.StatusBadRequest)
		return
	}

	// Direct downloadable headers (RFC 6266 + RFC 5987 for full browser & IDM compatibility)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q; filename*=UTF-8''%s`, cleanName, url.PathEscape(cleanName)))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "private, max-age=3600")

	ext := strings.ToLower(filepath.Ext(cleanName))
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mimeType)

	http.ServeFile(w, r, filePath)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
