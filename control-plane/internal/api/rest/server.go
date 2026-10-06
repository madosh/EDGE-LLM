package rest

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog/log"

	grpcapi "github.com/cami-fleet/control-plane/internal/api/grpc"
	natsclient "github.com/cami-fleet/control-plane/internal/events/nats"
	chstore "github.com/cami-fleet/control-plane/internal/store/clickhouse"
	pgstore "github.com/cami-fleet/control-plane/internal/store/postgres"
)

type artifactEntry struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

func NewRouter(
	pg *pgstore.Store,
	ch *chstore.Store,
	grpcSrv *grpcapi.Server,
	nats *natsclient.Client,
	artifactsDir string,
	artifactsURL string,
	apiKey string,
) http.Handler {
	h := &Handlers{
		pg:           pg,
		ch:           ch,
		grpcSrv:      grpcSrv,
		nats:         nats,
		artifactsDir: artifactsDir,
		artifactsURL: artifactsURL,
	}

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(zerologMiddleware)
	r.Use(metricsMiddleware)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	// Prometheus metrics endpoint (no auth)
	r.Handle("/metrics", MetricsHandler())

	r.Group(func(r chi.Router) {
		r.Use(apiKeyMiddleware(apiKey))
		r.Get("/api/health", h.Health)
		r.Get("/api/artifacts", h.ListArtifacts)
		r.Get("/api/devices", h.ListDevices)
		r.Get("/api/devices/{id}", h.GetDevice)
		r.Get("/api/devices/{id}/telemetry", h.GetDeviceTelemetry)
		r.Post("/api/deployments", h.CreateDeployment)
		r.Get("/api/deployments", h.ListDeployments)
		r.Get("/api/deployments/{id}", h.GetDeployment)
		r.Post("/api/deployments/{id}/promote", h.PromoteDeployment)
		r.Post("/api/deployments/{id}/rollback", h.RollbackDeployment)

		// Questions for the AI agent on a device
		r.Post("/api/devices/{id}/tasks", h.CreateTask)
		r.Get("/api/devices/{id}/tasks", h.ListTasks)
		r.Get("/api/tasks/{id}", h.GetTask)

		// Fleet-wide telemetry
		r.Get("/api/telemetry/fleet", h.GetFleetSummary)
		r.Get("/api/telemetry/timeseries", h.GetFleetTimeSeries)
		r.Get("/api/telemetry/devices", h.GetFleetDevices)
		r.Get("/api/telemetry/alerts", h.GetTelemetryAlerts)
	})

	// SSE endpoint — no API key required for browser EventSource (uses same CORS)
	r.Get("/api/events/stream", h.SSEStream)

	// Artifacts served without API key so agents can download freely
	r.Handle("/artifacts/*", http.StripPrefix("/artifacts/", http.FileServer(http.Dir(artifactsDir))))

	return r
}

func apiKeyMiddleware(key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := r.Header.Get("X-Api-Key")
			if got == "" {
				got = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			}
			if got != key {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Api-Key, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func zerologMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Msg("http")
		next.ServeHTTP(w, r)
	})
}

// discoverArtifacts lists the artifacts in dir that can be deployed: every
// file that has a "<file>.sha256" sidecar holding its digest (a .litertlm or
// .gguf model, for example), plus .tar.gz bundles for backward compatibility.
func discoverArtifacts(dir, baseURL string) []artifactEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var arts []artifactEntry
	for _, e := range entries {
		file := e.Name()
		if e.IsDir() || strings.HasPrefix(file, ".") || strings.HasSuffix(file, ".sha256") {
			continue
		}
		sha := ""
		if raw, err := os.ReadFile(filepath.Join(dir, file+".sha256")); err == nil {
			// Accept both a bare digest and `sha256sum` output ("<digest>  <file>").
			if fields := strings.Fields(string(raw)); len(fields) > 0 {
				sha = fields[0]
			}
		}
		if sha == "" && !strings.HasSuffix(file, ".tar.gz") {
			continue
		}
		name := strings.TrimSuffix(file, ".tar.gz")
		if name == file {
			name = strings.TrimSuffix(file, filepath.Ext(file))
		}
		arts = append(arts, artifactEntry{
			Name:   name,
			URL:    baseURL + "/artifacts/" + file,
			SHA256: sha,
		})
	}
	return arts
}
