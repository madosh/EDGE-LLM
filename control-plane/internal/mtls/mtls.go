// Package mtls builds the server-side TLS configuration every device-facing
// listener uses: the control plane's certificate, and client certificates
// required and verified against the fleet CA.
package mtls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// ServerConfig loads server.crt, server.key and ca.crt from certDir.
func ServerConfig(certDir string, minVersion uint16) (*tls.Config, error) {
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
	return &tls.Config{
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   minVersion,
	}, nil
}

// PeerCommonName returns the common name of the verified client certificate
// in a TLS connection state, or "" when there is none.
func PeerCommonName(state *tls.ConnectionState) string {
	if state == nil || len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
		return ""
	}
	return state.VerifiedChains[0][0].Subject.CommonName
}
