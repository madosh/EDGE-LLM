package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	grpcapi "github.com/cami-fleet/control-plane/internal/api/grpc"
	restapi "github.com/cami-fleet/control-plane/internal/api/rest"
	natsclient "github.com/cami-fleet/control-plane/internal/events/nats"
	"github.com/cami-fleet/control-plane/internal/metrics"
	"github.com/cami-fleet/control-plane/internal/notify"
	chstore "github.com/cami-fleet/control-plane/internal/store/clickhouse"
	pgstore "github.com/cami-fleet/control-plane/internal/store/postgres"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--health-check" {
		os.Exit(0)
	}

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	cfg := loadConfig()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// ── Postgres ───────────────────────────────────────────────────────────────
	log.Info().Str("dsn", redactDSN(cfg.PostgresDSN)).Msg("connecting to postgres")
	pg, err := pgstore.New(ctx, cfg.PostgresDSN)
	if err != nil {
		log.Fatal().Err(err).Msg("postgres init failed")
	}

	// ── ClickHouse ─────────────────────────────────────────────────────────────
	log.Info().Str("dsn", redactDSN(cfg.ClickHouseDSN)).Msg("connecting to clickhouse")
	ch, err := chstore.New(ctx, cfg.ClickHouseDSN)
	if err != nil {
		log.Fatal().Err(err).Msg("clickhouse init failed")
	}

	// ── NATS ──────────────────────────────────────────────────────────────────
	log.Info().Str("url", cfg.NATSURL).Msg("connecting to nats")
	nc, err := natsclient.New(cfg.NATSURL)
	if err != nil {
		log.Fatal().Err(err).Msg("nats init failed")
	}
	defer nc.Close()

	// ── Webhook notifier (optional) ──────────────────────────────────────────
	webhookNotifier := notify.NewWebhookNotifier(cfg.WebhookURL)
	if webhookNotifier != nil {
		// The URL itself is a credential for Slack/Discord webhooks; log only the host.
		log.Info().Str("host", webhookNotifier.Host()).Msg("webhook notifier enabled")
	}

	// ── gRPC server ────────────────────────────────────────────────────────────
	grpcSrv := grpcapi.NewServer(pg, ch, nc)
	grpcSrv.SetNotifier(webhookNotifier)

	grpcAddr := ":" + cfg.GRPCPort
	grpcServer, err := grpcapi.Listen(grpcAddr, cfg.CertDir, grpcSrv)
	if err != nil {
		log.Fatal().Err(err).Msg("grpc listen failed")
	}
	go func() {
		if err := grpcServer.Serve(); err != nil {
			log.Fatal().Err(err).Msg("grpc server failed")
		}
	}()

	// ── REST server ────────────────────────────────────────────────────────────
	router := restapi.NewRouter(pg, ch, grpcSrv, nc, cfg.ArtifactsDir, cfg.ArtifactsBaseURL, cfg.APIKey)

	httpSrv := &http.Server{
		Addr:         ":" + cfg.RESTPort,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	go func() {
		log.Info().Str("addr", httpSrv.Addr).Msg("REST server listening")
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("rest server failed")
		}
	}()

	// ── Offline detector ──────────────────────────────────────────────────────
	go runOfflineDetector(ctx, pg, nc, webhookNotifier)

	log.Info().Msg("cami-fleet control plane ready")
	<-ctx.Done()
	log.Info().Msg("shutting down")

	grpcServer.GracefulStop()
	log.Info().Msg("gRPC server stopped")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	if err := httpSrv.Shutdown(shutCtx); err != nil {
		log.Warn().Err(err).Msg("HTTP server shutdown error")
	}
	log.Info().Msg("HTTP server stopped")
}

// runOfflineDetector marks devices offline when last heartbeat > 30 s ago,
// and refreshes the device and deployment gauges from the database.
func runOfflineDetector(ctx context.Context, pg *pgstore.Store, nc *natsclient.Client, webhook *notify.WebhookNotifier) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ids, err := pg.MarkStaleDevicesOffline(ctx, 30*time.Second)
			if err != nil {
				log.Warn().Err(err).Msg("offline detector failed")
				continue
			}
			for _, id := range ids {
				log.Info().Str("device_id", id).Msg("device went offline")
				nc.Publish(natsclient.SubjectDeviceOffline(id), map[string]string{"device_id": id})
				if webhook != nil {
					name, err := pg.DeviceName(ctx, id)
					if err != nil {
						name = id
					}
					go webhook.NotifyDeviceOffline(id, name)
				}
			}
			refreshGauges(ctx, pg)
		}
	}
}

func refreshGauges(ctx context.Context, pg *pgstore.Store) {
	if counts, err := pg.CountDevicesByStatus(ctx); err == nil {
		metrics.SetCounts(metrics.DevicesGauge, counts)
	} else {
		log.Warn().Err(err).Msg("count devices failed")
	}
	if counts, err := pg.CountDeploymentsByStatus(ctx); err == nil {
		metrics.SetCounts(metrics.DeploymentsGauge, counts)
	} else {
		log.Warn().Err(err).Msg("count deployments failed")
	}
}

type config struct {
	PostgresDSN      string
	ClickHouseDSN    string
	NATSURL          string
	GRPCPort         string
	RESTPort         string
	APIKey           string
	CertDir          string
	ArtifactsDir     string
	ArtifactsBaseURL string
	WebhookURL       string
}

func loadConfig() config {
	restPort := getEnv("REST_PORT", "8080")
	return config{
		PostgresDSN:      getEnv("POSTGRES_DSN", "postgres://cami:cami@localhost:5432/cami?sslmode=disable"),
		ClickHouseDSN:    getEnv("CLICKHOUSE_DSN", "clickhouse://cami:cami@localhost:9000/cami"),
		NATSURL:          getEnv("NATS_URL", "nats://localhost:4222"),
		GRPCPort:         getEnv("GRPC_PORT", "9090"),
		RESTPort:         restPort,
		APIKey:           getEnv("API_KEY", "changeme"),
		CertDir:          getEnv("CERT_DIR", "/certs"),
		ArtifactsDir:     getEnv("ARTIFACTS_DIR", "/artifacts"),
		ArtifactsBaseURL: getEnv("ARTIFACTS_BASE_URL", fmt.Sprintf("http://localhost:%s", restPort)),
		WebhookURL:       getEnv("WEBHOOK_URL", ""),
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// redactDSN hides the password in a connection URL. A DSN that does not parse
// as a URL is not logged at all, since it may still contain a secret.
func redactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return "(unparseable DSN, not logged)"
	}
	return u.Redacted()
}
