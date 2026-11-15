package geo_test

// Real-subscription probe test, gated by env so a plain
// `go test` never touches it:
//
//	NBPP_SUB_FILE=/path/to/sub.txt go test -run TestRealNodes
//
// The sandbox has no direct internet, so each probe config
// gets an extra "egress" HTTP outbound (from *_PROXY env)
// and the node outbound detours through it. The WSS/TLS
// session itself stays end-to-end with the real server.

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"nekoboxpp-pc/internal/core"
	"nekoboxpp-pc/internal/store"
	"nekoboxpp-pc/internal/subs"
)

func egressMap() map[string]any {
	raw := os.Getenv("HTTPS_PROXY")
	if raw == "" {
		raw = os.Getenv("https_proxy")
	}
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	port := 3128
	if p, err := strconv.Atoi(u.Port()); err == nil {
		port = p
	}
	// Use the proxy's IP, not its name: inside the probe
	// box the name would resolve through the tunnel DNS,
	// which itself needs the tunnel — a deadlock.
	host := u.Hostname()
	if ips, err := net.LookupHost(host); err == nil {
		for _, ip := range ips {
			if strings.Contains(ip, ".") {
				host = ip
				break
			}
		}
	}
	ob := map[string]any{"type": "http", "tag": "egress",
		"server": host, "server_port": port}
	if u.User != nil {
		ob["username"] = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			ob["password"] = pw
		}
	}
	return ob
}

func probeReal(t *testing.T, n store.Node, port int) (int, string) {
	raw, err := core.BuildProbe(&n, port)
	if err != nil {
		t.Fatalf("BuildProbe: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if eg := egressMap(); eg != nil {
		obs := cfg["outbounds"].([]any)
		obs[0].(map[string]any)["detour"] = "egress"
		cfg["outbounds"] = append(obs, eg)
		raw, _ = json.Marshal(cfg)
	}
	stop, err := core.StartProbe(raw)
	if err != nil {
		t.Fatalf("probe start for %s: %v", n.Name, err)
	}
	defer stop()
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	client := &http.Client{Timeout: 15 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL),
			DisableKeepAlives: true}}
	lat := -1
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		start := time.Now()
		resp, err := client.Get("https://www.gstatic.com/generate_204")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 204 {
				lat = int(time.Since(start).Milliseconds())
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if lat < 0 {
		return -1, ""
	}
	resp, err := client.Get("https://cp.cloudflare.com/cdn-cgi/trace")
	if err != nil {
		return lat, ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "loc=") {
			return lat, strings.ToUpper(strings.TrimSpace(line[4:]))
		}
	}
	return lat, ""
}

func TestRealNodes(t *testing.T) {
	path := os.Getenv("NBPP_SUB_FILE")
	if path == "" {
		t.Skip("NBPP_SUB_FILE not set")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := subs.Parse(body)
	if err != nil {
		t.Fatalf("subs.Parse: %v", err)
	}
	t.Logf("parsed %d nodes", len(parsed))
	limit := 4
	if len(parsed) < limit {
		limit = len(parsed)
	}
	failures := 0
	for i := 0; i < limit; i++ {
		n := parsed[i].ToNode(1)
		lat, cc := probeReal(t, n, 23100+i)
		t.Logf("node %d [%s/%s] latency=%d region=%q", i, n.Type, n.Name, lat, cc)
		if lat <= 0 {
			failures++
		}
	}
	if failures == limit {
		t.Fatalf("all %d probed nodes failed", limit)
	}
}
