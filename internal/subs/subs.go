// Package subs fetches subscription bodies and parses the
// common share-link formats plus sing-box JSON into
// sing-box outbound objects.
package subs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"nekoboxpp-pc/internal/store"
)

type Parsed struct {
	Name string
	Type string
	Out  map[string]any
}

func (p Parsed) ToNode(groupID int64) store.Node {
	server, _ := p.Out["server"].(string)
	port := 0
	switch v := p.Out["server_port"].(type) {
	case int:
		port = v
	case float64:
		port = int(v)
	}
	fp := store.Fingerprint(p.Type, server, port)
	raw, _ := json.Marshal(p.Out)
	return store.Node{
		ID:          store.IDOf(fp),
		GroupID:     groupID,
		Name:        p.Name,
		Type:        p.Type,
		Fingerprint: fp,
		Outbound:    raw,
	}
}

func Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "NekoBoxPlusPlus-PC/1.0")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}

// Parse reads a subscription body in any supported shape.
func Parse(body []byte) ([]Parsed, error) {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil, fmt.Errorf("empty subscription")
	}
	// sing-box JSON: {"outbounds":[...]} or a bare array.
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		if out, err := parseSingBoxJSON(text); err == nil && len(out) > 0 {
			return out, nil
		}
	}
	// Possibly base64 wrapped link list.
	if decoded, err := b64decode(text); err == nil {
		if strings.Contains(decoded, "://") {
			text = decoded
		}
	}
	var out []Parsed
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if p, err := parseLink(line); err == nil {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no nodes parsed")
	}
	return out, nil
}

func parseSingBoxJSON(text string) ([]Parsed, error) {
	var arr []map[string]any
	var wrap struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(text), &wrap); err == nil && len(wrap.Outbounds) > 0 {
		arr = wrap.Outbounds
	} else if err := json.Unmarshal([]byte(text), &arr); err != nil {
		return nil, err
	}
	var out []Parsed
	for _, ob := range arr {
		typ, _ := ob["type"].(string)
		server, _ := ob["server"].(string)
		if typ == "" || server == "" {
			continue // selectors, urltests, direct etc. are not nodes
		}
		name, _ := ob["tag"].(string)
		if name == "" {
			name = server
		}
		ob["tag"] = name
		out = append(out, Parsed{Name: name, Type: typ, Out: ob})
	}
	return out, nil
}

func b64decode(s string) (string, error) {
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, s)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.URLEncoding,
		base64.RawStdEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b), nil
		}
	}
	return "", fmt.Errorf("not base64")
}

func parseLink(link string) (Parsed, error) {
	switch {
	case strings.HasPrefix(link, "vless://"):
		return parseVLESS(link)
	case strings.HasPrefix(link, "vmess://"):
		return parseVMess(link)
	case strings.HasPrefix(link, "trojan://"):
		return parseTrojan(link)
	case strings.HasPrefix(link, "ss://"):
		return parseSS(link)
	case strings.HasPrefix(link, "hysteria2://"), strings.HasPrefix(link, "hy2://"):
		return parseHY2(link)
	case strings.HasPrefix(link, "tuic://"):
		return parseTUIC(link)
	}
	return Parsed{}, fmt.Errorf("unsupported link")
}

func splitLink(link string) (u *url.URL, name string, err error) {
	u, err = url.Parse(link)
	if err != nil {
		return nil, "", err
	}
	name = u.Fragment
	if name == "" {
		name = u.Host
	}
	return u, name, nil
}

func portOf(u *url.URL) int {
	p, _ := strconv.Atoi(u.Port())
	return p
}

func tlsBlock(q url.Values, serverName string) map[string]any {
	tls := map[string]any{"enabled": true}
	if serverName != "" {
		tls["server_name"] = serverName
	}
	if fp := q.Get("fp"); fp != "" {
		tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
	}
	if q.Get("security") == "reality" {
		reality := map[string]any{"enabled": true}
		if pbk := q.Get("pbk"); pbk != "" {
			reality["public_key"] = pbk
		}
		if sid := q.Get("sid"); sid != "" {
			reality["short_id"] = sid
		}
		tls["reality"] = reality
	}
	if q.Get("allowInsecure") == "1" || q.Get("insecure") == "1" {
		tls["insecure"] = true
	}
	return tls
}

func transportBlock(q url.Values) map[string]any {
	typ := q.Get("type")
	switch typ {
	case "ws", "websocket":
		t := map[string]any{"type": "ws"}
		if p := q.Get("path"); p != "" {
			t["path"] = p
		}
		if h := q.Get("host"); h != "" {
			t["headers"] = map[string]any{"Host": h}
		}
		return t
	case "grpc":
		t := map[string]any{"type": "grpc"}
		if sn := q.Get("serviceName"); sn != "" {
			t["service_name"] = sn
		}
		return t
	case "http":
		t := map[string]any{"type": "http"}
		if h := q.Get("host"); h != "" {
			t["host"] = []string{h}
		}
		if p := q.Get("path"); p != "" {
			t["path"] = p
		}
		return t
	}
	return nil
}

func parseVLESS(link string) (Parsed, error) {
	u, name, err := splitLink(link)
	if err != nil {
		return Parsed{}, err
	}
	q := u.Query()
	out := map[string]any{
		"type":        "vless",
		"tag":         name,
		"server":      u.Hostname(),
		"server_port": portOf(u),
		"uuid":        u.User.Username(),
	}
	if flow := q.Get("flow"); flow != "" {
		out["flow"] = flow
	}
	sec := q.Get("security")
	if sec == "tls" || sec == "reality" {
		out["tls"] = tlsBlock(q, q.Get("sni"))
	}
	if tr := transportBlock(q); tr != nil {
		out["transport"] = tr
	}
	return Parsed{Name: name, Type: "vless", Out: out}, nil
}

func parseVMess(link string) (Parsed, error) {
	raw, err := b64decode(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		return Parsed{}, err
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return Parsed{}, err
	}
	str := func(k string) string {
		if s, ok := v[k].(string); ok {
			return s
		}
		if f, ok := v[k].(float64); ok {
			return strconv.Itoa(int(f))
		}
		return ""
	}
	name := str("ps")
	port, _ := strconv.Atoi(str("port"))
	out := map[string]any{
		"type":        "vmess",
		"tag":         name,
		"server":      str("add"),
		"server_port": port,
		"uuid":        str("id"),
		"security":    "auto",
	}
	if aid, _ := strconv.Atoi(str("aid")); aid > 0 {
		out["alter_id"] = aid
	}
	q := url.Values{}
	q.Set("type", str("net"))
	q.Set("path", str("path"))
	q.Set("host", str("host"))
	q.Set("serviceName", str("path"))
	if tr := transportBlock(q); tr != nil {
		out["transport"] = tr
	}
	if str("tls") == "tls" {
		out["tls"] = map[string]any{"enabled": true, "server_name": str("sni")}
	}
	return Parsed{Name: name, Type: "vmess", Out: out}, nil
}

func parseTrojan(link string) (Parsed, error) {
	u, name, err := splitLink(link)
	if err != nil {
		return Parsed{}, err
	}
	q := u.Query()
	out := map[string]any{
		"type":        "trojan",
		"tag":         name,
		"server":      u.Hostname(),
		"server_port": portOf(u),
		"password":    u.User.Username(),
		"tls":         tlsBlock(q, q.Get("sni")),
	}
	if tr := transportBlock(q); tr != nil {
		out["transport"] = tr
	}
	return Parsed{Name: name, Type: "trojan", Out: out}, nil
}

func parseSS(link string) (Parsed, error) {
	u, name, err := splitLink(link)
	if err != nil {
		return Parsed{}, err
	}
	var method, password string
	user := u.User
	if user != nil {
		if decoded, derr := b64decode(user.Username()); derr == nil && strings.Contains(decoded, ":") {
			parts := strings.SplitN(decoded, ":", 2)
			method, password = parts[0], parts[1]
		} else {
			method = user.Username()
			password, _ = user.Password()
		}
	}
	out := map[string]any{
		"type":        "shadowsocks",
		"tag":         name,
		"server":      u.Hostname(),
		"server_port": portOf(u),
		"method":      method,
		"password":    password,
	}
	return Parsed{Name: name, Type: "shadowsocks", Out: out}, nil
}

func parseHY2(link string) (Parsed, error) {
	link = strings.Replace(link, "hy2://", "hysteria2://", 1)
	u, name, err := splitLink(link)
	if err != nil {
		return Parsed{}, err
	}
	q := u.Query()
	auth := ""
	if u.User != nil {
		auth = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			auth += ":" + pw
		}
	}
	tls := map[string]any{"enabled": true}
	if sni := q.Get("sni"); sni != "" {
		tls["server_name"] = sni
	}
	if q.Get("insecure") == "1" {
		tls["insecure"] = true
	}
	out := map[string]any{
		"type":        "hysteria2",
		"tag":         name,
		"server":      u.Hostname(),
		"server_port": portOf(u),
		"password":    auth,
		"tls":         tls,
	}
	if obfs := q.Get("obfs"); obfs != "" {
		o := map[string]any{"type": obfs}
		if pw := q.Get("obfs-password"); pw != "" {
			o["password"] = pw
		}
		out["obfs"] = o
	}
	return Parsed{Name: name, Type: "hysteria2", Out: out}, nil
}

func parseTUIC(link string) (Parsed, error) {
	u, name, err := splitLink(link)
	if err != nil {
		return Parsed{}, err
	}
	q := u.Query()
	pw, _ := u.User.Password()
	tls := map[string]any{"enabled": true}
	if sni := q.Get("sni"); sni != "" {
		tls["server_name"] = sni
	}
	if alpn := q.Get("alpn"); alpn != "" {
		tls["alpn"] = strings.Split(alpn, ",")
	}
	out := map[string]any{
		"type":        "tuic",
		"tag":         name,
		"server":      u.Hostname(),
		"server_port": portOf(u),
		"uuid":        u.User.Username(),
		"password":    pw,
		"tls":         tls,
	}
	if cc := q.Get("congestion_control"); cc != "" {
		out["congestion_control"] = cc
	}
	return Parsed{Name: name, Type: "tuic", Out: out}, nil
}
