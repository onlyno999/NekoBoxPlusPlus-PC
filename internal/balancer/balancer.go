// Package balancer carries the Android upkeep model to
// the desktop: standby rounds that only refresh records,
// death judged by the core's own dial-failure counter,
// whole-group region sweeps, a full sweep whenever the
// network changes, and tests for newly arrived nodes
// after a subscription update.
package balancer

import (
	"context"
	"net"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"

	"nekoboxpp-pc/internal/geo"
	"nekoboxpp-pc/internal/store"
)

type Manager struct {
	Store *store.Store

	// ApplyConfig rebuilds the running configuration from
	// the store (selection or pool changed). Supplied by
	// the app layer.
	ApplyConfig func() error
	// UpdateGroup refreshes one subscription. Supplied by
	// the app layer.
	UpdateGroup func(gid int64) error
	// Changed is pinged whenever records move so the UI
	// can refresh.
	Changed func()

	sweepRunning atomic.Bool
	roundRunning atomic.Bool

	healthForSelected string
	rotationCursor    int
	lastNetSweep      time.Time
}

func (m *Manager) ping() {
	if m.Changed != nil {
		m.Changed()
	}
}

func (m *Manager) poolMembers(gid int64, cc string) []store.Node {
	var out []store.Node
	for _, n := range m.Store.Data.Nodes {
		if n.GroupID == gid && m.Store.Data.Regions[n.Fingerprint] == cc {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ---- region sweeps ----

func (m *Manager) SweepRegions(ctx context.Context, nodes []store.Node) {
	if !m.sweepRunning.CompareAndSwap(false, true) {
		return
	}
	defer m.sweepRunning.Store(false)
	jobs := make(chan store.Node, len(nodes))
	for _, n := range nodes {
		jobs <- n
	}
	close(jobs)
	done := make(chan struct{}, 32)
	for w := 0; w < 32; w++ {
		go func() {
			for n := range jobs {
				cc, err := geo.Region(ctx, &n)
				if err == nil && cc != "" && m.Store.Data.Regions[n.Fingerprint] != cc {
					m.Store.Data.Regions[n.Fingerprint] = cc
				}
			}
			done <- struct{}{}
		}()
	}
	for w := 0; w < 32; w++ {
		<-done
	}
	m.Store.Save()
	m.ping()
}

func (m *Manager) SweepGroup(ctx context.Context, gid int64) {
	m.SweepRegions(ctx, m.Store.NodesOfGroup(gid))
}

func (m *Manager) SweepAll(ctx context.Context) {
	m.SweepRegions(ctx, m.Store.Data.Nodes)
}

// ---- latency ----

func (m *Manager) TestLatency(ctx context.Context, nodes []store.Node) {
	jobs := make(chan store.Node, len(nodes))
	for _, n := range nodes {
		jobs <- n
	}
	close(jobs)
	done := make(chan struct{}, 16)
	for w := 0; w < 16; w++ {
		go func() {
			for n := range jobs {
				m.Store.Data.Latency[n.Fingerprint] = geo.Latency(ctx, &n)
			}
			done <- struct{}{}
		}()
	}
	for w := 0; w < 16; w++ {
		<-done
	}
	m.Store.Save()
	m.ping()
}

// TestNewNodes runs region + latency for freshly arrived
// nodes, in batches of ten, after a subscription update.
func (m *Manager) TestNewNodes(ctx context.Context, gid int64, fps []string) {
	if len(fps) == 0 {
		return
	}
	want := map[string]bool{}
	for _, fp := range fps {
		want[fp] = true
	}
	var fresh []store.Node
	for _, n := range m.Store.NodesOfGroup(gid) {
		if want[n.Fingerprint] {
			fresh = append(fresh, n)
		}
	}
	for i := 0; i < len(fresh); i += 10 {
		end := i + 10
		if end > len(fresh) {
			end = len(fresh)
		}
		batch := fresh[i:end]
		m.SweepRegions(ctx, batch)
		m.TestLatency(ctx, batch)
	}
}

// ---- standby round (armed pool, records only) ----

func (m *Manager) StandbyRound(ctx context.Context) {
	d := m.Store.Data
	if d.AutoGroupID == 0 || d.AutoCC == "" {
		return
	}
	if !m.roundRunning.CompareAndSwap(false, true) {
		return
	}
	defer m.roundRunning.Store(false)
	members := m.poolMembers(d.AutoGroupID, d.AutoCC)
	if len(members) == 0 {
		return
	}
	const batch = 5
	if len(members) <= batch {
		m.TestLatency(ctx, members)
		return
	}
	start := m.rotationCursor % len(members)
	var slice []store.Node
	for i := 0; i < batch; i++ {
		slice = append(slice, members[(start+i)%len(members)])
	}
	m.rotationCursor = (start + batch) % len(members)
	m.TestLatency(ctx, slice)
}

// ---- watchdog: death is passive, from the core ----

func (m *Manager) WatchdogTick() {
	d := m.Store.Data
	if d.AutoGroupID == 0 || d.AutoCC == "" {
		return
	}
	sel := d.SelectedID
	if sel != m.healthForSelected {
		m.healthForSelected = sel
		adapter.ResetDialHealth("proxy")
		return
	}
	if adapter.DialConsecutiveFailures("proxy") < 3 {
		return
	}
	m.failOver()
}

func (m *Manager) failOver() {
	d := m.Store.Data
	members := m.poolMembers(d.AutoGroupID, d.AutoCC)
	var best *store.Node
	bestMs := int(^uint(0) >> 1)
	for i := range members {
		if members[i].ID == d.SelectedID {
			continue
		}
		ms, ok := d.Latency[members[i].Fingerprint]
		if !ok || ms < 0 {
			continue
		}
		if ms < bestMs {
			bestMs = ms
			best = &members[i]
		}
	}
	if best == nil {
		return
	}
	d.SelectedID = best.ID
	m.healthForSelected = best.ID
	adapter.ResetDialHealth("proxy")
	m.Store.Save()
	if m.ApplyConfig != nil {
		_ = m.ApplyConfig()
	}
	m.ping()
}

// SteerIntoPool makes sure the selection sits inside the
// armed pool, moving it to the best recorded member when
// it does not.
func (m *Manager) SteerIntoPool() {
	d := m.Store.Data
	if d.AutoGroupID == 0 || d.AutoCC == "" {
		return
	}
	members := m.poolMembers(d.AutoGroupID, d.AutoCC)
	for _, n := range members {
		if n.ID == d.SelectedID {
			return
		}
	}
	bestMs := int(^uint(0) >> 1)
	var best *store.Node
	for i := range members {
		ms, ok := d.Latency[members[i].Fingerprint]
		if ok && ms >= 0 && ms < bestMs {
			bestMs = ms
			best = &members[i]
		}
	}
	if best == nil && len(members) > 0 {
		best = &members[0]
	}
	if best != nil {
		d.SelectedID = best.ID
		m.Store.Save()
	}
}

// ---- loops ----

func networkSignature() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var parts []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagLoopback != 0 || ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				parts = append(parts, ifc.Name+"="+ipnet.IP.String())
			}
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// Run starts every periodic loop until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	go m.loop(ctx, 10*time.Second, func() { m.WatchdogTick() })
	go m.loop(ctx, 5*time.Minute, func() { m.StandbyRound(ctx) })
	go m.loop(ctx, 30*time.Minute, func() {
		if gid := m.Store.Data.AutoGroupID; gid != 0 {
			m.SweepGroup(ctx, gid)
		}
	})
	go m.loop(ctx, time.Minute, func() { m.autoUpdateTick(ctx) })
	go m.networkLoop(ctx)
}

func (m *Manager) loop(ctx context.Context, every time.Duration, fn func()) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
		}
	}
}

func (m *Manager) networkLoop(ctx context.Context) {
	base := networkSignature()
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sig := networkSignature()
			if sig == "" || sig == base {
				continue
			}
			base = sig
			if time.Since(m.lastNetSweep) < 3*time.Minute {
				continue
			}
			m.lastNetSweep = time.Now()
			time.AfterFunc(3*time.Second, func() {
				m.SweepAll(ctx)
			})
		}
	}
}

func (m *Manager) autoUpdateTick(ctx context.Context) {
	if m.UpdateGroup == nil {
		return
	}
	now := time.Now().Unix()
	for _, g := range m.Store.Data.Groups {
		if !g.AutoUpdate || g.URL == "" {
			continue
		}
		delay := int64(g.AutoDelayMin) * 60
		if delay <= 0 {
			delay = 3600
		}
		if now-g.UpdatedAt >= delay {
			_ = m.UpdateGroup(g.ID)
		}
	}
}
