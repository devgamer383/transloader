package config

import (
	"os"
	"path/filepath"
	"strconv"
)

// Config holds the application configuration
type Config struct {
	Port              string
	DownloadDir       string
	MaxConcurrent     int
	RetentionHours    int
	AdminPassword     string
	YtDlpPath         string
	AutoInstallYtDlp  bool
	PublicBaseURL     string
}

// Load loads the configuration from environment variables with sensible defaults
func Load() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	downloadDir := os.Getenv("DOWNLOAD_DIR")
	if downloadDir == "" {
		downloadDir = "downloads"
	}

	// Ensure download directory is an absolute path or relative to current dir
	absDownloadDir, err := filepath.Abs(downloadDir)
	if err == nil {
		downloadDir = absDownloadDir
	}

	maxConcurrent := 3
	if env := os.Getenv("MAX_CONCURRENT_DOWNLOADS"); env != "" {
		if val, err := strconv.Atoi(env); err == nil && val > 0 && val <= 10 {
			maxConcurrent = val
		}
	}

	retentionHours := 6 // Default 6 hours ephemeral cache on Heroku
	if env := os.Getenv("FILE_RETENTION_HOURS"); env != "" {
		if val, err := strconv.Atoi(env); err == nil && val >= 0 {
			retentionHours = val
		}
	}

	adminPassword := os.Getenv("ADMIN_PASSWORD")
	publicBaseURL := os.Getenv("PUBLIC_BASE_URL")

	ytDlpPath := os.Getenv("YTDLP_PATH")
	if ytDlpPath == "" {
		ytDlpPath = filepath.Join("bin", "yt-dlp")
	}

	autoInstallYtDlp := true
	if env := os.Getenv("AUTO_INSTALL_YTDLP"); env != "" {
		if val, err := strconv.ParseBool(env); err == nil {
			autoInstallYtDlp = val
		}
	}

	return &Config{
		Port:             port,
		DownloadDir:      downloadDir,
		MaxConcurrent:    maxConcurrent,
		RetentionHours:   retentionHours,
		AdminPassword:    adminPassword,
		YtDlpPath:        ytDlpPath,
		AutoInstallYtDlp: autoInstallYtDlp,
		PublicBaseURL:    publicBaseURL,
	}
}
