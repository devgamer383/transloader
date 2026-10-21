package downloader

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMultiConnectionDownload(t *testing.T) {
	// Generate 10MB of random test data
	testDataSize := 10 * 1024 * 1024
	testData := make([]byte, testDataSize)
	_, err := rand.Read(testData)
	if err != nil {
		t.Fatalf("failed generating random data: %v", err)
	}
	expectedHash := sha256.Sum256(testData)

	headerReceived := false

	// Test HTTP server with RFC 7233 Range support
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Custom-Auth") == "secret123" {
			headerReceived = true
		}

		http.ServeContent(w, r, "testfile.dat", time.Now(), bytes.NewReader(testData))
	}))
	defer ts.Close()

	tempDir, err := os.MkdirTemp("", "transloader_test_*")
	if err != nil {
		t.Fatalf("failed creating temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := MultiDownloadConfig{
		MaxConnections: 4,
		CustomHeaders: map[string]string{
			"X-Custom-Auth": "secret123",
		},
		ChunkSizeMin: 1024 * 1024,
	}

	finalPath, downloadedSize, err := DownloadMultiConnection(
		context.Background(),
		ts.URL,
		tempDir,
		"downloaded.dat",
		cfg,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("DownloadMultiConnection failed: %v", err)
	}

	if downloadedSize != int64(testDataSize) {
		t.Errorf("expected size %d, got %d", testDataSize, downloadedSize)
	}

	if !headerReceived {
		t.Errorf("custom header X-Custom-Auth was not received by server")
	}

	downloadedBytes, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("failed reading downloaded file: %v", err)
	}

	actualHash := sha256.Sum256(downloadedBytes)
	if actualHash != expectedHash {
		t.Errorf("SHA256 mismatch! Expected %x, got %x", expectedHash, actualHash)
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"normal.txt", "normal.txt"},
		{"file/with\\bad:chars?.mp4", "file_with_bad_chars_.mp4"},
		{"   ", "transload_file.bin"},
		{"foo<bar>baz|qux*test.iso", "foo_bar_baz_qux_test.iso"},
	}

	for _, tt := range tests {
		got := SanitizeFilename(tt.input)
		if got != tt.expected {
			t.Errorf("SanitizeFilename(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestMakeUniquePath(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "unique_test_*")
	if err != nil {
		t.Fatalf("failed creating temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	baseFile := filepath.Join(tempDir, "sample.txt")
	_ = os.WriteFile(baseFile, []byte("first"), 0644)

	second := MakeUniquePath(baseFile)
	expectedSecond := filepath.Join(tempDir, "sample (1).txt")
	if second != expectedSecond {
		t.Errorf("MakeUniquePath = %s; want %s", second, expectedSecond)
	}

	_ = os.WriteFile(second, []byte("second"), 0644)
	third := MakeUniquePath(baseFile)
	expectedThird := filepath.Join(tempDir, "sample (2).txt")
	if third != expectedThird {
		t.Errorf("MakeUniquePath = %s; want %s", third, expectedThird)
	}
}
