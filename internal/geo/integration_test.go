package geo_test

// End-to-end pipeline test: a local sing-box server
// (VLESS + WebSocket, plain and TLS — the Cloudflare CDN
// node shape) is stood up on loopback, subscription links
// are parsed by the subs package, and the geo probes plus
// the main config must all work through the parsed nodes.
// This is the exact chain that failed in the field with
// "unknown transport type: websocket".

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"nekoboxpp-pc/internal/core"
	"nekoboxpp-pc/internal/geo"
	"nekoboxpp-pc/internal/store"
	"nekoboxpp-pc/internal/subs"
)

const testUUID = "11111111-2222-3333-4444-555555555555"

func writeCert(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(
		&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(
		&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

// egressJSON is the test server's way out: this sandbox
// has no direct internet, everything leaves through the
// CONNECT proxy named by the *_PROXY env vars. On a normal
// machine it falls back to a direct outbound.
func egressJSON() string {
	raw := os.Getenv("HTTPS_PROXY")
	if raw == "" {
		raw = os.Getenv("https_proxy")
	}
	if raw == "" {
		return `{"type": "direct", "tag": "direct"}`
	}
	u, err := url.Parse(raw)
	if err != nil {
		return `{"type": "direct", "tag": "direct"}`
	}
	port := 3128
	if p, err := strconv.Atoi(u.Port()); err == nil {
		port = p
	}
	ob := map[string]any{"type": "http", "tag": "direct",
		"server": u.Hostname(), "server_port": port}
	if u.User != nil {
		ob["username"] = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			ob["password"] = pw
		}
	}
	b, _ := json.Marshal(ob)
	return string(b)
}

func startServer(t *testing.T, port int, tlsBlock string) func() {
	t.Helper()
	cfg := fmt.Sprintf(`{
	  "log": {"level": "error"},
	  "inbounds": [{
	    "type": "vless", "tag": "in",
	    "listen": "127.0.0.1", "listen_port": %d,
	    "users": [{"uuid": "%s"}],
	    "transport": {"type": "ws", "path": "/ws"}
	    %s
	  }],
	  "outbounds": [%s],
	  "route": {"final": "direct"}
	}`, port, testUUID, tlsBlock, egressJSON())
	stop, err := core.StartProbe([]byte(cfg))
	if err != nil {
		t.Fatalf("server start (port %d): %v", port, err)
	}
	return stop
}

func parseOne(t *testing.T, link string) store.Node {
	t.Helper()
	parsed, err := subs.Parse([]byte(link))
	if err != nil {
		t.Fatalf("parse link: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("want 1 node, got %d", len(parsed))
	}
	n := parsed[0].ToNode(1)
	raw := string(n.Outbound)
	if strings.Contains(raw, "websocket") {
		t.Fatalf("outbound still uses legacy transport name: %s", raw)
	}
	if !strings.Contains(raw, `"ws"`) {
		t.Fatalf("outbound missing ws transport: %s", raw)
	}
	return n
}

func TestProbePipeline(t *testing.T) {
	certPath, keyPath := writeCert(t)
	tls := fmt.Sprintf(`, "tls": {"enabled": true, "server_name": "127.0.0.1",
		"certificate_path": %q, "key_path": %q}`, certPath, keyPath)
	stopTLS := startServer(t, 18081, tls)
	defer stopTLS()
	stopPlain := startServer(t, 18082, "")
	defer stopPlain()
	time.Sleep(300 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	wss := parseOne(t, "vless://"+testUUID+"@127.0.0.1:18081?encryption=none"+
		"&security=tls&sni=127.0.0.1&allowInsecure=1&type=ws&host=127.0.0.1&path=%2Fws#WSS")
	ws := parseOne(t, "vless://"+testUUID+"@127.0.0.1:18082?encryption=none"+
		"&security=none&type=ws&path=%2Fws#WS")

	if ms := geo.Latency(ctx, &wss); ms <= 0 {
		t.Errorf("WSS latency = %d, want > 0", ms)
	} else {
		t.Logf("WSS latency: %d ms", ms)
	}
	if ms := geo.Latency(ctx, &ws); ms <= 0 {
		t.Errorf("WS latency = %d, want > 0", ms)
	} else {
		t.Logf("WS latency: %d ms", ms)
	}
	cc, err := geo.Region(ctx, &wss)
	if err != nil || len(cc) != 2 {
		t.Errorf("WSS region = %q, err = %v; want a 2-letter code", cc, err)
	} else {
		t.Logf("WSS region: %s", cc)
	}

	// Main config: build, start, fetch through the proxy port.
	d := &store.Data{
		Nodes:      []store.Node{wss},
		SelectedID: wss.ID,
		Regions:    map[string]string{},
		Latency:    map[string]int{},
		Traffic:    map[string][2]int64{},
		Settings:   store.Settings{Mode: "system", MixedPort: 12080},
	}
	raw, err := core.BuildMain(d)
	if err != nil {
		t.Fatalf("BuildMain: %v", err)
	}
	stop, err := core.StartProbe(raw)
	if err != nil {
		t.Fatalf("main config start: %v", err)
	}
	defer stop()
	proxyURL, _ := url.Parse("http://127.0.0.1:12080")
	client := &http.Client{Timeout: 15 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	deadline := time.Now().Add(20 * time.Second)
	for {
		resp, err := client.Get("https://www.gstatic.com/generate_204")
		if err == nil && resp.StatusCode == 204 {
			resp.Body.Close()
			break
		}
		if err == nil {
			resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("fetch through main config failed: %v", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Log("main config fetch: 204 OK")
}
