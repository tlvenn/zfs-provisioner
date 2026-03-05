package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tlvenn/zfs-provisioner/internal/config"
)

func TestProvision_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/provision" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}

		var req config.ProvisionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}

		if req.Parent != "tank/test" {
			t.Errorf("parent = %q, want %q", req.Parent, "tank/test")
		}

		resp := config.ProvisionResponse{
			Results: []config.DatasetResult{
				{Name: "tank/test/redis", Action: "created"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Parent: "tank/test",
		Datasets: []config.Dataset{
			{Name: "tank/test/redis", Properties: config.ZFSProperties{Quota: "5G"}},
		},
	}

	if err := NewClient(srv.URL).Provision(cfg); err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
}

func TestProvision_PartialFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := config.ProvisionResponse{
			Results: []config.DatasetResult{
				{Name: "tank/test/redis", Action: "created"},
				{Name: "tank/test/logs", Action: "error", Error: "quota exceeds pool"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Parent: "tank/test",
		Datasets: []config.Dataset{
			{Name: "tank/test/redis", Properties: config.ZFSProperties{Quota: "5G"}},
			{Name: "tank/test/logs", Properties: config.ZFSProperties{Quota: "999T"}},
		},
	}

	err := NewClient(srv.URL).Provision(cfg)
	if err == nil {
		t.Fatal("expected error for partial failure")
	}
}

func TestProvision_BadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "parent is required"}`))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Datasets: []config.Dataset{
			{Name: "redis", Properties: config.ZFSProperties{Quota: "5G"}},
		},
	}

	err := NewClient(srv.URL).Provision(cfg)
	if err == nil {
		t.Fatal("expected error for bad request")
	}
}

func TestProvision_ServerUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	cfg := &config.Config{
		Parent: "tank/test",
		Datasets: []config.Dataset{
			{Name: "tank/test/redis", Properties: config.ZFSProperties{Quota: "5G"}},
		},
	}

	c := NewClient(url)
	c.MaxRetry = 1 * time.Second
	c.HTTPClient.Timeout = 500 * time.Millisecond

	err := c.Provision(cfg)
	if err == nil {
		t.Fatal("expected error for unavailable server")
	}
}

func TestBuildRequest(t *testing.T) {
	cfg := &config.Config{
		Parent:   "tank/test",
		Defaults: config.ZFSProperties{Compression: "zstd"},
		Datasets: []config.Dataset{
			{Name: "tank/test/redis", Properties: config.ZFSProperties{Quota: "5G", Compression: "zstd"}},
			{Name: "tank/test/pg", Properties: config.ZFSProperties{Quota: "50G", Compression: "lz4"}},
		},
	}

	req := buildRequest(cfg)

	if req.Parent != "tank/test" {
		t.Errorf("parent = %q, want %q", req.Parent, "tank/test")
	}

	if len(req.Datasets) != 2 {
		t.Fatalf("len(datasets) = %d, want 2", len(req.Datasets))
	}

	// redis has all non-empty properties (no default stripping)
	redisProps, ok := req.Datasets["redis"].(map[string]interface{})
	if !ok {
		t.Fatal("redis dataset not found or wrong type")
	}
	if redisProps["quota"] != "5G" {
		t.Errorf("redis quota = %v, want 5G", redisProps["quota"])
	}
	if redisProps["compression"] != "zstd" {
		t.Errorf("redis compression = %v, want zstd", redisProps["compression"])
	}

	// pg has lz4 compression (overrides default)
	pgProps, ok := req.Datasets["pg"].(map[string]interface{})
	if !ok {
		t.Fatal("pg dataset not found or wrong type")
	}
	if pgProps["compression"] != "lz4" {
		t.Errorf("pg compression = %v, want lz4", pgProps["compression"])
	}
}

func TestBuildRequest_NestedDatasets(t *testing.T) {
	cfg := &config.Config{
		Parent:   "tank/test",
		Defaults: config.ZFSProperties{Compression: "zstd"},
		Datasets: []config.Dataset{
			{Name: "tank/test/postgres/data", Properties: config.ZFSProperties{Quota: "50G", Compression: "zstd", Recordsize: "16K"}},
			{Name: "tank/test/postgres/wal", Properties: config.ZFSProperties{Quota: "10G", Compression: "zstd"}},
		},
	}

	req := buildRequest(cfg)

	// Should reconstruct nested structure
	pgMap, ok := req.Datasets["postgres"].(map[string]interface{})
	if !ok {
		t.Fatal("postgres not found or wrong type")
	}

	dataProps, ok := pgMap["data"].(map[string]interface{})
	if !ok {
		t.Fatal("postgres/data not found or wrong type")
	}
	if dataProps["quota"] != "50G" {
		t.Errorf("postgres/data quota = %v, want 50G", dataProps["quota"])
	}
	if dataProps["recordsize"] != "16K" {
		t.Errorf("postgres/data recordsize = %v, want 16K", dataProps["recordsize"])
	}

	walProps, ok := pgMap["wal"].(map[string]interface{})
	if !ok {
		t.Fatal("postgres/wal not found or wrong type")
	}
	if walProps["quota"] != "10G" {
		t.Errorf("postgres/wal quota = %v, want 10G", walProps["quota"])
	}
}
