package core

import (
	"context"
	"fmt"
	"sync"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
)

// Runner owns the one embedded core instance.
type Runner struct {
	mu      sync.Mutex
	box     *box.Box
	tracker *trafficTracker
	running bool
}

func newInstance(raw []byte) (*box.Box, *trafficTracker, error) {
	ctx := box.Context(
		context.Background(),
		include.InboundRegistry(),
		include.OutboundRegistry(),
		include.EndpointRegistry(),
		include.DNSTransportRegistry(),
		include.ServiceRegistry(),
	)
	options, err := json.UnmarshalExtendedContext[option.Options](ctx, raw)
	if err != nil {
		return nil, nil, fmt.Errorf("parse config: %w", err)
	}
	instance, err := box.New(box.Options{Context: ctx, Options: options})
	if err != nil {
		return nil, nil, fmt.Errorf("create core: %w", err)
	}
	tracker := newTrafficTracker()
	instance.Router().AppendTracker(tracker)
	if err := instance.Start(); err != nil {
		return nil, nil, fmt.Errorf("start core: %w", err)
	}
	return instance, tracker, nil
}

func (r *Runner) Start(raw []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return fmt.Errorf("already running")
	}
	instance, tracker, err := newInstance(raw)
	if err != nil {
		return err
	}
	r.box = instance
	r.tracker = tracker
	r.running = true
	return nil
}

func (r *Runner) Restart(raw []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.box != nil {
		_ = r.box.Close()
		r.box = nil
		r.running = false
	}
	instance, tracker, err := newInstance(raw)
	if err != nil {
		return err
	}
	r.box = instance
	r.tracker = tracker
	r.running = true
	return nil
}

func (r *Runner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.box != nil {
		_ = r.box.Close()
		r.box = nil
	}
	r.running = false
}

func (r *Runner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// StartProbe starts a short-lived standalone instance
// (region probe / latency test) and returns its stopper.
func StartProbe(raw []byte) (func(), error) {
	instance, _, err := newInstance(raw)
	if err != nil {
		return nil, err
	}
	return func() { _ = instance.Close() }, nil
}

// TrafficTotals returns per-outbound-tag byte counters
// ("n-<nodeId>" for pool members, "proxy" for the main
// line, pool tags for pool-level traffic).
func (r *Runner) TrafficTotals() map[string][2]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tracker == nil {
		return map[string][2]int64{}
	}
	return r.tracker.Snapshot()
}
