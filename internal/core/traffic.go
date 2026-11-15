package core

import (
	"context"
	"net"
	"sync/atomic"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/bufio"
	N "github.com/sagernet/sing/common/network"
)

// trafficTracker counts bytes per outbound tag. It also
// implements the fork's OutboundTrafficTracker extension
// so traffic carried by a pool member is attributed to
// the member, not just to the pool.
type trafficTracker struct {
	totals map[string]*[2]atomic.Int64
}

func newTrafficTracker() *trafficTracker {
	return &trafficTracker{totals: map[string]*[2]atomic.Int64{}}
}

func (t *trafficTracker) counters(tag string) *[2]atomic.Int64 {
	if c, ok := t.totals[tag]; ok {
		return c
	}
	c := new([2]atomic.Int64)
	t.totals[tag] = c
	return c
}

func (t *trafficTracker) RoutedConnection(
	ctx context.Context, conn net.Conn, metadata adapter.InboundContext,
	matchedRule adapter.Rule, matchOutbound adapter.Outbound,
) net.Conn {
	if matchOutbound == nil {
		return conn
	}
	return t.wrap(conn, matchOutbound.Tag())
}

func (t *trafficTracker) RoutedPacketConnection(
	ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext,
	matchedRule adapter.Rule, matchOutbound adapter.Outbound,
) N.PacketConn {
	if matchOutbound == nil {
		return conn
	}
	return t.wrapPacket(conn, matchOutbound.Tag())
}

func (t *trafficTracker) TrackOutboundConnection(conn net.Conn, outboundTag string) net.Conn {
	return t.wrap(conn, outboundTag)
}

func (t *trafficTracker) TrackOutboundPacketConnection(conn N.PacketConn, outboundTag string) N.PacketConn {
	return t.wrapPacket(conn, outboundTag)
}

func (t *trafficTracker) wrap(conn net.Conn, tag string) net.Conn {
	c := t.counters(tag)
	return &countConn{Conn: conn, up: &c[0], down: &c[1]}
}

func (t *trafficTracker) wrapPacket(conn N.PacketConn, tag string) N.PacketConn {
	c := t.counters(tag)
	return bufio.NewCounterPacketConn(conn,
		[]N.CountFunc{func(n int64) { c[1].Add(n) }},
		[]N.CountFunc{func(n int64) { c[0].Add(n) }})
}

func (t *trafficTracker) Snapshot() map[string][2]int64 {
	out := make(map[string][2]int64, len(t.totals))
	for tag, c := range t.totals {
		out[tag] = [2]int64{c[0].Load(), c[1].Load()}
	}
	return out
}

type countConn struct {
	net.Conn
	up, down *atomic.Int64
}

func (c *countConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.down.Add(int64(n))
	}
	return n, err
}

func (c *countConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if n > 0 {
		c.up.Add(int64(n))
	}
	return n, err
}

func (c *countConn) Upstream() any { return c.Conn }
