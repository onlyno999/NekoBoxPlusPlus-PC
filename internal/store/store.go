// Package store holds the whole app state in one JSON file
// that lives next to the executable, so the portable build
// never writes outside its own folder.
package store

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Node struct {
	ID          string          `json:"id"`
	GroupID     int64           `json:"groupId"`
	Name        string          `json:"name"`
	Type        string          `json:"type"`
	Fingerprint string          `json:"fp"`
	Outbound    json.RawMessage `json:"outbound"`
}

type Group struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	URL          string `json:"url"`
	UpdatedAt    int64  `json:"updatedAt"`
	AutoUpdate   bool   `json:"autoUpdate"`
	AutoDelayMin int    `json:"autoDelayMin"`
}

// Rule outbound kinds: "proxy", "direct", "block", "pool".
type Rule struct {
	Name        string `json:"name"`
	Content     string `json:"content"` // comma separated domains
	Outbound    string `json:"outbound"`
	PoolGroupID int64  `json:"poolGroupId,omitempty"`
	PoolCC      string `json:"poolCc,omitempty"`
	Enabled     bool   `json:"enabled"`
}

type Settings struct {
	Mode      string `json:"mode"` // "tun" | "system"
	MixedPort int    `json:"mixedPort"`
}

type Data struct {
	Groups  []Group `json:"groups"`
	Nodes   []Node  `json:"nodes"`
	Regions map[string]string `json:"regions"` // fingerprint -> CC
	Latency map[string]int    `json:"latency"` // fingerprint -> ms, -1 = fail
	Traffic map[string][2]int64 `json:"traffic"` // fingerprint -> [up, down]

	SelectedID string `json:"selectedId"`

	// Armed auto-mode pool; AutoGroupID == 0 means none.
	AutoGroupID int64  `json:"autoGroupId"`
	AutoCC      string `json:"autoCc"`

	Rules       []Rule `json:"rules"`
	BuiltinPlay bool   `json:"builtinPlay"`
	BuiltinCN   bool   `json:"builtinCn"`

	Settings Settings `json:"settings"`

	NextGroupID int64 `json:"nextGroupId"`
}

func Fingerprint(typ, server string, port int) string {
	return typ + "|" + server + "|" + itoa(port)
}

func IDOf(fp string) string {
	sum := sha1.Sum([]byte(fp))
	return hex.EncodeToString(sum[:])[:12]
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [12]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}

type Store struct {
	mu   sync.Mutex
	path string
	Data Data
}

func Dir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func Open() *Store {
	s := &Store{path: filepath.Join(Dir(), "data.json")}
	s.Data = Data{
		Regions:     map[string]string{},
		Latency:     map[string]int{},
		Traffic:     map[string][2]int64{},
		BuiltinPlay: true,
		BuiltinCN:   true,
		NextGroupID: 1,
	}
	s.Data.Settings = Settings{Mode: "tun", MixedPort: 2080}
	if raw, err := os.ReadFile(s.path); err == nil {
		var d Data
		if json.Unmarshal(raw, &d) == nil {
			s.Data = d
			if s.Data.Regions == nil {
				s.Data.Regions = map[string]string{}
			}
			if s.Data.Latency == nil {
				s.Data.Latency = map[string]int{}
			}
			if s.Data.Traffic == nil {
				s.Data.Traffic = map[string][2]int64{}
			}
		}
	}
	return s
}

func (s *Store) Save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.MarshalIndent(s.Data, "", " ")
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) == nil {
		_ = os.Rename(tmp, s.path)
	}
}

func (d *Data) NodeByID(id string) *Node {
	for i := range d.Nodes {
		if d.Nodes[i].ID == id {
			return &d.Nodes[i]
		}
	}
	return nil
}

func (s *Store) GroupByID(id int64) *Group {
	for i := range s.Data.Groups {
		if s.Data.Groups[i].ID == id {
			return &s.Data.Groups[i]
		}
	}
	return nil
}

func (s *Store) NodeByID(id string) *Node {
	for i := range s.Data.Nodes {
		if s.Data.Nodes[i].ID == id {
			return &s.Data.Nodes[i]
		}
	}
	return nil
}

func (s *Store) NodesOfGroup(id int64) []Node {
	var out []Node
	for _, n := range s.Data.Nodes {
		if n.GroupID == id {
			out = append(out, n)
		}
	}
	return out
}

// ReplaceGroupNodes swaps a group's node set after a
// subscription update and returns the fingerprints that are
// new compared to the previous set.
func (s *Store) ReplaceGroupNodes(groupID int64, fresh []Node) (added []string) {
	old := map[string]bool{}
	var kept []Node
	for _, n := range s.Data.Nodes {
		if n.GroupID == groupID {
			old[n.Fingerprint] = true
		} else {
			kept = append(kept, n)
		}
	}
	for _, n := range fresh {
		if !old[n.Fingerprint] {
			added = append(added, n.Fingerprint)
		}
		kept = append(kept, n)
	}
	s.Data.Nodes = kept
	return added
}
