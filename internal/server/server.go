package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/tlvenn/zfs-provisioner/internal/config"
	"github.com/tlvenn/zfs-provisioner/internal/provisioner"
)

const maxRequestBody = 1 << 20 // 1MB

// Server handles HTTP provisioning requests
type Server struct {
	backend provisioner.Backend
	mu      sync.Mutex
	logger  *log.Logger
}

// New creates a new Server
func New(backend provisioner.Backend) *Server {
	return &Server{
		backend: backend,
		logger:  log.New(os.Stdout, "", log.LstdFlags),
	}
}

// Handler returns the HTTP handler for the server
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /provision", s.handleProvision)
	mux.HandleFunc("GET /health", s.handleHealth)
	return mux
}

func (s *Server) handleProvision(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

	var req config.ProvisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	cfg, err := config.BuildConfig(req.Parent, req.Defaults, req.Datasets)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	s.logger.Printf("provisioning %d datasets under %s", len(cfg.Datasets), cfg.Parent)

	s.mu.Lock()
	p := provisioner.New(s.backend, false, false, io.Discard)
	results := p.ProvisionWithResults(cfg)
	s.mu.Unlock()

	for _, result := range results {
		if result.Error != "" {
			s.logger.Printf("  %s: error: %s", result.Name, result.Error)
		} else {
			s.logger.Printf("  %s: %s", result.Name, result.Action)
		}
	}

	writeJSON(w, http.StatusOK, config.ProvisionResponse{Results: results})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	cmd := exec.CommandContext(r.Context(), "zfs", "list", "-H", "-o", "name", "-d", "0")
	if err := cmd.Run(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "error",
			"error":  "zfs not available: " + err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ListenAndServe starts HTTP servers on the given addresses with graceful shutdown support.
// Multiple addresses allow binding to specific bridge interfaces.
func (s *Server) ListenAndServe(ctx context.Context, addrs []string) error {
	handler := s.Handler()

	var servers []*http.Server
	errCh := make(chan error, len(addrs))

	for _, addr := range addrs {
		srv := &http.Server{
			Addr:         addr,
			Handler:      handler,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 30 * time.Second,
			IdleTimeout:  60 * time.Second,
		}
		servers = append(servers, srv)

		go func(srv *http.Server) {
			s.logger.Printf("listening on %s", srv.Addr)
			if err := srv.ListenAndServe(); err != http.ErrServerClosed {
				errCh <- fmt.Errorf("listener %s: %w", srv.Addr, err)
			}
		}(srv)
	}

	// Wait for context cancellation or a listener error
	var firstErr error
	select {
	case <-ctx.Done():
	case firstErr = <-errCh:
	}

	s.logger.Println("shutting down server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, srv := range servers {
		srv.Shutdown(shutdownCtx)
	}

	return firstErr
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
