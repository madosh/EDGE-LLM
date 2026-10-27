package devicehttp_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/cami-fleet/control-plane/gen"
	"github.com/cami-fleet/control-plane/internal/api/devicehttp"
	grpcapi "github.com/cami-fleet/control-plane/internal/api/grpc"
	"github.com/cami-fleet/control-plane/internal/model"
)

// fakeDevices records the caller each call was made as. It owns one device,
// "dev-1", whose certificate name is "esp32-display-1".
type fakeDevices struct {
	callers    []string
	reconciled bool
}

func (f *fakeDevices) RegisterAs(_ context.Context, caller string, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	f.callers = append(f.callers, caller)
	if req.DeviceName != caller {
		return nil, status.Error(codes.PermissionDenied, "device_name must match the client certificate's common name")
	}
	return &pb.RegisterResponse{DeviceId: "dev-1"}, nil
}

func (f *fakeDevices) HeartbeatAs(ctx context.Context, caller string, req *pb.Heartbeat) (*pb.HeartbeatAck, error) {
	if err := f.Authorize(ctx, "SendHeartbeat", caller, req.DeviceId); err != nil {
		return nil, err
	}
	return &pb.HeartbeatAck{ServerTsUnix: 42}, nil
}

func (f *fakeDevices) AckAs(ctx context.Context, caller string, req *pb.DeploymentAck) (*pb.Empty, error) {
	if err := f.Authorize(ctx, "AckDeployment", caller, req.DeviceId); err != nil {
		return nil, err
	}
	if !model.IsAckStatus(req.Status) {
		return nil, status.Errorf(codes.InvalidArgument, "invalid deployment status %q", req.Status)
	}
	return &pb.Empty{}, nil
}

func (f *fakeDevices) Authorize(_ context.Context, _, caller, deviceID string) error {
	f.callers = append(f.callers, caller)
	if deviceID != "dev-1" || caller != "esp32-display-1" {
		return status.Error(codes.PermissionDenied, "device_id does not match client certificate")
	}
	return nil
}

func (f *fakeDevices) Reconcile(context.Context, string) { f.reconciled = true }

type fakeStore struct{ devices *fakeDevices }

func (s fakeStore) GetDevice(_ context.Context, id string) (*model.Device, error) {
	return &model.Device{ID: id, Name: "esp32-display-1", Status: model.StatusOnline}, nil
}

func (s fakeStore) DeviceAssignments(context.Context, string, int) ([]model.Assignment, error) {
	if !s.devices.reconciled {
		// State must reconcile before reading, or a new deployment is missed.
		return nil, nil
	}
	reason := "needs 2048 MB RAM, device has 1 MB"
	return []model.Assignment{{DeploymentID: "dep-1", ModelID: "gemma", Status: model.DDSkipped, ErrorMsg: &reason}}, nil
}

func (s fakeStore) CountDevicesByStatus(context.Context) (map[string]int, error) {
	return map[string]int{"online": 3, "offline": 1}, nil
}

func (s fakeStore) CountDeploymentsByStatus(context.Context) (map[string]int, error) {
	return map[string]int{"completed": 2}, nil
}

// withCert makes a request look like it arrived on a connection whose
// verified client certificate has the given common name.
func withCert(r *http.Request, cn string) *http.Request {
	leaf := &x509.Certificate{Subject: pkix.Name{CommonName: cn}}
	r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf}}}
	return r
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestRequiresClientCertificate(t *testing.T) {
	h := devicehttp.NewHandler(&fakeDevices{}, nil)
	w := serve(h, httptest.NewRequest(http.MethodGet, "/device/v1/state?device_id=dev-1", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without a client certificate, got %d", w.Code)
	}
}

func TestRegisterUnderAnotherNameIsForbidden(t *testing.T) {
	// The real gRPC server's checks, reached over HTTP. It needs no database
	// to reject a name that differs from the certificate.
	h := devicehttp.NewHandler(grpcapi.NewServer(nil, nil, nil), nil)
	body := `{"device_name":"device-barcelona-1","labels":{}}`
	r := withCert(httptest.NewRequest(http.MethodPost, "/device/v1/register", strings.NewReader(body)), "esp32-display-1")
	if w := serve(h, r); w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body)
	}
}

func TestHeartbeatForAnotherDeviceIsForbidden(t *testing.T) {
	devices := &fakeDevices{}
	h := devicehttp.NewHandler(devices, nil)
	r := withCert(httptest.NewRequest(http.MethodPost, "/device/v1/heartbeat",
		strings.NewReader(`{"device_id":"dev-1"}`)), "device-barcelona-2")
	if w := serve(h, r); w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
	if devices.callers[0] != "device-barcelona-2" {
		t.Fatalf("caller must come from the certificate, got %q", devices.callers[0])
	}
}

func TestAckRejectsStatusDevicesCannotReport(t *testing.T) {
	h := devicehttp.NewHandler(&fakeDevices{}, nil)
	body := `{"deployment_id":"dep-1","device_id":"dev-1","status":"skipped"}`
	r := withCert(httptest.NewRequest(http.MethodPost, "/device/v1/ack", strings.NewReader(body)), "esp32-display-1")
	if w := serve(h, r); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestStateReconcilesThenReportsAssignmentsAndCounts(t *testing.T) {
	devices := &fakeDevices{}
	h := devicehttp.NewHandler(devices, fakeStore{devices: devices})
	r := withCert(httptest.NewRequest(http.MethodGet, "/device/v1/state?device_id=dev-1", nil), "esp32-display-1")
	w := serve(h, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var got struct {
		Device      model.Device       `json:"device"`
		Assignments []model.Assignment `json:"assignments"`
		Fleet       struct {
			Devices map[string]int `json:"devices"`
		} `json:"fleet"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Assignments) != 1 || got.Assignments[0].Status != model.DDSkipped {
		t.Fatalf("expected the skipped assignment, got %+v", got.Assignments)
	}
	if got.Assignments[0].ErrorMsg == nil || !strings.Contains(*got.Assignments[0].ErrorMsg, "2048 MB") {
		t.Fatalf("expected the skip reason, got %v", got.Assignments[0].ErrorMsg)
	}
	if got.Fleet.Devices["online"] != 3 {
		t.Fatalf("expected fleet counts, got %v", got.Fleet.Devices)
	}
}

func TestStateForAnotherDeviceIsForbidden(t *testing.T) {
	devices := &fakeDevices{}
	h := devicehttp.NewHandler(devices, fakeStore{devices: devices})
	r := withCert(httptest.NewRequest(http.MethodGet, "/device/v1/state?device_id=dev-1", nil), "device-barcelona-2")
	if w := serve(h, r); w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
	if devices.reconciled {
		t.Fatal("must not reconcile for a caller that does not own the device")
	}
}

// ── Real TLS: the handshake an ESP32 makes ───────────────────────────────────

type testPKI struct {
	dir        string
	caPool     *x509.CertPool
	clientCert tls.Certificate
}

func newTestPKI(t *testing.T, clientCN string) testPKI {
	t.Helper()
	dir := t.TempDir()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)

	issue := func(cn string, usage x509.ExtKeyUsage, serial int64) ([]byte, []byte) {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: cn},
			DNSNames:     []string{"control-plane"},
			IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
			pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	}

	serverCrt, serverKey := issue("control-plane", x509.ExtKeyUsageServerAuth, 2)
	clientCrt, clientKey := issue(clientCN, x509.ExtKeyUsageClientAuth, 3)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	for name, data := range map[string][]byte{"ca.crt": caPEM, "server.crt": serverCrt, "server.key": serverKey} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	clientPair, err := tls.X509KeyPair(clientCrt, clientKey)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return testPKI{dir: dir, caPool: pool, clientCert: clientPair}
}

func startTLS(t *testing.T, pki testPKI, h http.Handler) string {
	t.Helper()
	srv, err := devicehttp.NewServer("127.0.0.1:0", pki.dir, h)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.ServeTLS(ln, "", "")
	t.Cleanup(func() { srv.Close() })
	return "https://" + ln.Addr().String()
}

func TestTLS12ClientCertificateNamesTheCaller(t *testing.T) {
	pki := newTestPKI(t, "esp32-display-1")
	devices := &fakeDevices{}
	url := startTLS(t, pki, devicehttp.NewHandler(devices, nil))

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:      pki.caPool,
		Certificates: []tls.Certificate{pki.clientCert},
		ServerName:   "control-plane",
		MaxVersion:   tls.VersionTLS12, // what mbedTLS on an ESP32 negotiates
	}}}
	resp, err := client.Post(url+"/device/v1/register", "application/json",
		strings.NewReader(`{"device_name":"esp32-display-1","labels":{"location":"desk"}}`))
	if err != nil {
		t.Fatalf("TLS 1.2 request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if resp.TLS.Version != tls.VersionTLS12 {
		t.Fatalf("expected TLS 1.2, got %x", resp.TLS.Version)
	}
	if len(devices.callers) != 1 || devices.callers[0] != "esp32-display-1" {
		t.Fatalf("caller must be the certificate's common name, got %v", devices.callers)
	}
}

func TestTLSWithoutClientCertificateFails(t *testing.T) {
	pki := newTestPKI(t, "esp32-display-1")
	url := startTLS(t, pki, devicehttp.NewHandler(&fakeDevices{}, nil))

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:    pki.caPool,
		ServerName: "control-plane",
	}}}
	resp, err := client.Get(url + "/device/v1/state?device_id=dev-1")
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected the handshake to fail without a client certificate")
	}
}
