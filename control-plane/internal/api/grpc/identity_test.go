package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"net"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	pb "github.com/cami-fleet/control-plane/gen"
)

// asDevice returns a context that looks like an RPC from a client whose
// verified certificate has the given common name.
func asDevice(name string) context.Context {
	leaf := &x509.Certificate{Subject: pkix.Name{CommonName: name}}
	return peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 50000},
		AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{
			VerifiedChains: [][]*x509.Certificate{{leaf}},
		}},
	})
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Fatalf("expected %s, got %s (err=%v)", want, got, err)
	}
}

func TestPeerNameRequiresPeer(t *testing.T) {
	_, err := peerName(context.Background())
	wantCode(t, err, codes.Unauthenticated)
}

func TestPeerNameRequiresTLS(t *testing.T) {
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{}})
	_, err := peerName(ctx)
	wantCode(t, err, codes.Unauthenticated)
}

func TestPeerNameRequiresVerifiedChain(t *testing.T) {
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr:     &net.TCPAddr{},
		AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{}},
	})
	_, err := peerName(ctx)
	wantCode(t, err, codes.Unauthenticated)
}

func TestPeerNameReadsCommonName(t *testing.T) {
	name, err := peerName(asDevice("device-barcelona-1"))
	if err != nil {
		t.Fatal(err)
	}
	if name != "device-barcelona-1" {
		t.Fatalf("expected device-barcelona-1, got %q", name)
	}
}

func TestRegisterRejectsNameNotInCertificate(t *testing.T) {
	srv := NewServer(nil, nil, nil)
	_, err := srv.Register(asDevice("device-barcelona-2"), &pb.RegisterRequest{DeviceName: "device-barcelona-1"})
	wantCode(t, err, codes.PermissionDenied)
}

func TestHeartbeatRejectsAnotherDevicesID(t *testing.T) {
	srv := NewServer(nil, nil, nil)
	srv.deviceNames.Store("id-of-device-1", "device-barcelona-1")

	// device-2's certificate claiming device-1's ID: the shared-certificate attack.
	_, err := srv.SendHeartbeat(asDevice("device-barcelona-2"), &pb.Heartbeat{DeviceId: "id-of-device-1"})
	wantCode(t, err, codes.PermissionDenied)
}

func TestAckRejectsStatusNotReportableByDevice(t *testing.T) {
	srv := NewServer(nil, nil, nil)
	srv.deviceNames.Store("id-of-device-1", "device-barcelona-1")

	_, err := srv.AckDeployment(asDevice("device-barcelona-1"), &pb.DeploymentAck{
		DeploymentId: "dep-1",
		DeviceId:     "id-of-device-1",
		Status:       "pending",
	})
	wantCode(t, err, codes.InvalidArgument)
}
