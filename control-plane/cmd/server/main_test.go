package main

import (
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
