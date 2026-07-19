package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/cases"
	"github.com/anmingwei/multi-agent-platform/internal/config"
	"github.com/anmingwei/multi-agent-platform/internal/harness"
	"github.com/anmingwei/multi-agent-platform/pkg/db"

	_ "modernc.org/sqlite"
)

// setupCaseTestDB creates a fresh SQLite database with the full schema.
func setupCaseTestDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "test.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		db.DB = nil
	})
}

// newCaseService creates an initialized case service for tests.
func newCaseService(t *testing.T) *cases.Service {
	t.Helper()
	setupCaseTestDB(t)
	svc, err := cases.Init(db.DB)
	if err != nil {
		t.Fatalf("init case service: %v", err)
	}
	return svc
}

// newTestCreateRequest returns a valid create payload.
func newTestCreateRequest() cases.CreateCaseRequest {
	return cases.CreateCaseRequest{
		Name:         "Test Case",
		Description:  "A test case",
		Icon:         "🧪",
		Category:     "test",
		SystemPrompt: "You are a test agent.",
		DefaultInput: "Run the test.",
		Contract: &harness.TaskContract{
			MaxSteps:    10,
			Permissions: harness.TaskPermissions{AllowFileWrite: true},
		},
		Tags: []string{"test", "custom"},
	}
}

// TestHandleListCases returns all cases by default.
func TestHandleListCases(t *testing.T) {
	svc := newCaseService(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleListCases(w, r, svc)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/cases", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var result []cases.Case
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(result) < 5 {
		t.Fatalf("expected at least 5 builtin cases, got %d", len(result))
	}
}

// TestHandleListCasesByCategory filters by category.
func TestHandleListCasesByCategory(t *testing.T) {
	svc := newCaseService(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleListCases(w, r, svc)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/cases?category=generation", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var result []cases.Case
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	found := false
	for _, c := range result {
		if c.ID == "code-gen" {
			found = true
		}
		if !strings.EqualFold(c.Category, "generation") {
			t.Errorf("case %s has wrong category %s", c.ID, c.Category)
		}
	}
	if !found {
		t.Errorf("expected code-gen in generation category")
	}
}

// TestHandleListCasesByTagOr filters with OR semantics.
func TestHandleListCasesByTagOr(t *testing.T) {
	svc := newCaseService(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleListCases(w, r, svc)
	})

	// Both dialogue and code-gen should match.
	req := httptest.NewRequest(http.MethodGet, "/api/cases?tag=dialogue,code", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var result []cases.Case
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	ids := make(map[string]bool)
	for _, c := range result {
		ids[c.ID] = true
	}
	if !ids["dialogue"] {
		t.Errorf("expected dialogue case to match tag=dialogue,code")
	}
	if !ids["code-gen"] {
		t.Errorf("expected code-gen case to match tag=dialogue,code")
	}
}

// TestHandleGetCaseBuiltin returns a builtin case.
func TestHandleGetCaseBuiltin(t *testing.T) {
	svc := newCaseService(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleGetCase(w, r, "code-gen", svc)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/cases/code-gen", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var c cases.Case
	if err := json.Unmarshal(rr.Body.Bytes(), &c); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if c.ID != "code-gen" {
		t.Errorf("expected code-gen, got %s", c.ID)
	}
}

// TestHandleGetCaseNotFound returns 404.
func TestHandleGetCaseNotFound(t *testing.T) {
	svc := newCaseService(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleGetCase(w, r, "not-exist", svc)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/cases/not-exist", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestHandleCreateCase creates a custom case.
func TestHandleCreateCase(t *testing.T) {
	svc := newCaseService(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleCreateCase(w, r, svc)
	})

	payload, _ := json.Marshal(newTestCreateRequest())
	req := httptest.NewRequest(http.MethodPost, "/api/cases", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var c cases.Case
	if err := json.Unmarshal(rr.Body.Bytes(), &c); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if c.ID == "" || strings.HasPrefix(c.ID, "builtin") {
		t.Errorf("expected custom case id, got %s", c.ID)
	}
	if c.IsBuiltin {
		t.Errorf("custom case should not be builtin")
	}
	if c.Name != "Test Case" {
		t.Errorf("expected name %q, got %q", "Test Case", c.Name)
	}
}

// TestHandleCreateCaseValidation rejects invalid request.
func TestHandleCreateCaseValidation(t *testing.T) {
	svc := newCaseService(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleCreateCase(w, r, svc)
	})

	invalid := newTestCreateRequest()
	invalid.Name = "   "
	payload, _ := json.Marshal(invalid)
	req := httptest.NewRequest(http.MethodPost, "/api/cases", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestHandleUpdateCase updates a custom case.
func TestHandleUpdateCase(t *testing.T) {
	svc := newCaseService(t)
	created, err := svc.Create(newTestCreateRequest())
	if err != nil {
		t.Fatalf("create case: %v", err)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleUpdateCase(w, r, created.ID, svc)
	})

	newName := "Updated Case"
	payload, _ := json.Marshal(cases.UpdateCaseRequest{Name: &newName})
	req := httptest.NewRequest(http.MethodPut, "/api/cases/"+created.ID, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var c cases.Case
	if err := json.Unmarshal(rr.Body.Bytes(), &c); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if c.Name != "Updated Case" {
		t.Errorf("expected updated name, got %q", c.Name)
	}
}

// TestHandleUpdateBuiltinCaseRejected returns 403.
func TestHandleUpdateBuiltinCaseRejected(t *testing.T) {
	svc := newCaseService(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleUpdateCase(w, r, "code-gen", svc)
	})

	newName := "Hacked"
	payload, _ := json.Marshal(cases.UpdateCaseRequest{Name: &newName})
	req := httptest.NewRequest(http.MethodPut, "/api/cases/code-gen", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestHandleUpdateCaseNotFound returns 404.
func TestHandleUpdateCaseNotFound(t *testing.T) {
	svc := newCaseService(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleUpdateCase(w, r, "case-notfound", svc)
	})

	newName := "Ghost"
	payload, _ := json.Marshal(cases.UpdateCaseRequest{Name: &newName})
	req := httptest.NewRequest(http.MethodPut, "/api/cases/case-notfound", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestHandleDeleteCase deletes a custom case.
func TestHandleDeleteCase(t *testing.T) {
	svc := newCaseService(t)
	created, err := svc.Create(newTestCreateRequest())
	if err != nil {
		t.Fatalf("create case: %v", err)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleDeleteCase(w, r, created.ID, svc)
	})

	req := httptest.NewRequest(http.MethodDelete, "/api/cases/"+created.ID, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("expected empty body for 204")
	}

	if _, err := svc.Get(created.ID); err == nil {
		t.Errorf("expected deleted case to be gone")
	}
}

// TestHandleDeleteBuiltinCaseRejected returns 403.
func TestHandleDeleteBuiltinCaseRejected(t *testing.T) {
	svc := newCaseService(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleDeleteCase(w, r, "dialogue", svc)
	})

	req := httptest.NewRequest(http.MethodDelete, "/api/cases/dialogue", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestHandleContractLimits returns server-enforced contract bounds as JSON.
func TestHandleContractLimits(t *testing.T) {
	cfg := &config.Config{
		ContractLimits: config.ContractLimits{
			MaxSteps:          25,
			MaxTokensPerStep:  2048,
			MaxTimeoutSeconds: 3600,
			MaxSubAgents:      5,
			MaxInputLength:    5000,
			Scopes:            []string{"read_only", "standard"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/contract-limits", nil)
	rr := httptest.NewRecorder()

	// handleContractLimits returns an http.HandlerFunc bound to cfg.
	handler := handleContractLimits(cfg)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var got config.ContractLimits
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if got.MaxSteps != cfg.ContractLimits.MaxSteps {
		t.Errorf("MaxSteps = %d, want %d", got.MaxSteps, cfg.ContractLimits.MaxSteps)
	}
	if got.MaxTokensPerStep != cfg.ContractLimits.MaxTokensPerStep {
		t.Errorf("MaxTokensPerStep = %d, want %d", got.MaxTokensPerStep, cfg.ContractLimits.MaxTokensPerStep)
	}
	if got.MaxTimeoutSeconds != cfg.ContractLimits.MaxTimeoutSeconds {
		t.Errorf("MaxTimeoutSeconds = %d, want %d", got.MaxTimeoutSeconds, cfg.ContractLimits.MaxTimeoutSeconds)
	}
	if got.MaxSubAgents != cfg.ContractLimits.MaxSubAgents {
		t.Errorf("MaxSubAgents = %d, want %d", got.MaxSubAgents, cfg.ContractLimits.MaxSubAgents)
	}
	if got.MaxInputLength != cfg.ContractLimits.MaxInputLength {
		t.Errorf("MaxInputLength = %d, want %d", got.MaxInputLength, cfg.ContractLimits.MaxInputLength)
	}
}

// TestCaseRoutesIntegration runs the full API through an httptest server.
func TestCaseRoutesIntegration(t *testing.T) {
	svc := newCaseService(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/cases", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleListCases(w, r, svc)
		case http.MethodPost:
			handleCreateCase(w, r, svc)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/cases/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/cases/")
		if id == "" {
			http.Error(w, "case ID required", http.StatusBadRequest)
			return
		}
		switch r.Method {
		case http.MethodGet:
			handleGetCase(w, r, id, svc)
		case http.MethodPut:
			handleUpdateCase(w, r, id, svc)
		case http.MethodDelete:
			handleDeleteCase(w, r, id, svc)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := ts.Client()

	// 1. List cases
	resp, err := client.Get(ts.URL + "/api/cases")
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing cases, got %d: %s", resp.StatusCode, string(body))
	}

	// 2. Create case
	payload, _ := json.Marshal(newTestCreateRequest())
	resp, err = client.Post(ts.URL+"/api/cases", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating case, got %d: %s", resp.StatusCode, string(body))
	}
	var created cases.Case
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created case: %v", err)
	}

	// 3. Get case
	resp, err = client.Get(ts.URL + "/api/cases/" + created.ID)
	if err != nil {
		t.Fatalf("get case: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 getting case, got %d: %s", resp.StatusCode, string(body))
	}

	// 4. Filter by category
	resp, err = client.Get(ts.URL + "/api/cases?category=test")
	if err != nil {
		t.Fatalf("filter by category: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 filtering by category, got %d: %s", resp.StatusCode, string(body))
	}
	var filtered []cases.Case
	if err := json.Unmarshal(body, &filtered); err != nil {
		t.Fatalf("decode filtered: %v", err)
	}
	found := false
	for _, c := range filtered {
		if c.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected created case in filtered results")
	}

	// 5. Update case
	newName := "Updated Integration"
	updatePayload, _ := json.Marshal(cases.UpdateCaseRequest{Name: &newName})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/cases/"+created.ID, bytes.NewReader(updatePayload))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("update case: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 updating case, got %d: %s", resp.StatusCode, string(body))
	}

	// 6. Delete case
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/api/cases/"+created.ID, nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("delete case: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 deleting case, got %d", resp.StatusCode)
	}

	// 7. Confirm gone
	resp, err = client.Get(ts.URL + "/api/cases/" + created.ID)
	if err != nil {
		t.Fatalf("get deleted case: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", resp.StatusCode)
	}
}
