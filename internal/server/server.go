package server

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"transloader/internal/config"
	"transloader/internal/downloader"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Server encapsulates the HTTP router, configuration, and download manager
type Server struct {
	cfg     *config.Config
	manager *downloader.Manager
	router  *chi.Mux
}

// NewServer initializes HTTP routes and middlewares
func NewServer(cfg *config.Config, manager *downloader.Manager) *Server {
	s := &Server{
		cfg:     cfg,
		manager: manager,
		router:  chi.NewRouter(),
	}

	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	r := s.router

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Health check (always public, no auth needed for UptimeRobot)
	r.Get("/health", s.handleHealth)

	// Public static files
	r.Get("/static/*", s.handleStatic)

	// Protected routes group (if ADMIN_PASSWORD is set)
	r.Group(func(protected chi.Router) {
		if s.cfg.AdminPassword != "" {
			protected.Use(s.basicAuthMiddleware)
		}

		// Web UI
		protected.Get("/", s.handleIndex)

		// Direct Download (with RFC 7233 range & multi-connection support)
		protected.Get("/files/{name}", s.handleDownloadFile)

		// API routes
		protected.Route("/api", func(api chi.Router) {
			api.Get("/stats", s.handleGetStats)
			api.Get("/tasks", s.handleListTasks)
			api.Get("/files", s.handleListFiles)
			api.Post("/download", s.handleDownload)
			api.Post("/cancel/{id}", s.handleCancel)
			api.Delete("/files/{name}", s.handleDeleteFile)
			api.Delete("/files", s.handleDeleteAllFiles)
			api.Get("/events", s.handleEvents)
		})
	})
}

func (s *Server) basicAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.AdminPassword)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Transloader Access"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		_ = user
		next.ServeHTTP(w, r)
	})
}

// ListenAndServe runs the HTTP server binding to the configured port
func (s *Server) ListenAndServe() error {
	addr := fmt.Sprintf(":%s", s.cfg.Port)
	server := &http.Server{
		Addr:              addr,
		Handler:           s.router,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	slog.Info("Starting Transloader HTTP Server", "addr", addr, "port", s.cfg.Port)
	return server.ListenAndServe()
}
