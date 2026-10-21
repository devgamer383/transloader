package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"transloader/internal/config"
	"transloader/internal/downloader"
)

func TestServerEndpoints(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "server_test_*")
	if err != nil {
		t.Fatalf("failed creating temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create dummy downloadable file
	dummyFilename := "test_sample.zip"
	dummyContent := []byte("Hello Transloader Direct Multi-Connection File Content")
	_ = os.WriteFile(filepath.Join(tempDir, dummyFilename), dummyContent, 0644)

	cfg := &config.Config{
		Port:           "8080",
		DownloadDir:    tempDir,
		MaxConcurrent:  2,
		RetentionHours: 1,
	}

	mgr := downloader.NewManager(cfg)
	defer mgr.Close()

	srv := NewServer(cfg, mgr)

	// 1. Test /health
	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()
	srv.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("health returned status %d; want %d", rr.Code, http.StatusOK)
	}

	// 2. Test /api/stats
	req = httptest.NewRequest("GET", "/api/stats", nil)
	rr = httptest.NewRecorder()
	srv.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("stats returned status %d; want %d", rr.Code, http.StatusOK)
	}

	// 3. Test /api/files
	req = httptest.NewRequest("GET", "/api/files", nil)
	rr = httptest.NewRecorder()
	srv.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("files returned status %d; want %d", rr.Code, http.StatusOK)
	}

	var files []downloader.FileItem
	if err := json.Unmarshal(rr.Body.Bytes(), &files); err != nil {
		t.Fatalf("failed decoding files json: %v", err)
	}
	if len(files) != 1 || files[0].Name != dummyFilename {
		t.Errorf("unexpected files list: %+v", files)
	}

	// 4. Test /files/{name} - MUST have Content-Disposition: attachment and Accept-Ranges: bytes
	req = httptest.NewRequest("GET", "/files/"+dummyFilename, nil)
	rr = httptest.NewRecorder()
	srv.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("direct download returned status %d; want %d", rr.Code, http.StatusOK)
	}

	cd := rr.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") {
		t.Errorf("expected Content-Disposition attachment, got %q", cd)
	}

	ar := rr.Header().Get("Accept-Ranges")
	if ar != "bytes" {
		t.Errorf("expected Accept-Ranges: bytes, got %q", ar)
	}

	if rr.Body.String() != string(dummyContent) {
		t.Errorf("downloaded content mismatch, got %s", rr.Body.String())
	}
}
