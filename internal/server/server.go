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
	"strings"
	"sync"
	"time"

	"github.com/tlvenn/zfs-provisioner/internal/config"
	"github.com/tlvenn/zfs-provisioner/internal/idmap"
	"github.com/tlvenn/zfs-provisioner/internal/mountcheck"
	"github.com/tlvenn/zfs-provisioner/internal/provisioner"
)

const maxRequestBody = 1 << 20 // 1MB

// IdmapRepairer fixes the ownership view of datasets that show up inside
// running Incus containers after they started (see package idmap).
type IdmapRepairer interface {
	Instances() ([]idmap.Instance, error)
	Repair(instances []idmap.Instance, hostMountpoint string) ([]string, error)
}

// Server handles HTTP provisioning requests
type Server struct {
	backend        provisioner.Backend
	allowedParents []string
	repairer       IdmapRepairer
	mu             sync.Mutex
	logger         *log.Logger
}

// Option configures a Server
type Option func(*Server)

// WithAllowedParents restricts provisioning to these datasets and their
// descendants. Without it any parent in any pool is accepted.
func WithAllowedParents(parents []string) Option {
	return func(s *Server) {
		for _, p := range parents {
			if p = strings.Trim(strings.TrimSpace(p), "/"); p != "" {
				s.allowedParents = append(s.allowedParents, p)
			}
		}
	}
}

// WithIdmapRepair re-idmaps provisioned datasets inside running Incus
// containers.
func WithIdmapRepair(r IdmapRepairer) Option {
	return func(s *Server) {
		s.repairer = r
	}
}

// New creates a new Server
func New(backend provisioner.Backend, opts ...Option) *Server {
	s := &Server{
		backend: backend,
		logger:  log.New(os.Stdout, "", log.LstdFlags),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
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

	if status, err := s.checkParent(cfg.Parent); err != nil {
		s.logger.Printf("provision rejected from %s: %v", r.RemoteAddr, err)
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	s.logger.Printf("provisioning %d datasets under %s", len(cfg.Datasets), cfg.Parent)

	// The server performs the zfs mounts, so if it is itself containerized
	// its parent mountpoint needs shared propagation to reach the host.
	if resolver, ok := s.backend.(interface {
		NearestMountpoint(string) (string, error)
	}); ok {
		if mp, err := resolver.NearestMountpoint(cfg.Parent); err == nil && strings.HasPrefix(mp, "/") {
			if err := mountcheck.VerifyPropagation(mp); err != nil {
				s.logger.Printf("provision rejected: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
	}

	s.mu.Lock()
	p := provisioner.New(s.backend, false, false, io.Discard)
	results := p.ProvisionWithResults(cfg)
	if s.repairer != nil {
		s.repairIdmaps(results)
	}
	s.mu.Unlock()

	for _, result := range results {
		if result.Error != "" {
			s.logger.Printf("  %s: error: %s", result.Name, result.Error)
		} else {
			s.logger.Printf("  %s: %s", result.Name, result.Action)
		}
		for _, where := range result.Idmapped {
			s.logger.Printf("    idmapped in %s", where)
		}
	}

	writeJSON(w, http.StatusOK, config.ProvisionResponse{Results: results})
}

// checkParent validates the requested parent dataset against the allowlist.
func (s *Server) checkParent(parent string) (int, error) {
	for _, comp := range strings.Split(parent, "/") {
		if comp == "" || comp == "." || comp == ".." {
			return http.StatusBadRequest, fmt.Errorf("invalid parent dataset %q", parent)
		}
	}

	if len(s.allowedParents) == 0 {
		return 0, nil
	}
	for _, allowed := range s.allowedParents {
		if parent == allowed || strings.HasPrefix(parent, allowed+"/") {
			return 0, nil
		}
	}

	return http.StatusForbidden, fmt.Errorf("parent %q is outside the datasets this server provisions (%s)",
		parent, strings.Join(s.allowedParents, ", "))
}

// repairIdmaps swaps idmapped mounts in for the propagated mounts running
// Incus containers received for these datasets. A failure marks the dataset
// as errored: the container would see it owned by nobody.
func (s *Server) repairIdmaps(results []config.DatasetResult) {
	resolver, ok := s.backend.(interface {
		GetMountpoint(string) (string, error)
	})
	if !ok {
		return
	}

	instances, err := s.repairer.Instances()
	if err != nil {
		for i := range results {
			if results[i].Action != "error" {
				results[i] = config.DatasetResult{Name: results[i].Name, Action: "error", Error: "listing incus instances: " + err.Error()}
			}
		}
		return
	}

	for i := range results {
		if results[i].Action == "error" {
			continue
		}

		mp, err := resolver.GetMountpoint(results[i].Name)
		if err != nil {
			results[i] = config.DatasetResult{Name: results[i].Name, Action: "error", Error: err.Error()}
			continue
		}
		if !strings.HasPrefix(mp, "/") {
			continue
		}

		fixed, err := s.repairer.Repair(instances, mp)
		results[i].Idmapped = fixed
		if err != nil {
			results[i].Action = "error"
			results[i].Error = "idmap repair: " + err.Error()
		}
	}
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
