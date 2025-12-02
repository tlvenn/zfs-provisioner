package config

import (
	"testing"
)

func TestParse(t *testing.T) {
	yaml := `
x-zfs:
  parent: "tank/docker/stacks/myapp"
  defaults:
    compression: "zstd"
  datasets:
    redis:
      quota: "5G"
    postgres:
      data:
        quota: "50G"
        recordsize: "16K"
      wal:
        quota: "10G"
    app:
      config:
        quota: "1G"
      data:
        quota: "100G"
      logs:
        quota: "20G"
        compression: "lz4"

services:
  redis:
    image: redis:7
`

	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Parent != "tank/docker/stacks/myapp" {
		t.Errorf("Parent = %q, want %q", cfg.Parent, "tank/docker/stacks/myapp")
	}

	if cfg.Defaults.Compression != "zstd" {
		t.Errorf("Defaults.Compression = %q, want %q", cfg.Defaults.Compression, "zstd")
	}

	// Build a map for easier lookup
	datasets := make(map[string]Dataset)
	for _, ds := range cfg.Datasets {
		datasets[ds.Name] = ds
	}

	// Check simple form
	redis, ok := datasets["tank/docker/stacks/myapp/redis"]
	if !ok {
		t.Error("missing dataset: tank/docker/stacks/myapp/redis")
	} else {
		if redis.Properties.Quota != "5G" {
			t.Errorf("redis.Quota = %q, want %q", redis.Properties.Quota, "5G")
		}
		if redis.Properties.Compression != "zstd" {
			t.Errorf("redis.Compression = %q, want %q (from defaults)", redis.Properties.Compression, "zstd")
		}
	}

	// Check nested form
	pgData, ok := datasets["tank/docker/stacks/myapp/postgres/data"]
	if !ok {
		t.Error("missing dataset: tank/docker/stacks/myapp/postgres/data")
	} else {
		if pgData.Properties.Quota != "50G" {
			t.Errorf("postgres/data.Quota = %q, want %q", pgData.Properties.Quota, "50G")
		}
		if pgData.Properties.Recordsize != "16K" {
			t.Errorf("postgres/data.Recordsize = %q, want %q", pgData.Properties.Recordsize, "16K")
		}
		if pgData.Properties.Compression != "zstd" {
			t.Errorf("postgres/data.Compression = %q, want %q (from defaults)", pgData.Properties.Compression, "zstd")
		}
	}

	pgWal, ok := datasets["tank/docker/stacks/myapp/postgres/wal"]
	if !ok {
		t.Error("missing dataset: tank/docker/stacks/myapp/postgres/wal")
	} else {
		if pgWal.Properties.Quota != "10G" {
			t.Errorf("postgres/wal.Quota = %q, want %q", pgWal.Properties.Quota, "10G")
		}
	}

	// Check override of defaults
	appLogs, ok := datasets["tank/docker/stacks/myapp/app/logs"]
	if !ok {
		t.Error("missing dataset: tank/docker/stacks/myapp/app/logs")
	} else {
		if appLogs.Properties.Compression != "lz4" {
			t.Errorf("app/logs.Compression = %q, want %q (override)", appLogs.Properties.Compression, "lz4")
		}
	}

	// Count total datasets
	expectedCount := 6 // redis, postgres/data, postgres/wal, app/config, app/data, app/logs
	if len(cfg.Datasets) != expectedCount {
		t.Errorf("len(Datasets) = %d, want %d", len(cfg.Datasets), expectedCount)
		for _, ds := range cfg.Datasets {
			t.Logf("  - %s", ds.Name)
		}
	}
}

func TestParse_MissingParent(t *testing.T) {
	yaml := `
x-zfs:
  datasets:
    redis:
      quota: "5G"
`

	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Error("expected error for missing parent")
	}
}

func TestParse_MissingXZFS(t *testing.T) {
	yaml := `
services:
  redis:
    image: redis:7
`

	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Error("expected error for missing x-zfs section")
	}
}

func TestParse_EmptyDataset(t *testing.T) {
	yaml := `
x-zfs:
  parent: "tank/docker/stacks/myapp"
  defaults:
    compression: "zstd"
  datasets:
    redis: {}
    cache: {}
    postgres:
      data:
        quota: "50G"
`

	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Build a map for easier lookup
	datasets := make(map[string]Dataset)
	for _, ds := range cfg.Datasets {
		datasets[ds.Name] = ds
	}

	// Check empty dataset gets defaults
	redis, ok := datasets["tank/docker/stacks/myapp/redis"]
	if !ok {
		t.Error("missing dataset: tank/docker/stacks/myapp/redis")
	} else {
		if redis.Properties.Compression != "zstd" {
			t.Errorf("redis.Compression = %q, want %q (from defaults)", redis.Properties.Compression, "zstd")
		}
		if redis.Properties.Quota != "" {
			t.Errorf("redis.Quota = %q, want empty", redis.Properties.Quota)
		}
	}

	cache, ok := datasets["tank/docker/stacks/myapp/cache"]
	if !ok {
		t.Error("missing dataset: tank/docker/stacks/myapp/cache")
	} else {
		if cache.Properties.Compression != "zstd" {
			t.Errorf("cache.Compression = %q, want %q (from defaults)", cache.Properties.Compression, "zstd")
		}
	}

	// Count total datasets
	expectedCount := 3 // redis, cache, postgres/data
	if len(cfg.Datasets) != expectedCount {
		t.Errorf("len(Datasets) = %d, want %d", len(cfg.Datasets), expectedCount)
		for _, ds := range cfg.Datasets {
			t.Logf("  - %s", ds.Name)
		}
	}
}
