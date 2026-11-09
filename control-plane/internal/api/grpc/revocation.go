package grpc

import (
	"context"
	"crypto/x509"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/cami-fleet/control-plane/internal/mtls"
)

// revocationPoll is how often an open stream re-checks its certificate. The
// handshake check alone is not enough: an agent holds its streams open for
// hours, so a revoked device would otherwise keep receiving deployments.
const revocationPoll = 5 * time.Second

var errRevoked = status.Error(codes.PermissionDenied, "client certificate is revoked")

func peerCertificate(ctx context.Context) *x509.Certificate {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return nil
	}
	return mtls.PeerCertificate(&tlsInfo.State)
}

func revocationUnary(rev *mtls.Revocations) ggrpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *ggrpc.UnaryServerInfo, handler ggrpc.UnaryHandler) (any, error) {
		if rev.Revoked(peerCertificate(ctx)) {
			return nil, errRevoked
		}
		return handler(ctx, req)
	}
}

// revocationStream refuses a stream from a revoked certificate and ends an
// open one within revocationPoll of its certificate being revoked.
func revocationStream(rev *mtls.Revocations) ggrpc.StreamServerInterceptor {
	return func(srv any, ss ggrpc.ServerStream, info *ggrpc.StreamServerInfo, handler ggrpc.StreamHandler) error {
		cert := peerCertificate(ss.Context())
		if rev.Revoked(cert) {
			return errRevoked
		}
		ctx, cancel := context.WithCancel(ss.Context())
		defer cancel()
		stream := &revocableStream{ServerStream: ss, ctx: ctx}
		go func() {
			t := time.NewTicker(revocationPoll)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if rev.Revoked(cert) {
						log.Warn().Str("device", cert.Subject.CommonName).Str("rpc", info.FullMethod).
							Msg("certificate revoked: closing stream")
						stream.revoked.Store(true)
						cancel()
						return
					}
				}
			}
		}()
		err := handler(srv, stream)
		if stream.revoked.Load() {
			return errRevoked
		}
		return err
	}
}

// revocableStream swaps in a context that is cancelled on revocation. Handlers
// that wait on the context return at once; RecvMsg and SendMsg fail on their
// next call, which covers handlers blocked reading a client stream.
type revocableStream struct {
	ggrpc.ServerStream
	ctx     context.Context
	revoked atomic.Bool
}

func (s *revocableStream) Context() context.Context { return s.ctx }

func (s *revocableStream) RecvMsg(m any) error {
	if err := s.ServerStream.RecvMsg(m); err != nil {
		return err
	}
	if s.revoked.Load() {
		return errRevoked
	}
	return nil
}

func (s *revocableStream) SendMsg(m any) error {
	if s.revoked.Load() {
		return errRevoked
	}
	return s.ServerStream.SendMsg(m)
}
