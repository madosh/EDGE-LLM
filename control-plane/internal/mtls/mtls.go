// Package mtls builds the server-side TLS configuration every device-facing
// listener uses: the control plane's certificate, and client certificates
// required and verified against the fleet CA, minus any revoked ones.
package mtls

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"
)

// RevokedFile is the name, inside the cert directory, of the revocation list:
// one certificate serial per line in hex (the format `openssl x509 -serial`
// prints, with or without the "serial=" prefix). Everything after a # is a
// comment. A missing file means nothing is revoked.
const RevokedFile = "revoked.txt"

// ServerConfig loads server.crt, server.key and ca.crt from certDir. When rev
// is not nil, handshakes from a revoked client certificate are refused.
func ServerConfig(certDir string, minVersion uint16, rev *Revocations) (*tls.Config, error) {
	serverCert, err := tls.LoadX509KeyPair(certDir+"/server.crt", certDir+"/server.key")
	if err != nil {
		return nil, fmt.Errorf("load server cert: %w", err)
	}
	caCert, err := os.ReadFile(certDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("read ca cert: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("no certificates found in %s/ca.crt", certDir)
	}
	cfg := &tls.Config{
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   minVersion,
	}
	if rev != nil {
		cfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if cert := PeerCertificate(&cs); rev.Revoked(cert) {
				return fmt.Errorf("client certificate %q (serial %s) is revoked",
					cert.Subject.CommonName, cert.SerialNumber.Text(16))
			}
			return nil
		}
	}
	return cfg, nil
}

// PeerCertificate returns the verified client certificate in a TLS connection
// state, or nil when there is none.
func PeerCertificate(state *tls.ConnectionState) *x509.Certificate {
	if state == nil || len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
		return nil
	}
	return state.VerifiedChains[0][0]
}

// PeerCommonName returns the common name of the verified client certificate
// in a TLS connection state, or "" when there is none.
func PeerCommonName(state *tls.ConnectionState) string {
	if cert := PeerCertificate(state); cert != nil {
		return cert.Subject.CommonName
	}
	return ""
}

// Revocations is the set of revoked client-certificate serials in a cert
// directory's revoked.txt. The file is re-read whenever its size or
// modification time changes, so revoking a device needs no restart.
type Revocations struct {
	path string

	mu      sync.Mutex
	modTime time.Time
	size    int64
	serials map[string]struct{}
}

// NewRevocations watches certDir/revoked.txt.
func NewRevocations(certDir string) *Revocations {
	return &Revocations{path: certDir + "/" + RevokedFile}
}

// Revoked reports whether cert's serial is on the list. A nil certificate is
// not revoked. If the file exists but cannot be read, every certificate
// counts as revoked: failing closed is safer than letting a revoked device in.
func (r *Revocations) Revoked(cert *x509.Certificate) bool {
	if cert == nil {
		return false
	}
	serials, err := r.current()
	if err != nil {
		return true
	}
	_, revoked := serials[cert.SerialNumber.Text(16)]
	return revoked
}

func (r *Revocations) current() (map[string]struct{}, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	info, err := os.Stat(r.path)
	if errors.Is(err, fs.ErrNotExist) {
		r.serials, r.modTime, r.size = nil, time.Time{}, 0
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if r.serials != nil && info.ModTime().Equal(r.modTime) && info.Size() == r.size {
		return r.serials, nil
	}
	serials, err := readSerials(r.path)
	if err != nil {
		return nil, err
	}
	r.serials, r.modTime, r.size = serials, info.ModTime(), info.Size()
	return serials, nil
}

func readSerials(path string) (map[string]struct{}, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	serials := map[string]struct{}{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if s := normalizeSerial(sc.Text()); s != "" {
			serials[s] = struct{}{}
		}
	}
	return serials, sc.Err()
}

// normalizeSerial turns "serial=0A:1B..." or "0a1b..." into big.Int.Text(16)
// form: lower case, no separators, no leading zeros.
func normalizeSerial(line string) string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	line = strings.TrimPrefix(strings.ToLower(line), "serial=")
	line = strings.ReplaceAll(line, ":", "")
	line = strings.TrimLeft(line, "0")
	if line == "" {
		return "0"
	}
	return line
}
