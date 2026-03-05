package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tlvenn/zfs-provisioner/internal/config"
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
