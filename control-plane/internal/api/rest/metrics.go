package rest

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	DevicesGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cami_devices_total",
		Help: "Number of registered devices by status",
	}, []string{"status"})

	GRPCStreamsGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "cami_grpc_streams_active",
		Help: "Number of active gRPC telemetry streams",
	})

	DeploymentsGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cami_deployments_total",
		Help: "Number of deployments by state",
	}, []string{"state"})

	RESTDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cami_rest_request_duration_seconds",
		Help:    "REST API request duration in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path", "status"})

	TelemetryInserts = promauto.NewCounter(prometheus.CounterOpts{
		Name: "cami_telemetry_inserts_total",
		Help: "Total number of telemetry rows inserted into ClickHouse",
	})
)

func MetricsHandler() http.Handler {
	return promhttp.Handler()
}

func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(ww, r)
		duration := time.Since(start).Seconds()
		RESTDuration.WithLabelValues(r.Method, r.URL.Path, strconv.Itoa(ww.status)).Observe(duration)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
