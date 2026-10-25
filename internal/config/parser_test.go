package config

import (
	"strings"
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

	if cfg.Parents[0].Parent != "tank/docker/stacks/myapp" {
		t.Errorf("Parent = %q, want %q", cfg.Parents[0].Parent, "tank/docker/stacks/myapp")
	}

	if cfg.Parents[0].Defaults.Compression != "zstd" {
		t.Errorf("Defaults.Compression = %q, want %q", cfg.Parents[0].Defaults.Compression, "zstd")
	}

	// Build a map for easier lookup
	datasets := make(map[string]Dataset)
	for _, ds := range cfg.AllDatasets() {
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
	if len(cfg.AllDatasets()) != expectedCount {
		t.Errorf("len(Datasets) = %d, want %d", len(cfg.AllDatasets()), expectedCount)
		for _, ds := range cfg.AllDatasets() {
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

func TestParseEnv(t *testing.T) {
	envValue := `parent: "tank/docker/stacks/myapp"
defaults:
  compression: "zstd"
datasets:
  redis:
    quota: "5G"
  postgres:
    data:
      quota: "50G"`

	cfg, err := ParseEnv(envValue)
	if err != nil {
		t.Fatalf("ParseEnv failed: %v", err)
	}

	if cfg.Parents[0].Parent != "tank/docker/stacks/myapp" {
		t.Errorf("Parent = %q, want %q", cfg.Parents[0].Parent, "tank/docker/stacks/myapp")
	}

	if cfg.Parents[0].Defaults.Compression != "zstd" {
		t.Errorf("Defaults.Compression = %q, want %q", cfg.Parents[0].Defaults.Compression, "zstd")
	}

	// Build a map for easier lookup
	datasets := make(map[string]Dataset)
	for _, ds := range cfg.AllDatasets() {
		datasets[ds.Name] = ds
	}

	// Check datasets
	redis, ok := datasets["tank/docker/stacks/myapp/redis"]
	if !ok {
		t.Error("missing dataset: tank/docker/stacks/myapp/redis")
	} else {
		if redis.Properties.Quota != "5G" {
			t.Errorf("redis.Quota = %q, want %q", redis.Properties.Quota, "5G")
		}
	}

	pgData, ok := datasets["tank/docker/stacks/myapp/postgres/data"]
	if !ok {
		t.Error("missing dataset: tank/docker/stacks/myapp/postgres/data")
	} else {
		if pgData.Properties.Quota != "50G" {
			t.Errorf("postgres/data.Quota = %q, want %q", pgData.Properties.Quota, "50G")
		}
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
	for _, ds := range cfg.AllDatasets() {
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
	if len(cfg.AllDatasets()) != expectedCount {
		t.Errorf("len(Datasets) = %d, want %d", len(cfg.AllDatasets()), expectedCount)
		for _, ds := range cfg.AllDatasets() {
			t.Logf("  - %s", ds.Name)
		}
	}
}

func TestParse_UIDGIDDefaults(t *testing.T) {
	yaml := `
x-zfs:
  parent: "tank/docker/stacks/myapp"
  defaults:
    compression: "zstd"
    uid: "1000"
    gid: "1000"
  datasets:
    redis: {}
    cache:
      quota: "5G"
`

	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Check defaults are parsed
	if cfg.Parents[0].Defaults.UID != "1000" {
		t.Errorf("Defaults.UID = %q, want %q", cfg.Parents[0].Defaults.UID, "1000")
	}
	if cfg.Parents[0].Defaults.GID != "1000" {
		t.Errorf("Defaults.GID = %q, want %q", cfg.Parents[0].Defaults.GID, "1000")
	}

	// Build a map for easier lookup
	datasets := make(map[string]Dataset)
	for _, ds := range cfg.AllDatasets() {
		datasets[ds.Name] = ds
	}

	// Check empty dataset inherits uid/gid from defaults
	redis, ok := datasets["tank/docker/stacks/myapp/redis"]
	if !ok {
		t.Error("missing dataset: tank/docker/stacks/myapp/redis")
	} else {
		if redis.Properties.UID != "1000" {
			t.Errorf("redis.UID = %q, want %q (from defaults)", redis.Properties.UID, "1000")
		}
		if redis.Properties.GID != "1000" {
			t.Errorf("redis.GID = %q, want %q (from defaults)", redis.Properties.GID, "1000")
		}
	}

	// Check dataset with properties also inherits uid/gid
	cache, ok := datasets["tank/docker/stacks/myapp/cache"]
	if !ok {
		t.Error("missing dataset: tank/docker/stacks/myapp/cache")
	} else {
		if cache.Properties.UID != "1000" {
			t.Errorf("cache.UID = %q, want %q (from defaults)", cache.Properties.UID, "1000")
		}
		if cache.Properties.GID != "1000" {
			t.Errorf("cache.GID = %q, want %q (from defaults)", cache.Properties.GID, "1000")
		}
		if cache.Properties.Quota != "5G" {
			t.Errorf("cache.Quota = %q, want %q", cache.Properties.Quota, "5G")
		}
	}
}

func TestParse_UIDGIDOverride(t *testing.T) {
	yaml := `
x-zfs:
  parent: "tank/docker/stacks/myapp"
  defaults:
    uid: "1000"
    gid: "1000"
  datasets:
    redis: {}
    postgres:
      data:
        quota: "50G"
        uid: "999"
        gid: "999"
`

	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Build a map for easier lookup
	datasets := make(map[string]Dataset)
	for _, ds := range cfg.AllDatasets() {
		datasets[ds.Name] = ds
	}

	// Check redis inherits defaults
	redis, ok := datasets["tank/docker/stacks/myapp/redis"]
	if !ok {
		t.Error("missing dataset: tank/docker/stacks/myapp/redis")
	} else {
		if redis.Properties.UID != "1000" {
			t.Errorf("redis.UID = %q, want %q (from defaults)", redis.Properties.UID, "1000")
		}
		if redis.Properties.GID != "1000" {
			t.Errorf("redis.GID = %q, want %q (from defaults)", redis.Properties.GID, "1000")
		}
	}

	// Check postgres/data overrides uid/gid
	pgData, ok := datasets["tank/docker/stacks/myapp/postgres/data"]
	if !ok {
		t.Error("missing dataset: tank/docker/stacks/myapp/postgres/data")
	} else {
		if pgData.Properties.UID != "999" {
			t.Errorf("postgres/data.UID = %q, want %q (override)", pgData.Properties.UID, "999")
		}
		if pgData.Properties.GID != "999" {
			t.Errorf("postgres/data.GID = %q, want %q (override)", pgData.Properties.GID, "999")
		}
		if pgData.Properties.Quota != "50G" {
			t.Errorf("postgres/data.Quota = %q, want %q", pgData.Properties.Quota, "50G")
		}
	}
}

func TestParse_UIDOnly(t *testing.T) {
	yaml := `
x-zfs:
  parent: "tank/docker/stacks/myapp"
  datasets:
    redis:
      uid: "1000"
`

	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Build a map for easier lookup
	datasets := make(map[string]Dataset)
	for _, ds := range cfg.AllDatasets() {
		datasets[ds.Name] = ds
	}

	redis, ok := datasets["tank/docker/stacks/myapp/redis"]
	if !ok {
		t.Error("missing dataset: tank/docker/stacks/myapp/redis")
	} else {
		if redis.Properties.UID != "1000" {
			t.Errorf("redis.UID = %q, want %q", redis.Properties.UID, "1000")
		}
		if redis.Properties.GID != "" {
			t.Errorf("redis.GID = %q, want empty", redis.Properties.GID)
		}
	}
}

func TestParse_InvalidUID(t *testing.T) {
	yaml := `
x-zfs:
  parent: "tank/docker/stacks/myapp"
  datasets:
    redis:
      uid: "--reference=/etc/shadow"
`

	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Error("expected error for non-numeric uid")
	}
}

func TestParse_InvalidGID(t *testing.T) {
	yaml := `
x-zfs:
  parent: "tank/docker/stacks/myapp"
  datasets:
    redis:
      gid: "notanumber"
`

	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Error("expected error for non-numeric gid")
	}
}

func TestParse_NumericUIDWithoutQuotes(t *testing.T) {
	yaml := `
x-zfs:
  parent: "tank/docker/stacks/myapp"
  datasets:
    redis:
      uid: 1000
      gid: 1000
`

	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	datasets := make(map[string]Dataset)
	for _, ds := range cfg.AllDatasets() {
		datasets[ds.Name] = ds
	}

	redis := datasets["tank/docker/stacks/myapp/redis"]
	if redis.Properties.UID != "1000" {
		t.Errorf("redis.UID = %q, want %q", redis.Properties.UID, "1000")
	}
	if redis.Properties.GID != "1000" {
		t.Errorf("redis.GID = %q, want %q", redis.Properties.GID, "1000")
	}
}

func TestParse_GIDOnly(t *testing.T) {
	yaml := `
x-zfs:
  parent: "tank/docker/stacks/myapp"
  datasets:
    redis:
      gid: "1000"
`

	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Build a map for easier lookup
	datasets := make(map[string]Dataset)
	for _, ds := range cfg.AllDatasets() {
		datasets[ds.Name] = ds
	}

	redis, ok := datasets["tank/docker/stacks/myapp/redis"]
	if !ok {
		t.Error("missing dataset: tank/docker/stacks/myapp/redis")
	} else {
		if redis.Properties.UID != "" {
			t.Errorf("redis.UID = %q, want empty", redis.Properties.UID)
		}
		if redis.Properties.GID != "1000" {
			t.Errorf("redis.GID = %q, want %q", redis.Properties.GID, "1000")
		}
	}
}

func TestParse_Parents(t *testing.T) {
	yaml := `
x-zfs:
  defaults:
    uid: "1000"
    gid: "1000"
  parents:
    rpool/apps:
      datasets:
        uchiyomi:
          data: {}
          config: {}
    data/media:
      defaults:
        recordsize: "1M"
        uid: "568"
      datasets:
        manga: {}
`

	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Parents come in name order, whatever the YAML's order
	names := cfg.ParentNames()
	if len(names) != 2 || names[0] != "data/media" || names[1] != "rpool/apps" {
		t.Fatalf("ParentNames() = %v, want [data/media rpool/apps]", names)
	}
	if want := (ZFSProperties{Recordsize: "1M", UID: "568", GID: "1000"}); cfg.Parents[0].Defaults != want {
		t.Errorf("data/media defaults = %+v, want %+v", cfg.Parents[0].Defaults, want)
	}

	want := map[string]ZFSProperties{
		"rpool/apps/uchiyomi/data":   {UID: "1000", GID: "1000"},
		"rpool/apps/uchiyomi/config": {UID: "1000", GID: "1000"},
		"data/media/manga":           {Recordsize: "1M", UID: "568", GID: "1000"},
	}
	got := cfg.AllDatasets()
	if len(got) != len(want) {
		t.Fatalf("len(AllDatasets()) = %d, want %d: %+v", len(got), len(want), got)
	}
	for _, ds := range got {
		if props, ok := want[ds.Name]; !ok {
			t.Errorf("unexpected dataset %s", ds.Name)
		} else if ds.Properties != props {
			t.Errorf("%s properties = %+v, want %+v", ds.Name, ds.Properties, props)
		}
	}
}

func TestParse_ParentsErrors(t *testing.T) {
	cases := []struct {
		name string
		xzfs string
		want string
	}{
		{"parent and parents", `
  parent: "tank/a"
  parents:
    tank/b:
      datasets:
        x: {}`, "exclusive"},
		{"datasets at the top", `
  datasets:
    x: {}
  parents:
    tank/b:
      datasets:
        y: {}`, "goes under each parent"},
		{"no parents", `
  parents: {}`, "must map each parent"},
		{"parent not a map", `
  parents:
    tank/b: "x"`, "must be a map"},
		{"unknown key", `
  parents:
    tank/b:
      dataset:
        x: {}`, "unknown key"},
		{"bad parent defaults", `
  parents:
    tank/b:
      defaults:
        uid: "abc"
      datasets:
        x: {}`, "x-zfs.parents.tank/b.defaults"},
		{"dataset under two parents", `
  parents:
    rpool/apps:
      datasets:
        app:
          data: {}
    rpool/apps/app:
      datasets:
        data: {}`, "listed under both"},
	}

	for _, tc := range cases {
		_, err := Parse([]byte("x-zfs:" + tc.xzfs + "\n"))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want one containing %q", tc.name, err, tc.want)
		}
	}
}

func TestParseEnv_Parents(t *testing.T) {
	value := `parents:
  rpool/apps:
    datasets:
      app: {}
  data/media:
    datasets:
      library: {}`

	cfg, err := ParseEnv(value)
	if err != nil {
		t.Fatalf("ParseEnv failed: %v", err)
	}
	if len(cfg.AllDatasets()) != 2 {
		t.Errorf("len(AllDatasets()) = %d, want 2", len(cfg.AllDatasets()))
	}
}

func TestBuildConfig_Parents(t *testing.T) {
	cfg, err := BuildConfig(ProvisionRequest{
		Defaults: ZFSProperties{Compression: "zstd"},
		Parents: map[string]ProvisionParent{
			"tank/b": {Defaults: ZFSProperties{Quota: "1G"}, Datasets: map[string]interface{}{"x": map[string]interface{}{}}},
			"tank/a": {Datasets: map[string]interface{}{"y": map[string]interface{}{"quota": "5G"}}},
		},
	})
	if err != nil {
		t.Fatalf("BuildConfig failed: %v", err)
	}

	if names := cfg.ParentNames(); len(names) != 2 || names[0] != "tank/a" || names[1] != "tank/b" {
		t.Fatalf("ParentNames() = %v, want [tank/a tank/b]", names)
	}
	want := map[string]ZFSProperties{
		"tank/a/y": {Compression: "zstd", Quota: "5G"},
		"tank/b/x": {Compression: "zstd", Quota: "1G"},
	}
	for _, ds := range cfg.AllDatasets() {
		if ds.Properties != want[ds.Name] {
			t.Errorf("%s properties = %+v, want %+v", ds.Name, ds.Properties, want[ds.Name])
		}
	}
}

func TestBuildConfig_Errors(t *testing.T) {
	cases := []struct {
		name string
		req  ProvisionRequest
		want string
	}{
		{"neither", ProvisionRequest{}, "parent is required"},
		{"both", ProvisionRequest{Parent: "tank/a", Parents: map[string]ProvisionParent{"tank/b": {}}}, "exclusive"},
		{"datasets at the top", ProvisionRequest{
			Datasets: map[string]interface{}{"x": map[string]interface{}{}},
			Parents:  map[string]ProvisionParent{"tank/b": {}},
		}, "under each parent"},
		{"empty parent name", ProvisionRequest{Parents: map[string]ProvisionParent{"": {}}}, "parent is required"},
	}

	for _, tc := range cases {
		_, err := BuildConfig(tc.req)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want one containing %q", tc.name, err, tc.want)
		}
	}
}
