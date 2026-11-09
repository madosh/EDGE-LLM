// Command loadgen runs many simulated agents against a real control plane to
// measure how it holds up: RPC latency under load, and how long one
// deployment takes to reach every device.
//
// Each simulated agent is a full participant, not a shortcut: its own client
// certificate (minted in memory from the fleet CA, so the CN is its identity),
// its own mTLS gRPC connection, Register, periodic heartbeats and telemetry,
// an open WatchDeployments stream, and an ack when a deployment arrives. It
// does not download artifacts; it measures the control plane, not the network.
//
// It needs the fleet CA's private key, so it runs only next to the stack:
//
//	docker compose run ... (see scripts/load-test.sh)
package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	pb "github.com/cami-fleet/control-plane/gen"
)

type config struct {
	agents     int
	rampPerSec int
	addr       string
	serverName string
	caDir      string
	api        string
	apiKey     string
	heartbeat  time.Duration
	telemetry  time.Duration
	soak       time.Duration
	deploy     bool
	artifact   string
}

func main() {
	var c config
	flag.IntVar(&c.agents, "agents", 100, "simulated agents")
	flag.IntVar(&c.rampPerSec, "ramp", 50, "agents started per second")
	flag.StringVar(&c.addr, "addr", "control-plane:9090", "gRPC address")
	flag.StringVar(&c.serverName, "server-name", "control-plane", "name in the server certificate")
	flag.StringVar(&c.caDir, "ca-dir", "/ca", "directory holding ca.crt and ca.key")
	flag.StringVar(&c.api, "api", "http://control-plane:8080", "REST API, for the fan-out deployment")
	flag.StringVar(&c.apiKey, "api-key", os.Getenv("CAMI_API_KEY"), "REST API key")
	flag.DurationVar(&c.heartbeat, "heartbeat", 10*time.Second, "heartbeat interval, as the real agent")
	flag.DurationVar(&c.telemetry, "telemetry", 30*time.Second, "telemetry interval, as the real agent")
	flag.DurationVar(&c.soak, "soak", 60*time.Second, "steady-state time measured after the ramp")
	flag.BoolVar(&c.deploy, "deploy", true, "after the soak, deploy to every simulated agent and time the fan-out")
	flag.StringVar(&c.artifact, "artifact", "sample-gemma-4-e2b.tar.gz", "artifact served under /artifacts")
	flag.Parse()
	if c.apiKey == "" {
		c.apiKey = "changeme"
	}
	if err := run(c); err != nil {
		log.Fatal(err)
	}
}

// -- Measurements ------------------------------------------------------------

type latencies struct {
	mu   sync.Mutex
	vals []time.Duration
	errs atomic.Int64
}

func (l *latencies) add(d time.Duration, err error) {
	if err != nil {
		l.errs.Add(1)
		return
	}
	l.mu.Lock()
	l.vals = append(l.vals, d)
	l.mu.Unlock()
}

func (l *latencies) row(name string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	v := append([]time.Duration(nil), l.vals...)
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	if len(v) == 0 {
		return fmt.Sprintf("| %s | 0 | - | - | - | - | %d |", name, l.errs.Load())
	}
	p := func(q float64) string { return ms(v[int(float64(len(v)-1)*q)]) }
	return fmt.Sprintf("| %s | %d | %s | %s | %s | %s | %d |",
		name, len(v), p(0.5), p(0.95), p(0.99), ms(v[len(v)-1]), l.errs.Load())
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1f", float64(d.Microseconds())/1000) }

type stats struct {
	register, heartbeat, ack latencies
	connected, watching      atomic.Int64
	telemetrySent            atomic.Int64

	deployStart atomic.Int64 // unix nanos when the fan-out deployment was created
	received    latencies    // deployment created -> instruction on the agent
}

// -- Certificates ------------------------------------------------------------

type issuer struct {
	cert *x509.Certificate
	key  crypto.Signer
	pool *x509.CertPool
}

func loadIssuer(dir string) (*issuer, error) {
	certPEM, err := os.ReadFile(dir + "/ca.crt")
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(dir + "/ca.key")
	if err != nil {
		return nil, err
	}
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, errors.New("ca.crt or ca.key is not PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, err
	}
	var key crypto.Signer
	if k, err := x509.ParsePKCS8PrivateKey(kb.Bytes); err == nil {
		s, ok := k.(crypto.Signer)
		if !ok {
			return nil, errors.New("CA key cannot sign")
		}
		key = s
	} else if k, err := x509.ParsePKCS1PrivateKey(kb.Bytes); err == nil {
		key = k
	} else {
		return nil, fmt.Errorf("unsupported CA key: %w", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &issuer{cert: cert, key: key, pool: pool}, nil
}

// clientCert mints a short-lived client certificate whose CN is name.
func (is *issuer) clientCert(name string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, is.cert, &key.PublicKey, is.key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// -- One simulated agent -----------------------------------------------------

func agent(ctx context.Context, c config, is *issuer, st *stats, run string, i int) {
	name := fmt.Sprintf("loadgen-%s-%04d", run, i)
	cert, err := is.clientCert(name)
	if err != nil {
		st.register.add(0, err)
		return
	}
	creds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      is.pool,
		ServerName:   c.serverName,
		MinVersion:   tls.VersionTLS13,
	})
	conn, err := grpc.NewClient(c.addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		st.register.add(0, err)
		return
	}
	defer conn.Close()
	client := pb.NewAgentServiceClient(conn)

	start := time.Now()
	reg, err := client.Register(ctx, &pb.RegisterRequest{
		DeviceName: name, AgentVersion: "loadgen/0.1.0", Arch: "x86_64", Os: "linux",
		MemTotalMb: 4096, Accelerators: []string{"cpu"},
		Labels: map[string]string{"loadgen": run, "type": "simulated"},
	})
	st.register.add(time.Since(start), err)
	if err != nil {
		return
	}
	st.connected.Add(1)
	id := reg.DeviceId

	go watch(ctx, client, st, id)
	go telemetry(ctx, c, client, st, id)

	t := time.NewTicker(c.heartbeat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			hbStart := time.Now()
			_, err := client.SendHeartbeat(ctx, &pb.Heartbeat{DeviceId: id, TsUnix: time.Now().Unix()})
			if ctx.Err() == nil {
				st.heartbeat.add(time.Since(hbStart), err)
			}
		}
	}
}

func watch(ctx context.Context, client pb.AgentServiceClient, st *stats, id string) {
	stream, err := client.WatchDeployments(ctx, &pb.WatchRequest{DeviceId: id})
	if err != nil {
		return
	}
	st.watching.Add(1)
	for {
		instr, err := stream.Recv()
		if err != nil {
			return
		}
		if t0 := st.deployStart.Load(); t0 != 0 {
			st.received.add(time.Since(time.Unix(0, t0)), nil)
		}
		ackStart := time.Now()
		_, err = client.AckDeployment(ctx, &pb.DeploymentAck{
			DeploymentId: instr.DeploymentId, DeviceId: id, Status: "running",
		})
		st.ack.add(time.Since(ackStart), err)
	}
}

func telemetry(ctx context.Context, c config, client pb.AgentServiceClient, st *stats, id string) {
	stream, err := client.StreamTelemetry(ctx)
	if err != nil {
		return
	}
	t := time.NewTicker(c.telemetry)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			err := stream.Send(&pb.TelemetryReport{
				DeviceId: id, ModelId: "loadgen", Tps: 20, TtftMs: 100, MemMb: 512,
				Status: "running", TsUnix: time.Now().Unix(), Source: "stub",
			})
			if err != nil {
				return
			}
			st.telemetrySent.Add(1)
		}
	}
}

// -- Orchestration -----------------------------------------------------------

func run(c config) error {
	is, err := loadIssuer(c.caDir)
	if err != nil {
		return fmt.Errorf("load CA: %w", err)
	}
	runID := fmt.Sprintf("%x", time.Now().Unix()&0xffffff)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	st := &stats{}
	var wg sync.WaitGroup
	log.Printf("run %s: starting %d agents at %d/s against %s", runID, c.agents, c.rampPerSec, c.addr)
	rampStart := time.Now()
	gap := time.Second / time.Duration(max(c.rampPerSec, 1))
	for i := 0; i < c.agents; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			agent(ctx, c, is, st, runID, i)
		}(i)
		time.Sleep(gap)
	}
	deadline := time.Now().Add(60 * time.Second)
	for st.watching.Load() < st.connected.Load() || st.connected.Load()+st.register.errs.Load() < int64(c.agents) {
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	ramp := time.Since(rampStart)
	log.Printf("ramp done in %s: %d registered, %d watching, %d failed",
		ramp.Round(time.Millisecond), st.connected.Load(), st.watching.Load(), st.register.errs.Load())

	log.Printf("soaking for %s", c.soak)
	time.Sleep(c.soak)

	var fanout time.Duration
	var depStatus string
	if c.deploy {
		fanout, depStatus, err = fanOut(c, st, runID)
		if err != nil {
			log.Printf("fan-out deployment: %v", err)
		}
	}

	fmt.Printf("\n### Load test: %d agents (run %s)\n\n", c.agents, runID)
	fmt.Printf("Ramp: %d registered and watching in %s (%d failed). Soak: %s at a %s heartbeat, %d telemetry reports.\n\n",
		st.connected.Load(), ramp.Round(time.Millisecond), st.register.errs.Load(), c.soak, c.heartbeat, st.telemetrySent.Load())
	fmt.Println("| RPC | Calls | p50 ms | p95 ms | p99 ms | max ms | Errors |")
	fmt.Println("|-----|-------|--------|--------|--------|--------|--------|")
	fmt.Println(st.register.row("Register"))
	fmt.Println(st.heartbeat.row("SendHeartbeat"))
	fmt.Println(st.ack.row("AckDeployment"))
	if c.deploy {
		fmt.Println(st.received.row("Deployment created -> on device"))
		fmt.Printf("\nFan-out: one deployment reached all %d agents and was marked %q %s after it was created.\n",
			st.connected.Load(), depStatus, fanout.Round(time.Millisecond))
	}
	cancel()
	wg.Wait()
	return nil
}

// fanOut creates one deployment for every agent of this run and waits until
// the control plane marks it completed.
func fanOut(c config, st *stats, runID string) (time.Duration, string, error) {
	sha, err := httpGet(c, "/artifacts/"+c.artifact+".sha256")
	if err != nil {
		return 0, "", fmt.Errorf("artifact digest: %w", err)
	}
	body, _ := json.Marshal(map[string]any{
		"model_id":        "loadgen-model",
		"artifact_url":    c.api + "/artifacts/" + c.artifact,
		"artifact_sha256": strings.Fields(sha)[0],
		"tag_selector":    map[string]string{"loadgen": runID},
	})
	st.deployStart.Store(time.Now().UnixNano())
	start := time.Now()
	resp, err := apiDo(c, http.MethodPost, "/api/deployments", body)
	if err != nil {
		return 0, "", err
	}
	var dep struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp, &dep); err != nil || dep.ID == "" {
		return 0, "", fmt.Errorf("create deployment: %s", resp)
	}
	for time.Since(start) < 2*time.Minute {
		resp, err := apiDo(c, http.MethodGet, "/api/deployments/"+dep.ID, nil)
		if err == nil {
			var d struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(resp, &d) == nil && d.Status != "pending" && d.Status != "in_progress" {
				return time.Since(start), d.Status, nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return time.Since(start), "timeout", errors.New("deployment did not finish within 2 minutes")
}

func httpGet(c config, path string) (string, error) {
	b, err := apiDo(c, http.MethodGet, path, nil)
	return string(b), err
}

func apiDo(c config, method, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(method, c.api+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, b)
	}
	return b, err
}
