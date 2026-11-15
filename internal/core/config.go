// Package core builds sing-box configurations that mirror
// the Android NekoBox++ semantics and runs the embedded
// core in-process.
package core

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"nekoboxpp-pc/internal/store"
)

const (
	TagProxy  = "proxy"
	TagDirect = "direct"
	TagBlock  = "block"
)

func PoolTag(groupID int64, cc string) string {
	return fmt.Sprintf("lb-%d-%s", groupID, cc)
}

func nodeTag(id string) string { return "n-" + id }

var playDomains = []string{
	"googleapis.com", "connectivitycheck.gstatic.com", "www.google.com",
	"play-fe.googleapis.com", "play-lh.googleusercontent.com",
	"playstoregatewayadapter-pa.googleapis.com", "services.googleapis.cn",
	"xn--ngstr-lra8j.com", "play.googleapis.com", "mtalk.google.com",
}

const (
	geoSiteCNURL = "https://raw.githubusercontent.com/onlyno999/sing-rules-filtered/main/rule-set/geosite-cn.srs"
	geoIPCNURL   = "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs"
)

type poolKey struct {
	gid int64
	cc  string
}

// poolsNeeded collects every (group, country) pool the
// config must carry: the armed auto-mode pool plus every
// pool referenced by an enabled rule.
func poolsNeeded(d *store.Data) map[poolKey][]string {
	need := map[poolKey]bool{}
	if d.AutoGroupID != 0 && d.AutoCC != "" {
		need[poolKey{d.AutoGroupID, d.AutoCC}] = true
	}
	for _, r := range d.Rules {
		if r.Enabled && r.Outbound == "pool" && r.PoolCC != "" {
			need[poolKey{r.PoolGroupID, r.PoolCC}] = true
		}
	}
	out := map[poolKey][]string{}
	for k := range need {
		var members []string
		for _, n := range d.Nodes {
			if n.GroupID != k.gid {
				continue
			}
			if d.Regions[n.Fingerprint] == k.cc {
				members = append(members, nodeTag(n.ID))
			}
		}
		if len(members) > 0 {
			out[k] = members
		}
	}
	return out
}

func poolOutbound(tag string, members []string) map[string]any {
	return map[string]any{
		"type":      "urltest",
		"tag":       tag,
		"outbounds": members,
		"url":       "https://www.gstatic.com/generate_204",
		"interval":  "5m",
		"tolerance": 50,
	}
}

func cloneOutbound(raw json.RawMessage, tag string) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	m["tag"] = tag
	return m, nil
}

// BuildMain renders the full running configuration.
func BuildMain(d *store.Data) ([]byte, error) {
	if len(d.Nodes) == 0 {
		return nil, fmt.Errorf("no nodes")
	}
	pools := poolsNeeded(d)

	var outbounds []any
	outbounds = append(outbounds,
		map[string]any{"type": "direct", "tag": TagDirect},
		map[string]any{"type": "block", "tag": TagBlock},
		map[string]any{"type": "dns", "tag": "dns-out"},
	)
	for _, n := range d.Nodes {
		ob, err := cloneOutbound(n.Outbound, nodeTag(n.ID))
		if err != nil {
			return nil, err
		}
		outbounds = append(outbounds, ob)
	}
	for k, members := range pools {
		outbounds = append(outbounds, poolOutbound(PoolTag(k.gid, k.cc), members))
	}
	// The general line is always the selected node's own
	// outbound (mirrors Android: in auto mode the selection
	// is the pool's current carrier member, and the
	// balancer moves it on death).
	sel := d.NodeByID(d.SelectedID)
	if sel == nil {
		sel = &d.Nodes[0]
	}
	ob, err := cloneOutbound(sel.Outbound, TagProxy)
	if err != nil {
		return nil, err
	}
	outbounds = append(outbounds, ob)

	var inbounds []any
	inbounds = append(inbounds, map[string]any{
		"type":        "mixed",
		"tag":         "mixed-in",
		"listen":      "127.0.0.1",
		"listen_port": d.Settings.MixedPort,
	})
	if d.Settings.Mode == "tun" {
		inbounds = append(inbounds, map[string]any{
			"type":           "tun",
			"tag":            "tun-in",
			"interface_name": "NekoBoxPlusPlus",
			"address":        []string{"172.19.0.1/30"},
			"mtu":            9000,
			"auto_route":     true,
			"strict_route":   true,
			"stack":          "mixed",
		})
	}

	var rules []any
	rules = append(rules, map[string]any{"protocol": "dns", "outbound": "dns-out"})
	for _, r := range d.Rules {
		if !r.Enabled {
			continue
		}
		target := r.Outbound
		if target == "pool" {
			tag := PoolTag(r.PoolGroupID, r.PoolCC)
			if _, ok := pools[poolKey{r.PoolGroupID, r.PoolCC}]; !ok {
				continue
			}
			target = tag
		}
		rules = append(rules, map[string]any{
			"domain_suffix": splitDomains(r.Content),
			"outbound":      target,
		})
	}
	var ruleSets []any
	if d.BuiltinPlay {
		rules = append(rules, map[string]any{
			"domain_suffix": playDomains,
			"outbound":      TagProxy,
		})
	}
	if d.BuiltinCN {
		ruleSets = append(ruleSets,
			map[string]any{
				"type":            "remote",
				"tag":             "geosite-cn",
				"format":          "binary",
				"url":             geoSiteCNURL,
				"download_detour": TagProxy,
				"update_interval": "24h",
			},
			map[string]any{
				"type":            "remote",
				"tag":             "geoip-cn",
				"format":          "binary",
				"url":             geoIPCNURL,
				"download_detour": TagProxy,
				"update_interval": "24h",
			},
		)
		rules = append(rules, map[string]any{
			"rule_set": []string{"geosite-cn", "geoip-cn"},
			"outbound": TagDirect,
		})
	}

	cfg := map[string]any{
		"log":   map[string]any{"level": "info", "timestamp": true},
		"dns":   dnsConfig(d.BuiltinCN),
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route": map[string]any{
			"rules":                 rules,
			"rule_set":              ruleSets,
			"final":                 TagProxy,
			"auto_detect_interface": true,
		},
	}
	return json.Marshal(cfg)
}

func dnsConfig(withCN bool) map[string]any {
	dns := map[string]any{
		"servers": []any{
			map[string]any{
				"tag":     "remote",
				"address": "https://223.5.5.5/dns-query",
				"detour":  TagProxy,
			},
			map[string]any{
				"tag":     "direct",
				"address": "https://223.5.5.5/dns-query",
				"detour":  TagDirect,
			},
		},
		"final":    "remote",
		"strategy": "ipv4_only",
	}
	if withCN {
		dns["rules"] = []any{
			map[string]any{"rule_set": []string{"geosite-cn"}, "server": "direct"},
		}
	}
	return dns
}

func splitDomains(content string) []string {
	var out []string
	for _, p := range strings.Split(content, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// BuildProbe renders a one-node configuration used by the
// region probe and the latency test: a mixed inbound on a
// loopback port and the node as the only way out.
func BuildProbe(n *store.Node, port int) ([]byte, error) {
	ob, err := cloneOutbound(n.Outbound, TagProxy)
	if err != nil {
		return nil, err
	}
	// Target names must resolve THROUGH the node (remote DoH
	// detoured via the node itself): resolving them with the
	// system resolver hands the server polluted or fake IPs
	// and every probe times out. The node's own server name,
	// when it is a domain, resolves locally to avoid a loop.
	dns := map[string]any{
		"servers": []any{
			map[string]any{
				"tag":     "remote",
				"address": "https://223.5.5.5/dns-query",
				"detour":  TagProxy,
			},
			map[string]any{
				"tag":     "local",
				"address": "local",
			},
		},
		"final":    "remote",
		"strategy": "ipv4_only",
	}
	if srv, _ := ob["server"].(string); srv != "" && net.ParseIP(srv) == nil {
		dns["rules"] = []any{
			map[string]any{"domain": []string{srv}, "server": "local"},
		}
	}
	cfg := map[string]any{
		"log": map[string]any{"level": "warn", "timestamp": true},
		"dns": dns,
		"inbounds": []any{
			map[string]any{
				"type":        "mixed",
				"tag":         "probe-in",
				"listen":      "127.0.0.1",
				"listen_port": port,
			},
		},
		"outbounds": []any{
			ob,
			map[string]any{"type": "direct", "tag": TagDirect},
		},
		"route": map[string]any{"final": TagProxy},
	}
	return json.Marshal(cfg)
}
