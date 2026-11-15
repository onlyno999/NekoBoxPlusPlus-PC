// Package geo measures node exit regions and latencies by
// running each node in a throwaway core instance and
// fetching through it, exactly like the Android probe.
package geo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"nekoboxpp-pc/internal/core"
	"nekoboxpp-pc/internal/store"
)

var portSeq atomic.Int64

func nextPort() int {
	base := int64(21000 + (time.Now().UnixNano()/1e6)%2000)
	return int(base + portSeq.Add(1)%500)
}

func probeClient(port int) *http.Client {
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	return &http.Client{
		Timeout: 12 * time.Second,
		Transport: &http.Transport{
			Proxy:               http.ProxyURL(proxyURL),
			DisableKeepAlives:   true,
			TLSHandshakeTimeout: 8 * time.Second,
		},
	}
}

func withProbe(n *store.Node, fn func(client *http.Client) error) error {
	port := nextPort()
	raw, err := core.BuildProbe(n, port)
	if err != nil {
		return err
	}
	stop, err := core.StartProbe(raw)
	if err != nil {
		return err
	}
	defer stop()
	// The listener needs a moment to come up.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := fn(probeClient(port)); err == nil {
			return nil
		} else if time.Now().After(deadline) {
			return err
		}
		time.Sleep(120 * time.Millisecond)
	}
}

// Region returns the node's exit country code: Cloudflare
// trace first, ip.sb as the fallback, like Android.
func Region(ctx context.Context, n *store.Node) (string, error) {
	var cc string
	err := withProbe(n, func(client *http.Client) error {
		if c, err := fetchTrace(ctx, client); err == nil && len(c) == 2 {
			cc = c
			return nil
		}
		if c, err := fetchIPSB(ctx, client); err == nil && len(c) == 2 {
			cc = c
			return nil
		}
		return fmt.Errorf("region unknown")
	})
	return cc, err
}

func fetchTrace(ctx context.Context, client *http.Client) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://cp.cloudflare.com/cdn-cgi/trace", nil)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "loc=") {
			return strings.ToUpper(strings.TrimSpace(line[4:])), nil
		}
	}
	return "", fmt.Errorf("no loc")
}

func fetchIPSB(ctx context.Context, client *http.Client) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.ip.sb/geoip", nil)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var v struct {
		CountryCode string `json:"country_code"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&v); err != nil {
		return "", err
	}
	return strings.ToUpper(v.CountryCode), nil
}

// Latency times one generate_204 fetch through the node;
// -1 when the fetch fails.
func Latency(ctx context.Context, n *store.Node) int {
	ms := -1
	_ = withProbe(n, func(client *http.Client) error {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
			"https://www.gstatic.com/generate_204", nil)
		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		ms = int(time.Since(start).Milliseconds())
		return nil
	})
	return ms
}
