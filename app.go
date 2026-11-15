package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"nekoboxpp-pc/internal/balancer"
	"nekoboxpp-pc/internal/core"
	"nekoboxpp-pc/internal/store"
	"nekoboxpp-pc/internal/subs"
)

// ---------- DTOs for the frontend ----------

type NodeDTO struct {
	ID       string `json:"id"`
	GroupID  int64  `json:"groupId"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Region   string `json:"region"`
	Latency  int    `json:"latency"`
	HasLat   bool   `json:"hasLatency"`
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
	Selected bool   `json:"selected"`
}

type GroupDTO struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	URL          string `json:"url"`
	UpdatedAt    int64  `json:"updatedAt"`
	AutoUpdate   bool   `json:"autoUpdate"`
	AutoDelayMin int    `json:"autoDelayMin"`
	NodeCount    int    `json:"nodeCount"`
}

type RuleDTO struct {
	Name        string `json:"name"`
	Content     string `json:"content"`
	Outbound    string `json:"outbound"`
	PoolGroupID int64  `json:"poolGroupId"`
	PoolCC      string `json:"poolCc"`
	Enabled     bool   `json:"enabled"`
}

type StateDTO struct {
	Running     bool       `json:"running"`
	Mode        string     `json:"mode"`
	MixedPort   int        `json:"mixedPort"`
	SelectedID  string     `json:"selectedId"`
	AutoGroupID int64      `json:"autoGroupId"`
	AutoCC      string     `json:"autoCc"`
	Groups      []GroupDTO `json:"groups"`
	Nodes       []NodeDTO  `json:"nodes"`
	Rules       []RuleDTO  `json:"rules"`
	BuiltinPlay bool       `json:"builtinPlay"`
	BuiltinCN   bool       `json:"builtinCn"`
	UpSpeed     int64      `json:"upSpeed"`
	DownSpeed   int64      `json:"downSpeed"`
	LastError   string     `json:"lastError"`
}

// ---------- App ----------

type App struct {
	ctx    context.Context
	store  *store.Store
	runner *core.Runner
	bal    *balancer.Manager

	logMu sync.Mutex
	logs  []string

	trafMu   sync.Mutex
	lastTraf map[string][2]int64
	lastTime time.Time
	speeds   [2]int64

	errMu     sync.Mutex
	lastError string
}

func NewApp() *App {
	a := &App{
		store:  store.Open(),
		runner: &core.Runner{},
	}
	a.bal = &balancer.Manager{
		Store: a.store,
		ApplyConfig: func() error {
			a.logf("自动模式：节点失效，已切换备选并重连")
			return a.applyConfig()
		},
		UpdateGroup: func(gid int64) error {
			return a.updateGroup(gid)
		},
		Changed: func() { a.emitState() },
	}
	return a
}

func (a *App) startup(ctx context.Context) {
	bootlog("startup begin")
	a.ctx = ctx
	bg, cancel := context.WithCancel(context.Background())
	_ = cancel
	a.bal.Run(bg)
	bootlog("balancer running")
	a.logf("程序启动")
}

func (a *App) shutdown(ctx context.Context) {
	a.Disconnect()
}

func (a *App) logf(format string, args ...any) {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	line := time.Now().Format("15:04:05 ") + fmt.Sprintf(format, args...)
	a.logs = append(a.logs, line)
	if len(a.logs) > 300 {
		a.logs = a.logs[len(a.logs)-300:]
	}
}

func (a *App) setError(err error) {
	a.errMu.Lock()
	defer a.errMu.Unlock()
	if err == nil {
		a.lastError = ""
	} else {
		a.lastError = err.Error()
		a.logf("错误：%s", err.Error())
	}
}

func (a *App) emitState() {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "state-changed")
	}
}

// ---------- state ----------

func (a *App) GetState() StateDTO {
	d := a.store.Data
	totals := a.runner.TrafficTotals()

	// Speeds from the proxy line's counters.
	a.trafMu.Lock()
	now := time.Now()
	if prev, ok := a.lastTraf["proxy"]; ok && !a.lastTime.IsZero() {
		cur := totals["proxy"]
		dt := now.Sub(a.lastTime).Seconds()
		if dt > 0 {
			a.speeds = [2]int64{
				int64(float64(cur[0]-prev[0]) / dt),
				int64(float64(cur[1]-prev[1]) / dt),
			}
		}
	}
	a.lastTraf = totals
	a.lastTime = now
	a.trafMu.Unlock()

	st := StateDTO{
		Groups:      []GroupDTO{},
		Nodes:       []NodeDTO{},
		Rules:       []RuleDTO{},
		Running:     a.runner.Running(),
		Mode:        d.Settings.Mode,
		MixedPort:   d.Settings.MixedPort,
		SelectedID:  d.SelectedID,
		AutoGroupID: d.AutoGroupID,
		AutoCC:      d.AutoCC,
		BuiltinPlay: d.BuiltinPlay,
		BuiltinCN:   d.BuiltinCN,
		UpSpeed:     a.speeds[0],
		DownSpeed:   a.speeds[1],
	}
	a.errMu.Lock()
	st.LastError = a.lastError
	a.errMu.Unlock()

	for _, g := range d.Groups {
		st.Groups = append(st.Groups, GroupDTO{
			ID: g.ID, Name: g.Name, URL: g.URL, UpdatedAt: g.UpdatedAt,
			AutoUpdate: g.AutoUpdate, AutoDelayMin: g.AutoDelayMin,
			NodeCount: len(a.store.NodesOfGroup(g.ID)),
		})
	}
	for _, n := range d.Nodes {
		lat, has := d.Latency[n.Fingerprint]
		traf := totals["n-"+n.ID]
		if n.ID == d.SelectedID {
			p := totals["proxy"]
			traf[0] += p[0]
			traf[1] += p[1]
		}
		st.Nodes = append(st.Nodes, NodeDTO{
			ID: n.ID, GroupID: n.GroupID, Name: n.Name, Type: n.Type,
			Region:   d.Regions[n.Fingerprint],
			Latency:  lat, HasLat: has,
			Up: traf[0], Down: traf[1],
			Selected: n.ID == d.SelectedID,
		})
	}
	for _, r := range d.Rules {
		st.Rules = append(st.Rules, RuleDTO(r))
	}
	return st
}

func (a *App) GetLogs() []string {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	out := make([]string, len(a.logs))
	copy(out, a.logs)
	return out
}

// ---------- connection ----------

func (a *App) applyConfig() error {
	if !a.runner.Running() {
		return nil
	}
	cfg, err := core.BuildMain(&a.store.Data)
	if err != nil {
		return err
	}
	if err := a.runner.Restart(cfg); err != nil {
		return err
	}
	return a.applyProxyMode()
}

func (a *App) applyProxyMode() error {
	if a.store.Data.Settings.Mode == "system" {
		return core.SetSystemProxy(a.store.Data.Settings.MixedPort)
	}
	return core.ClearSystemProxy()
}

func (a *App) Connect() string {
	cfg, err := core.BuildMain(&a.store.Data)
	if err != nil {
		a.setError(err)
		return err.Error()
	}
	if err := a.runner.Start(cfg); err != nil {
		a.setError(err)
		return err.Error()
	}
	if err := a.applyProxyMode(); err != nil {
		a.setError(err)
	}
	a.setError(nil)
	a.logf("已连接（%s 模式）", a.store.Data.Settings.Mode)
	a.emitState()
	return ""
}

func (a *App) Disconnect() string {
	a.runner.Stop()
	_ = core.ClearSystemProxy()
	a.logf("已断开")
	a.emitState()
	return ""
}

func (a *App) SelectNode(id string) string {
	if a.store.NodeByID(id) == nil {
		return "node not found"
	}
	a.store.Data.SelectedID = id
	a.store.Save()
	a.logf("已选择节点")
	if err := a.applyConfig(); err != nil {
		a.setError(err)
		return err.Error()
	}
	a.emitState()
	return ""
}

func (a *App) SetMode(mode string) string {
	if mode != "tun" && mode != "system" {
		return "bad mode"
	}
	a.store.Data.Settings.Mode = mode
	a.store.Save()
	if err := a.applyConfig(); err != nil {
		a.setError(err)
		return err.Error()
	}
	a.logf("连接方式：%s", mode)
	a.emitState()
	return ""
}

func (a *App) SetPort(port int) string {
	if port < 1024 || port > 65535 {
		return "bad port"
	}
	a.store.Data.Settings.MixedPort = port
	a.store.Save()
	if err := a.applyConfig(); err != nil {
		a.setError(err)
		return err.Error()
	}
	a.emitState()
	return ""
}

// ---------- auto mode ----------

func (a *App) ArmPool(gid int64, cc string) string {
	a.store.Data.AutoGroupID = gid
	a.store.Data.AutoCC = cc
	a.bal.SteerIntoPool()
	a.store.Save()
	if err := a.applyConfig(); err != nil {
		a.setError(err)
		return err.Error()
	}
	a.logf("自动模式已开启")
	a.emitState()
	return ""
}

func (a *App) DisarmPool() string {
	a.store.Data.AutoGroupID = 0
	a.store.Data.AutoCC = ""
	a.store.Save()
	if err := a.applyConfig(); err != nil {
		a.setError(err)
		return err.Error()
	}
	a.logf("自动模式已关闭")
	a.emitState()
	return ""
}

// ---------- subscriptions ----------

func (a *App) AddGroup(name, rawURL string) string {
	d := a.store.Data
	if strings.TrimSpace(name) == "" {
		name = "订阅"
	}
	g := store.Group{
		ID:           d.NextGroupID,
		Name:         strings.TrimSpace(name),
		URL:          strings.TrimSpace(rawURL),
		AutoUpdate:   true,
		AutoDelayMin: 360,
	}
	d.NextGroupID++
	a.store.Data.Groups = append(a.store.Data.Groups, g)
	a.store.Save()
	if err := a.updateGroup(g.ID); err != nil {
		a.setError(err)
		return err.Error()
	}
	a.emitState()
	return ""
}

func (a *App) updateGroup(gid int64) error {
	g := a.store.GroupByID(gid)
	if g == nil || g.URL == "" {
		return fmt.Errorf("group not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	body, err := subs.Fetch(ctx, g.URL)
	if err != nil {
		return err
	}
	parsed, err := subs.Parse(body)
	if err != nil {
		return err
	}
	var nodes []store.Node
	for _, p := range parsed {
		nodes = append(nodes, p.ToNode(gid))
	}
	added := a.store.ReplaceGroupNodes(gid, nodes)
	g.UpdatedAt = time.Now().Unix()
	a.store.Save()
	a.logf("订阅「%s」已更新：%d 个节点，新增 %d 个", g.Name, len(nodes), len(added))
	a.emitState()
	go a.bal.TestNewNodes(context.Background(), gid, added)
	return nil
}

func (a *App) UpdateGroup(gid int64) string {
	if err := a.updateGroup(gid); err != nil {
		a.setError(err)
		return err.Error()
	}
	a.setError(nil)
	return ""
}

func (a *App) DeleteGroup(gid int64) string {
	d := a.store.Data
	var groups []store.Group
	for _, g := range d.Groups {
		if g.ID != gid {
			groups = append(groups, g)
		}
	}
	d.Groups = groups
	var nodes []store.Node
	for _, n := range d.Nodes {
		if n.GroupID != gid {
			nodes = append(nodes, n)
		}
	}
	d.Nodes = nodes
	if d.AutoGroupID == gid {
		d.AutoGroupID, d.AutoCC = 0, ""
	}
	a.store.Save()
	_ = a.applyConfig()
	a.emitState()
	return ""
}

func (a *App) SetGroupAuto(gid int64, on bool, delayMin int) string {
	g := a.store.GroupByID(gid)
	if g == nil {
		return "group not found"
	}
	g.AutoUpdate = on
	if delayMin > 0 {
		g.AutoDelayMin = delayMin
	}
	a.store.Save()
	a.emitState()
	return ""
}

// ---------- probing ----------

func (a *App) ProbeGroup(gid int64) string {
	go func() {
		nodes := a.store.NodesOfGroup(gid)
		var unknown []store.Node
		for _, n := range nodes {
			if a.store.Data.Regions[n.Fingerprint] == "" {
				unknown = append(unknown, n)
			}
		}
		if len(unknown) > 0 {
			a.bal.SweepRegions(context.Background(), unknown)
		}
		a.bal.TestLatency(context.Background(), a.store.NodesOfGroup(gid))
		a.logf("测速完成")
	}()
	return ""
}

// ProbeNode measures one node's region and latency
// (right-click menu on a node row).
func (a *App) ProbeNode(id string) string {
	go func() {
		n := a.store.NodeByID(id)
		if n == nil {
			return
		}
		one := []store.Node{*n}
		a.bal.SweepRegions(context.Background(), one)
		a.bal.TestLatency(context.Background(), one)
		a.logf("节点测速完成：%s", n.Name)
	}()
	return ""
}

func (a *App) SweepAll() string {
	go func() {
		a.bal.SweepAll(context.Background())
		a.logf("全量地区扫描完成")
	}()
	return ""
}

// ---------- rules ----------

func (a *App) SaveRules(rules []RuleDTO, play, cn bool) string {
	var rs []store.Rule
	for _, r := range rules {
		rs = append(rs, store.Rule(r))
	}
	a.store.Data.Rules = rs
	a.store.Data.BuiltinPlay = play
	a.store.Data.BuiltinCN = cn
	a.store.Save()
	if err := a.applyConfig(); err != nil {
		a.setError(err)
		return err.Error()
	}
	a.emitState()
	return ""
}
