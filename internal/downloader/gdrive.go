package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"time"
)

var (
	gdriveRegex = regexp.MustCompile(`https?://(?:drive|docs)\.google\.com/(?:file/d/|open\?id=|uc\?(?:export=download&)?id=)([a-zA-Z0-9_-]+)`)
	confirmRegex = regexp.MustCompile(`confirm=([0-9A-Za-z_]+)`)
)

// IsGoogleDriveURL checks if the provided URL matches Google Drive file sharing patterns
func IsGoogleDriveURL(rawURL string) bool {
	return gdriveRegex.MatchString(rawURL)
}

// ExtractGoogleDriveID retrieves the Google Drive file ID from the URL
func ExtractGoogleDriveID(rawURL string) string {
	m := gdriveRegex.FindStringSubmatch(rawURL)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

// ResolveGoogleDriveDirectURL resolves a public Google Drive sharing URL to its direct binary download URL
func ResolveGoogleDriveDirectURL(ctx context.Context, rawURL string) (string, *http.Response, *http.Client, error) {
	fileID := ExtractGoogleDriveID(rawURL)
	if fileID == "" {
		return "", nil, nil, fmt.Errorf("could not extract Google Drive file ID from URL")
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:     jar,
		Timeout: 45 * time.Second,
	}

	// First attempt: drive.usercontent.google.com with confirm=t
	directURL := fmt.Sprintf("https://drive.usercontent.google.com/download?id=%s&export=download&authuser=0&confirm=t", fileID)

	req, err := http.NewRequestWithContext(ctx, "GET", directURL, nil)
	if err != nil {
		return "", nil, nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to reach Google Drive: %w", err)
	}

	// Check if this response is already the file download
	ct := resp.Header.Get("Content-Type")
	cd := resp.Header.Get("Content-Disposition")
	if strings.Contains(cd, "attachment") || (!strings.Contains(ct, "text/html") && resp.ContentLength > 0) {
		return directURL, resp, client, nil
	}

	// If it returned an HTML confirmation page (large file virus warning):
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	resp.Body.Close()
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to read Google Drive confirmation page: %w", err)
	}

	bodyStr := string(bodyBytes)
	// Check for confirm token
	var confirmToken string
	if m := confirmRegex.FindStringSubmatch(bodyStr); len(m) > 1 {
		confirmToken = m[1]
	}

	var secondURL string
	if confirmToken != "" {
		secondURL = fmt.Sprintf("https://drive.usercontent.google.com/download?id=%s&export=download&confirm=%s", fileID, confirmToken)
	} else {
		// Fallback to docs.google.com/uc
		secondURL = fmt.Sprintf("https://docs.google.com/uc?export=download&id=%s&confirm=t", fileID)
	}

	req2, err := http.NewRequestWithContext(ctx, "GET", secondURL, nil)
	if err != nil {
		return "", nil, nil, err
	}
	req2.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	req2.Header.Set("Accept", "*/*")

	resp2, err := client.Do(req2)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed second Google Drive request: %w", err)
	}

	return secondURL, resp2, client, nil
}
