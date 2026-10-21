package config

import (
	"flag"
	"fmt"
	"net"
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

// Load loads the configuration from flags and environment variables with sensible defaults
func Load() *Config {
	defaultPort := os.Getenv("PORT")
	if defaultPort == "" {
		defaultPort = "8080"
	}

	defaultDir := os.Getenv("DOWNLOAD_DIR")
	if defaultDir == "" {
		defaultDir = "downloads"
	}

	defaultRetention := 6
	if env := os.Getenv("FILE_RETENTION_HOURS"); env != "" {
		if val, err := strconv.Atoi(env); err == nil && val >= 0 {
			defaultRetention = val
		}
	}

	defaultConcurrent := 3
	if env := os.Getenv("MAX_CONCURRENT_DOWNLOADS"); env != "" {
		if val, err := strconv.Atoi(env); err == nil && val > 0 && val <= 10 {
			defaultConcurrent = val
		}
	}

	// CLI flags
	fs := flag.NewFlagSet("transloader", flag.ContinueOnError)
	portFlag := fs.String("port", "", "Port to listen on (e.g. -port 8888 or -p 8888)")
	pFlag := fs.String("p", "", "Shorthand for -port")
	dirFlag := fs.String("dir", "", "Directory to store downloads")
	retentionFlag := fs.Int("retention", -1, "File retention hours")
	concurrencyFlag := fs.Int("concurrency", -1, "Max concurrent downloads")

	_ = fs.Parse(os.Args[1:])

	port := defaultPort
	if *portFlag != "" {
		port = *portFlag
	} else if *pFlag != "" {
		port = *pFlag
	}

	// Auto-check if port is available; if not and port was default 8080, find free port!
	if port == "8080" && !isPortAvailable(port) {
		for candidate := 8081; candidate <= 8090; candidate++ {
			candStr := strconv.Itoa(candidate)
			if isPortAvailable(candStr) {
				port = candStr
				break
			}
		}
	}

	downloadDir := defaultDir
	if *dirFlag != "" {
		downloadDir = *dirFlag
	}
	absDownloadDir, err := filepath.Abs(downloadDir)
	if err == nil {
		downloadDir = absDownloadDir
	}

	retentionHours := defaultRetention
	if *retentionFlag >= 0 {
		retentionHours = *retentionFlag
	}

	maxConcurrent := defaultConcurrent
	if *concurrencyFlag > 0 && *concurrencyFlag <= 16 {
		maxConcurrent = *concurrencyFlag
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

func isPortAvailable(port string) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
