// Package metrics holds the control plane's Prometheus collectors. It lives
// apart from the REST package so the gRPC server can update the same series
// without an import cycle.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// DevicesGauge is set from the database on a timer, never incremented by
	// hand, so it always matches the devices table.
	DevicesGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cami_devices_total",
		Help: "Number of registered devices by status",
	}, []string{"status"})

	GRPCStreamsGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cami_grpc_streams_active",
		Help: "Number of open device gRPC streams by kind",
	}, []string{"kind"})

	// DeploymentsGauge is set from the database on a timer.
	DeploymentsGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cami_deployments_total",
		Help: "Number of deployments by state",
	}, []string{"state"})

	RESTDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cami_rest_request_duration_seconds",
		Help:    "REST API request duration in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path", "status"})

	TelemetryInserts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cami_telemetry_inserts_total",
		Help: "Telemetry rows written to ClickHouse, by result",
	}, []string{"result"})

	IdentityRejections = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cami_identity_rejections_total",
		Help: "Device RPCs rejected because the claimed device did not match the TLS certificate",
	}, []string{"rpc"})
)

// SetCounts replaces every label value of g with the given counts, so labels
// that disappear from the database drop to zero instead of going stale.
func SetCounts(g *prometheus.GaugeVec, counts map[string]int) {
	g.Reset()
	for label, n := range counts {
		g.WithLabelValues(label).Set(float64(n))
	}
}
