package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRedactDSNHidesPassword(t *testing.T) {
	cases := []string{
		"postgres://cami:cami@localhost:5432/cami?sslmode=disable",
		"clickhouse://cami:cami@clickhouse:9000/cami",
		"postgres://fleet:S3cr3tPassw0rd@db.internal:5432/fleet",
	}
	for _, dsn := range cases {
		got := redactDSN(dsn)
		pass := dsn[strings.Index(dsn, "://")+3:]
		pass = pass[strings.Index(pass, ":")+1 : strings.Index(pass, "@")]
		// Match ":password@" — the demo password "cami" is also the user and DB name.
		if strings.Contains(got, ":"+pass+"@") {
			t.Errorf("redactDSN(%q) = %q still contains the password", dsn, got)
		}
		if !strings.Contains(got, "@") {
			t.Errorf("redactDSN(%q) = %q lost the host part", dsn, got)
		}
	}
}

func TestRedactDSNRefusesUnparseable(t *testing.T) {
	got := redactDSN("host=db user=fleet password=S3cr3t dbname=fleet")
	if strings.Contains(got, "S3cr3t") {
		t.Fatalf("unparseable DSN leaked: %q", got)
	}
}

func TestHealthCheckProbesHealthz(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer healthy.Close()
	t.Setenv("REST_PORT", healthy.URL[strings.LastIndex(healthy.URL, ":")+1:])
	if code := healthCheck(); code != 0 {
		t.Fatalf("healthy server: exit code %d, want 0", code)
	}

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer broken.Close()
	t.Setenv("REST_PORT", broken.URL[strings.LastIndex(broken.URL, ":")+1:])
	if code := healthCheck(); code != 1 {
		t.Fatalf("failing server: exit code %d, want 1", code)
	}

	t.Setenv("REST_PORT", "1") // nothing listens on port 1
	if code := healthCheck(); code != 1 {
		t.Fatalf("no server: exit code %d, want 1", code)
	}
}
