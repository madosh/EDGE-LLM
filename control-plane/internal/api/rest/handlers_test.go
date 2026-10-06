package rest_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	grpcapi "github.com/cami-fleet/control-plane/internal/api/grpc"
	restapi "github.com/cami-fleet/control-plane/internal/api/rest"
)

func setupTestRouter(t *testing.T, artifactsDir string) http.Handler {
	t.Helper()
	grpcSrv := grpcapi.NewServer(nil, nil, nil)
	// Pass nil stores — endpoints that hit the DB will fail, but we can
	// test routing, middleware, health, and artifact discovery.
	return restapi.NewRouter(nil, nil, grpcSrv, nil, artifactsDir, "http://test:8080", "test-key")
}

func TestHealthEndpoint(t *testing.T) {
	dir := t.TempDir()
	router := setupTestRouter(t, dir)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("X-Api-Key", "test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %s", body["status"])
	}
}

func TestAPIKeyMiddlewareRejectsNoKey(t *testing.T) {
	dir := t.TempDir()
	router := setupTestRouter(t, dir)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without key, got %d", w.Code)
	}
}

func TestAPIKeyMiddlewareRejectsWrongKey(t *testing.T) {
	dir := t.TempDir()
	router := setupTestRouter(t, dir)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("X-Api-Key", "wrong-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong key, got %d", w.Code)
	}
}

func TestAPIKeyMiddlewareBearerAuth(t *testing.T) {
	dir := t.TempDir()
	router := setupTestRouter(t, dir)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with Bearer auth, got %d", w.Code)
	}
}

func TestCORSHeaders(t *testing.T) {
	dir := t.TempDir()
	router := setupTestRouter(t, dir)

	req := httptest.NewRequest(http.MethodOptions, "/api/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("expected CORS Allow-Origin header")
	}
	if w.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("expected CORS Allow-Methods header")
	}
}

func TestCreateDeploymentValidation(t *testing.T) {
	dir := t.TempDir()
	router := setupTestRouter(t, dir)

	tests := []struct {
		name   string
		body   map[string]any
		expect int
	}{
		{
			name:   "empty body",
			body:   map[string]any{},
			expect: http.StatusBadRequest,
		},
		{
			name: "missing model_id",
			body: map[string]any{
				"artifact_url":    "http://example.com/model.tar.gz",
				"artifact_sha256": "abc123",
			},
			expect: http.StatusBadRequest,
		},
		{
			name: "missing artifact_url",
			body: map[string]any{
				"model_id":        "gemma-4-e2b",
				"artifact_sha256": "abc123",
			},
			expect: http.StatusBadRequest,
		},
		{
			name: "missing artifact_sha256",
			body: map[string]any{
				"model_id":     "gemma-4-e2b",
				"artifact_url": "http://example.com/model.tar.gz",
			},
			expect: http.StatusBadRequest,
		},
		{
			name: "sha256 not 64 hex characters",
			body: map[string]any{
				"model_id":        "gemma-4-e2b",
				"artifact_url":    "http://example.com/model.litertlm",
				"artifact_sha256": "abc123",
				"tag_selector":    map[string]string{"location": "lab"},
			},
			expect: http.StatusBadRequest,
		},
		{
			name: "empty selector without all_devices",
			body: map[string]any{
				"model_id":        "gemma-4-e2b",
				"artifact_url":    "http://example.com/model.litertlm",
				"artifact_sha256": "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			expect: http.StatusBadRequest,
		},
		{
			name: "rollout above 100",
			body: map[string]any{
				"model_id":        "gemma-4-e2b",
				"artifact_url":    "http://example.com/model.litertlm",
				"artifact_sha256": "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
				"tag_selector":    map[string]string{"location": "lab"},
				"rollout_percent": 150,
			},
			expect: http.StatusBadRequest,
		},
		{
			name: "unknown accelerator requirement",
			body: map[string]any{
				"model_id":        "gemma-4-e2b",
				"artifact_url":    "http://example.com/model.litertlm",
				"artifact_sha256": "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
				"tag_selector":    map[string]string{"location": "lab"},
				"requirements":    map[string]any{"accelerator": "tpu"},
			},
			expect: http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(tc.body)
			req := httptest.NewRequest(http.MethodPost, "/api/deployments", bytes.NewReader(b))
			req.Header.Set("X-Api-Key", "test-key")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != tc.expect {
				body, _ := io.ReadAll(w.Body)
				t.Errorf("expected %d, got %d: %s", tc.expect, w.Code, string(body))
			}
		})
	}
}

func TestListArtifacts(t *testing.T) {
	dir := t.TempDir()
	// Create a test artifact and sha256 sidecar
	os.WriteFile(filepath.Join(dir, "test-model.tar.gz"), []byte("fake"), 0644)
	os.WriteFile(filepath.Join(dir, "test-model.tar.gz.sha256"), []byte("abcdef123456"), 0644)

	router := setupTestRouter(t, dir)

	req := httptest.NewRequest(http.MethodGet, "/api/artifacts", nil)
	req.Header.Set("X-Api-Key", "test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var arts []map[string]string
	if err := json.NewDecoder(w.Body).Decode(&arts); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(arts))
	}
	if arts[0]["name"] != "test-model" {
		t.Errorf("expected name test-model, got %s", arts[0]["name"])
	}
	if arts[0]["sha256"] != "abcdef123456" {
		t.Errorf("expected sha256 abcdef123456, got %s", arts[0]["sha256"])
	}
}

func TestListArtifactsIncludesModelFilesWithDigest(t *testing.T) {
	dir := t.TempDir()
	digest := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	os.WriteFile(filepath.Join(dir, "gemma-4-e2b.litertlm"), []byte("weights"), 0644)
	// sha256sum output format: "<digest>  <file>"
	os.WriteFile(filepath.Join(dir, "gemma-4-e2b.litertlm.sha256"), []byte(digest+"  gemma-4-e2b.litertlm\n"), 0644)
	// A file without a digest sidecar is not deployable and is not listed.
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me"), 0644)

	router := setupTestRouter(t, dir)
	req := httptest.NewRequest(http.MethodGet, "/api/artifacts", nil)
	req.Header.Set("X-Api-Key", "test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var arts []map[string]string
	if err := json.NewDecoder(w.Body).Decode(&arts); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("expected 1 artifact, got %d: %v", len(arts), arts)
	}
	if arts[0]["name"] != "gemma-4-e2b" || arts[0]["sha256"] != digest {
		t.Errorf("unexpected artifact: %v", arts[0])
	}
	if arts[0]["url"] != "http://test:8080/artifacts/gemma-4-e2b.litertlm" {
		t.Errorf("unexpected url: %s", arts[0]["url"])
	}
}

func TestListArtifactsEmpty(t *testing.T) {
	dir := t.TempDir()
	router := setupTestRouter(t, dir)

	req := httptest.NewRequest(http.MethodGet, "/api/artifacts", nil)
	req.Header.Set("X-Api-Key", "test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var arts []map[string]string
	json.NewDecoder(w.Body).Decode(&arts)
	if len(arts) != 0 {
		t.Errorf("expected empty artifacts list, got %d", len(arts))
	}
}

func TestArtifactFileServing(t *testing.T) {
	dir := t.TempDir()
	content := []byte("model-binary-content")
	os.WriteFile(filepath.Join(dir, "test.tar.gz"), content, 0644)

	router := setupTestRouter(t, dir)

	// Artifact endpoint should work without API key
	req := httptest.NewRequest(http.MethodGet, "/artifacts/test.tar.gz", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != string(content) {
		t.Error("unexpected artifact content")
	}
}

// Compile-time check that the packages import correctly.
var (
	_ = restapi.NewRouter
	_ = grpcapi.NewServer
)

func postJSON(t *testing.T, router http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("X-Api-Key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestPromoteValidation(t *testing.T) {
	router := setupTestRouter(t, t.TempDir())
	for _, pct := range []int{0, -5, 101} {
		w := postJSON(t, router, "/api/deployments/some-id/promote", map[string]any{"rollout_percent": pct})
		if w.Code != http.StatusBadRequest {
			t.Errorf("rollout_percent=%d: expected 400, got %d", pct, w.Code)
		}
	}
}

func TestCreateTaskValidation(t *testing.T) {
	router := setupTestRouter(t, t.TempDir())
	cases := []map[string]any{
		{},
		{"prompt": "   "},
		{"prompt": strings.Repeat("x", 4001)},
		{"prompt": "what model are you running?", "max_steps": 99},
	}
	for _, body := range cases {
		w := postJSON(t, router, "/api/devices/some-device/tasks", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %v: expected 400, got %d", body, w.Code)
		}
	}
}

func TestRequestBodyIsLimited(t *testing.T) {
	router := setupTestRouter(t, t.TempDir())
	huge := map[string]any{"prompt": strings.Repeat("x", 2<<20)}
	w := postJSON(t, router, "/api/devices/some-device/tasks", huge)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a 2 MiB body should be rejected with 400, got %d", w.Code)
	}
}
