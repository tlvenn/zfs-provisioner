package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
		Parents: []config.ParentConfig{{
			Parent: "tank/test",
			Datasets: []config.Dataset{
				{Name: "tank/test/redis", Properties: config.ZFSProperties{Quota: "5G"}},
			},
		}},
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
		Parents: []config.ParentConfig{{
			Parent: "tank/test",
			Datasets: []config.Dataset{
				{Name: "tank/test/redis", Properties: config.ZFSProperties{Quota: "5G"}},
				{Name: "tank/test/logs", Properties: config.ZFSProperties{Quota: "999T"}},
			},
		}},
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
		Parents: []config.ParentConfig{{
			Datasets: []config.Dataset{
				{Name: "redis", Properties: config.ZFSProperties{Quota: "5G"}},
			},
		}},
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
		Parents: []config.ParentConfig{{
			Parent: "tank/test",
			Datasets: []config.Dataset{
				{Name: "tank/test/redis", Properties: config.ZFSProperties{Quota: "5G"}},
			},
		}},
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
		Parents: []config.ParentConfig{{
			Parent:   "tank/test",
			Defaults: config.ZFSProperties{Compression: "zstd"},
			Datasets: []config.Dataset{
				{Name: "tank/test/redis", Properties: config.ZFSProperties{Quota: "5G", Compression: "zstd"}},
				{Name: "tank/test/pg", Properties: config.ZFSProperties{Quota: "50G", Compression: "lz4"}},
			},
		}},
	}

	req := buildRequest(cfg)

	if req.Parent != "tank/test" {
		t.Errorf("parent = %q, want %q", req.Parent, "tank/test")
	}
	// One parent goes in the original form, which older servers accept
	if req.Parents != nil {
		t.Errorf("parents = %v, want none for a single parent", req.Parents)
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
		Parents: []config.ParentConfig{{
			Parent:   "tank/test",
			Defaults: config.ZFSProperties{Compression: "zstd"},
			Datasets: []config.Dataset{
				{Name: "tank/test/postgres/data", Properties: config.ZFSProperties{Quota: "50G", Compression: "zstd", Recordsize: "16K"}},
				{Name: "tank/test/postgres/wal", Properties: config.ZFSProperties{Quota: "10G", Compression: "zstd"}},
			},
		}},
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

func TestBuildRequest_Parents(t *testing.T) {
	cfg := &config.Config{
		Parents: []config.ParentConfig{
			{
				Parent:   "data/media",
				Defaults: config.ZFSProperties{Recordsize: "1M"},
				Datasets: []config.Dataset{
					{Name: "data/media/manga/library", Properties: config.ZFSProperties{Recordsize: "1M"}},
				},
			},
			{
				Parent: "rpool/apps",
				Datasets: []config.Dataset{
					{Name: "rpool/apps/uchiyomi/data", Properties: config.ZFSProperties{UID: "1000"}},
				},
			},
		},
	}

	req := buildRequest(cfg)

	if req.Parent != "" || req.Datasets != nil {
		t.Errorf("parent = %q, datasets = %v: want neither with several parents", req.Parent, req.Datasets)
	}
	if len(req.Parents) != 2 {
		t.Fatalf("len(parents) = %d, want 2", len(req.Parents))
	}
	if req.Parents["data/media"].Defaults.Recordsize != "1M" {
		t.Errorf("data/media defaults = %+v, want recordsize 1M", req.Parents["data/media"].Defaults)
	}
	manga, ok := req.Parents["data/media"].Datasets["manga"].(map[string]interface{})
	if !ok {
		t.Fatal("data/media: manga not found or wrong type")
	}
	if library, ok := manga["library"].(map[string]interface{}); !ok || library["recordsize"] != "1M" {
		t.Errorf("data/media: manga/library = %v, want recordsize 1M", manga["library"])
	}
	uchiyomi, ok := req.Parents["rpool/apps"].Datasets["uchiyomi"].(map[string]interface{})
	if !ok {
		t.Fatal("rpool/apps: uchiyomi not found or wrong type")
	}
	if data, ok := uchiyomi["data"].(map[string]interface{}); !ok || data["uid"] != "1000" {
		t.Errorf("rpool/apps: uchiyomi/data = %v, want uid 1000", uchiyomi["data"])
	}

	// No top-level parent key on the wire, so an older server refuses the
	// request instead of provisioning part of it
	body, _ := json.Marshal(req)
	if strings.Contains(string(body), `"parent":`) {
		t.Errorf("request carries a top-level parent: %s", body)
	}
}

// What the client sends, the server must rebuild into the same datasets
func TestBuildRequest_RoundTrip(t *testing.T) {
	for _, value := range []string{
		`parent: "tank/app"
defaults:
  compression: "zstd"
datasets:
  redis:
    quota: "5G"
  postgres:
    data:
      recordsize: "16K"
    wal: {}`,
		`defaults:
  uid: "1000"
parents:
  rpool/apps:
    datasets:
      uchiyomi:
        data: {}
        config: {}
  data/media:
    defaults:
      recordsize: "1M"
    datasets:
      manga:
        library: {}`,
	} {
		cfg, err := config.ParseEnv(value)
		if err != nil {
			t.Fatalf("ParseEnv failed: %v", err)
		}

		// Through JSON, as on the wire
		body, err := json.Marshal(buildRequest(cfg))
		if err != nil {
			t.Fatal(err)
		}
		var req config.ProvisionRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		rebuilt, err := config.BuildConfig(req)
		if err != nil {
			t.Fatalf("BuildConfig failed: %v", err)
		}

		want := map[string]config.ZFSProperties{}
		for _, ds := range cfg.AllDatasets() {
			want[ds.Name] = ds.Properties
		}
		got := rebuilt.AllDatasets()
		if len(got) != len(want) {
			t.Errorf("rebuilt %d datasets, want %d: %+v", len(got), len(want), got)
		}
		for _, ds := range got {
			if props, ok := want[ds.Name]; !ok || props != ds.Properties {
				t.Errorf("rebuilt %s = %+v, want %+v", ds.Name, ds.Properties, props)
			}
		}
	}
}
