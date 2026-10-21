package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"transloader/internal/config"
	"transloader/internal/downloader"
	"transloader/internal/server"
)

func main() {
	// Configure structured logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("Starting Transloader Application...")

	cfg := config.Load()
	manager := downloader.NewManager(cfg)
	srv := server.NewServer(cfg, manager)

	// Graceful shutdown context
	stopCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server failed", "error", err)
			os.Exit(1)
		}
	}()

	slog.Info("Transloader is ready and listening", "port", cfg.Port, "retention_hours", cfg.RetentionHours)

	<-stopCtx.Done()
	slog.Info("Shutting down gracefully...")

	manager.Close()
	time.Sleep(500 * time.Millisecond)
	slog.Info("Transloader stopped cleanly.")
}
