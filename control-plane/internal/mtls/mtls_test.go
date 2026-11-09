package mtls

import (
	"crypto/x509"
	"math/big"
	"os"
	"path/filepath"
	"testing"
)

func certWithSerial(hex string) *x509.Certificate {
	n, ok := new(big.Int).SetString(hex, 16)
	if !ok {
		panic("bad serial " + hex)
	}
	return &x509.Certificate{SerialNumber: n}
}

func writeRevoked(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, RevokedFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRevocationsMissingFileRevokesNothing(t *testing.T) {
	rev := NewRevocations(t.TempDir())
	if rev.Revoked(certWithSerial("abc")) {
		t.Fatal("certificate revoked with no revocation list")
	}
	if rev.Revoked(nil) {
		t.Fatal("nil certificate counted as revoked")
	}
}

func TestRevocationsAcceptsOpensslFormats(t *testing.T) {
	dir := t.TempDir()
	writeRevoked(t, dir, "# revoked devices\nserial=0A1B2C\n\n  00:DE:AD:BE:EF  \n"+
		"C0FFEE  # device-barcelona-2 lost on site\n")
	rev := NewRevocations(dir)

	for _, serial := range []string{"a1b2c", "deadbeef", "c0ffee"} {
		if !rev.Revoked(certWithSerial(serial)) {
			t.Errorf("serial %s should be revoked", serial)
		}
	}
	if rev.Revoked(certWithSerial("1234")) {
		t.Error("serial 1234 is not on the list")
	}
}

func TestRevocationsReloadWhenFileChanges(t *testing.T) {
	dir := t.TempDir()
	rev := NewRevocations(dir)
	cert := certWithSerial("1f")

	writeRevoked(t, dir, "# nothing yet\n")
	if rev.Revoked(cert) {
		t.Fatal("revoked before being listed")
	}

	writeRevoked(t, dir, "# revoked during the demo\n1F\n")
	if !rev.Revoked(cert) {
		t.Fatal("new entry not picked up without a restart")
	}

	if err := os.Remove(filepath.Join(dir, RevokedFile)); err != nil {
		t.Fatal(err)
	}
	if rev.Revoked(cert) {
		t.Fatal("still revoked after the list was deleted")
	}
}

func TestRevocationsFailClosedOnUnreadableList(t *testing.T) {
	dir := t.TempDir()
	// A directory where the file should be: Stat succeeds, reading fails.
	if err := os.Mkdir(filepath.Join(dir, RevokedFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if !NewRevocations(dir).Revoked(certWithSerial("1")) {
		t.Fatal("an unreadable revocation list must refuse every certificate")
	}
}
