package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tlvenn/zfs-provisioner/internal/config"
	"github.com/tlvenn/zfs-provisioner/internal/idmap"
)

// mockBackend implements provisioner.Backend for testing
type mockBackend struct {
	datasets map[string]config.ZFSProperties
}

func newMockBackend() *mockBackend {
	return &mockBackend{datasets: make(map[string]config.ZFSProperties)}
}

func (m *mockBackend) DatasetExists(name string) (bool, error) {
	_, exists := m.datasets[name]
	return exists, nil
}

func (m *mockBackend) CreateDataset(name string, props config.ZFSProperties) error {
	m.datasets[name] = props
	return nil
}

func (m *mockBackend) UpdateProperties(name string, desired config.ZFSProperties) ([]string, error) {
	m.datasets[name] = desired
	return nil, nil
}

func (m *mockBackend) VerifyMounted(name string) error {
	return nil
}

func (m *mockBackend) GetMountpoint(name string) (string, error) {
	return "/" + name, nil
}

func TestHandleProvision_CreateDatasets(t *testing.T) {
	backend := newMockBackend()
	srv := New(backend)

	body := `{
		"parent": "tank/test",
		"defaults": {"compression": "zstd"},
		"datasets": {
			"redis": {"quota": "5G"},
			"postgres": {"quota": "50G", "recordsize": "16K"}
		}
	}`

	req := httptest.NewRequest("POST", "/provision", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp config.ProvisionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp.Results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(resp.Results))
	}

	// Check all datasets were created
	for _, r := range resp.Results {
		if r.Action != "created" {
			t.Errorf("dataset %s: action = %q, want %q", r.Name, r.Action, "created")
		}
	}

	// Verify backend received the datasets
	if _, ok := backend.datasets["tank/test/redis"]; !ok {
		t.Error("backend missing dataset: tank/test/redis")
	}
	if _, ok := backend.datasets["tank/test/postgres"]; !ok {
		t.Error("backend missing dataset: tank/test/postgres")
	}
}

func TestHandleProvision_ExistingDataset(t *testing.T) {
	backend := newMockBackend()
	backend.datasets["tank/test/redis"] = config.ZFSProperties{Quota: "5G"}
	srv := New(backend)

	body := `{
		"parent": "tank/test",
		"datasets": {
			"redis": {"quota": "5G"}
		}
	}`

	req := httptest.NewRequest("POST", "/provision", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp config.ProvisionResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	if len(resp.Results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(resp.Results))
	}

	if resp.Results[0].Action != "unchanged" {
		t.Errorf("action = %q, want %q", resp.Results[0].Action, "unchanged")
	}
}

func TestHandleProvision_MissingParent(t *testing.T) {
	srv := New(newMockBackend())

	body := `{"datasets": {"redis": {"quota": "5G"}}}`
	req := httptest.NewRequest("POST", "/provision", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleProvision_InvalidJSON(t *testing.T) {
	srv := New(newMockBackend())

	req := httptest.NewRequest("POST", "/provision", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleProvision_NestedDatasets(t *testing.T) {
	backend := newMockBackend()
	srv := New(backend)

	body := `{
		"parent": "tank/test",
		"defaults": {"compression": "zstd"},
		"datasets": {
			"postgres": {
				"data": {"quota": "50G", "recordsize": "16K"},
				"wal": {"quota": "10G"}
			}
		}
	}`

	req := httptest.NewRequest("POST", "/provision", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp config.ProvisionResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	if len(resp.Results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(resp.Results))
	}

	// Verify backend received nested datasets
	if _, ok := backend.datasets["tank/test/postgres/data"]; !ok {
		t.Error("backend missing dataset: tank/test/postgres/data")
	}
	if _, ok := backend.datasets["tank/test/postgres/wal"]; !ok {
		t.Error("backend missing dataset: tank/test/postgres/wal")
	}
}

func TestHandleProvision_InvalidDatasetName(t *testing.T) {
	srv := New(newMockBackend())

	body := `{
		"parent": "tank/test",
		"datasets": {
			"../escape": {"quota": "5G"}
		}
	}`

	req := httptest.NewRequest("POST", "/provision", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	if !strings.Contains(w.Body.String(), "invalid dataset name") {
		t.Errorf("expected invalid dataset name error, got: %s", w.Body.String())
	}
}

func TestHandleProvision_OversizedBody(t *testing.T) {
	srv := New(newMockBackend())

	// Create a body larger than 1MB
	bigBody := `{"parent": "tank/test", "datasets": {"x": {"quota": "` + strings.Repeat("A", 2<<20) + `"}}}`

	req := httptest.NewRequest("POST", "/provision", bytes.NewBufferString(bigBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d for oversized body", w.Code, http.StatusBadRequest)
	}
}

func provision(t *testing.T, srv *Server, body string) (*httptest.ResponseRecorder, config.ProvisionResponse) {
	t.Helper()
	req := httptest.NewRequest("POST", "/provision", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	var resp config.ProvisionResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}
	}
	return w, resp
}

func TestHandleProvision_AllowedParents(t *testing.T) {
	cases := []struct {
		parent string
		want   int
	}{
		{"rpool/data/magelo/ops", http.StatusOK},
		{"rpool/data/magelo/ops/traceway", http.StatusOK},
		{"rpool/data/magelo/opsx", http.StatusForbidden},
		{"rpool/data/magelo/prod", http.StatusForbidden},
		{"rpool/data/magelo/ops/../prod", http.StatusBadRequest},
		{"rpool/data/magelo/ops//x", http.StatusBadRequest},
	}

	for _, tc := range cases {
		backend := newMockBackend()
		srv := New(backend, WithAllowedParents([]string{" rpool/data/magelo/ops/ ", "tank/other"}))

		w, _ := provision(t, srv, `{"parent": "`+tc.parent+`", "datasets": {"app": {}}}`)
		if w.Code != tc.want {
			t.Errorf("parent %q: status = %d, want %d; body: %s", tc.parent, w.Code, tc.want, w.Body.String())
		}
		if tc.want != http.StatusOK && len(backend.datasets) != 0 {
			t.Errorf("parent %q: rejected request still provisioned %v", tc.parent, backend.datasets)
		}
	}
}

func TestHandleProvision_Parents(t *testing.T) {
	backend := newMockBackend()
	srv := New(backend, WithAllowedParents([]string{"rpool/apps", "data/media"}))

	w, resp := provision(t, srv, `{
		"defaults": {"uid": "1000"},
		"parents": {
			"rpool/apps": {"datasets": {"uchiyomi": {"data": {}, "config": {}}}},
			"data/media": {"defaults": {"recordsize": "1M"}, "datasets": {"manga": {}}}
		}
	}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if len(resp.Results) != 3 {
		t.Fatalf("len(results) = %d, want 3: %+v", len(resp.Results), resp.Results)
	}

	want := map[string]config.ZFSProperties{
		"rpool/apps/uchiyomi/data":   {UID: "1000"},
		"rpool/apps/uchiyomi/config": {UID: "1000"},
		"data/media/manga":           {UID: "1000", Recordsize: "1M"},
	}
	for name, props := range want {
		if got, ok := backend.datasets[name]; !ok {
			t.Errorf("backend missing dataset: %s", name)
		} else if got != props {
			t.Errorf("%s properties = %+v, want %+v", name, got, props)
		}
	}
}

func TestHandleProvision_ParentsOneForbidden(t *testing.T) {
	backend := newMockBackend()
	srv := New(backend, WithAllowedParents([]string{"rpool/apps"}))

	w, _ := provision(t, srv, `{"parents": {
		"rpool/apps": {"datasets": {"app": {}}},
		"data/media": {"datasets": {"manga": {}}}
	}}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
	// Refused as a whole: not even the allowed parent's datasets
	if len(backend.datasets) != 0 {
		t.Errorf("rejected request still provisioned %v", backend.datasets)
	}
}

func TestHandleProvision_ParentAndParents(t *testing.T) {
	w, _ := provision(t, New(newMockBackend()), `{"parent": "tank/a", "parents": {"tank/b": {}}}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

type fakeRepairer struct {
	instancesErr error
	fixed        map[string][]string
	errs         map[string]error
	repaired     []string
}

func (f *fakeRepairer) Instances() ([]idmap.Instance, error) {
	return nil, f.instancesErr
}

func (f *fakeRepairer) Repair(instances []idmap.Instance, mp string) ([]string, error) {
	f.repaired = append(f.repaired, mp)
	return f.fixed[mp], f.errs[mp]
}

func TestHandleProvision_IdmapRepair(t *testing.T) {
	repairer := &fakeRepairer{
		fixed: map[string][]string{"/tank/test/redis": {"magelo/ops:/data/redis"}},
		errs:  map[string]error{"/tank/test/postgres": errors.New("move_mount failed")},
	}
	srv := New(newMockBackend(), WithIdmapRepair(repairer))

	w, resp := provision(t, srv, `{"parent": "tank/test", "datasets": {"redis": {}, "postgres": {}}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}

	byName := map[string]config.DatasetResult{}
	for _, r := range resp.Results {
		byName[r.Name] = r
	}
	if r := byName["tank/test/redis"]; r.Action != "created" || len(r.Idmapped) != 1 || r.Idmapped[0] != "magelo/ops:/data/redis" {
		t.Errorf("unexpected redis result %+v", r)
	}
	if r := byName["tank/test/postgres"]; r.Action != "error" || !strings.Contains(r.Error, "move_mount failed") {
		t.Errorf("expected postgres repair error, got %+v", r)
	}
}

func TestHandleProvision_IdmapRepairInstancesError(t *testing.T) {
	repairer := &fakeRepairer{instancesErr: errors.New("incus down")}
	srv := New(newMockBackend(), WithIdmapRepair(repairer))

	_, resp := provision(t, srv, `{"parent": "tank/test", "datasets": {"redis": {}}}`)
	if len(resp.Results) != 1 || resp.Results[0].Action != "error" || !strings.Contains(resp.Results[0].Error, "incus down") {
		t.Errorf("expected every dataset to fail, got %+v", resp.Results)
	}
	if len(repairer.repaired) != 0 {
		t.Errorf("expected no repair attempt, got %v", repairer.repaired)
	}
}
