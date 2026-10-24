// Copyright 2026 The DoorPass Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.getoutline.org/sdk/network"

	"localhost/Intra/Android/app/src/go/doh"
	"localhost/Intra/Android/app/src/go/intra"
	"localhost/Intra/Android/app/src/go/intra/protect"
)

// Same network layout as the Android app (GoVpnAdapter.java).
const (
	tunGatewayIP = "10.111.222.1"
	tunRouterIP  = "10.111.222.2"
	fakeDNSIP    = "10.111.222.3"
	tunSubnet    = "10.111.222.0/24"
)

// Routing modes.
const (
	// modeFull routes all IPv4 traffic through the tunnel, like Intra on
	// Android: DNS goes over DoH and HTTPS connections get TLS splitting.
	modeFull = "full"
	// modeDNS routes only the fake DNS address through the tunnel.
	modeDNS = "dns"
)

// protector keeps the helper's own sockets off the tunnel by binding them to
// the physical interface, and remembers the network's plain DNS servers for
// bootstrapping DoH hostnames. It implements protect.Protector.
type protector struct {
	ifindex   atomic.Int32
	ifname    atomic.Value // string
	gateway   atomic.Value // string
	resolvers atomic.Value // string, comma separated
	exclude   string       // Name of our own TUN interface.

	// onUplink, if set, is called when the interface or gateway changes.
	onUplink func(name, gateway string)
}

func (p *protector) Protect(fd int32) bool {
	idx := int(p.ifindex.Load())
	if idx == 0 {
		return true
	}
	if err := bindToInterface(uintptr(fd), idx); err != nil {
		slog.Warn("protect: bind failed", "ifindex", idx, "err", err)
		return false
	}
	return true
}

func (p *protector) GetResolvers() string {
	r, _ := p.resolvers.Load().(string)
	return r
}

// refresh re-detects the physical default interface and its DNS servers.
// It keeps the previous values if detection fails, e.g. while Wi-Fi switches.
func (p *protector) refresh() {
	name, idx, gw, err := defaultInterface(p.exclude)
	if err != nil {
		slog.Debug("protect: no default interface", "err", err)
		return
	}
	old, _ := p.ifname.Load().(string)
	oldGW, _ := p.gateway.Load().(string)
	if old != "" && old != name {
		slog.Info("protect: default interface changed", "from", old, "to", name, "index", idx)
	}
	p.ifname.Store(name)
	p.gateway.Store(gw)
	p.ifindex.Store(int32(idx))
	if p.onUplink != nil && (old != name || oldGW != gw) {
		p.onUplink(name, gw)
	}
	if rs := filterResolvers(interfaceResolvers(name, idx)); len(rs) > 0 {
		p.resolvers.Store(strings.Join(rs, ","))
	} else if p.GetResolvers() == "" {
		p.resolvers.Store("1.1.1.1,8.8.8.8")
	}
}

type session struct {
	url   string
	ips   string
	mode  string
	since time.Time

	dev    *tunIO
	ifname string
	tunnel *intra.Tunnel
	prot   *protector
	undo   func() error
	stop   chan struct{}

	pinMu  sync.Mutex
	unpin  func()
	closed bool
}

// pin keeps a default route scoped to the uplink (Full mode only).
func (s *session) pin(name, gw string) {
	s.pinMu.Lock()
	defer s.pinMu.Unlock()
	if s.closed {
		return
	}
	if s.unpin != nil {
		s.unpin()
	}
	s.unpin = pinUplink(name, gw)
}

func (s *session) unpinAll() {
	s.pinMu.Lock()
	defer s.pinMu.Unlock()
	s.closed = true
	if s.unpin != nil {
		s.unpin()
		s.unpin = nil
	}
}

// manager owns at most one session.
type manager struct {
	mu      sync.Mutex
	cur     *session
	hub     *hub
	lastErr string
}

func newManager() *manager {
	return &manager{hub: newHub()}
}

// Status is reported by GET /status.
type Status struct {
	Version   string `json:"version"`
	Connected bool   `json:"connected"`
	URL       string `json:"url,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Since     int64  `json:"since,omitempty"`
	Interface string `json:"interface,omitempty"`
	Uplink    string `json:"uplink,omitempty"`
	LastError string `json:"lastError,omitempty"`
	QueryLog  bool   `json:"queryLog"`
	Stats     Stats  `json:"stats"`
}

func (m *manager) status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{Version: version, LastError: m.lastErr, QueryLog: m.hub.logQueries.Load(), Stats: m.hub.stats()}
	if s := m.cur; s != nil {
		st.Connected = true
		st.URL = s.url
		st.Mode = s.mode
		st.Since = s.since.UnixMilli()
		st.Interface = s.ifname
		st.Uplink, _ = s.prot.ifname.Load().(string)
	}
	return st
}

func splitIPs(ips string) []string {
	if ips == "" {
		return nil
	}
	return strings.Split(ips, ",")
}

// connect starts a session, or switches the DoH server of the running one.
// A mode change needs a reconnect because the routes differ.
func (m *manager) connect(url, ips, mode string) error {
	if mode != modeDNS {
		mode = modeFull
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if s := m.cur; s != nil && s.mode == mode {
		r, err := doh.NewResolver(url, splitIPs(ips), dialerFor(s.prot), nil, m.hub)
		if err != nil {
			m.lastErr = err.Error()
			return err
		}
		s.tunnel.SetDNS(r)
		s.url, s.ips = url, ips
		m.lastErr = ""
		slog.Info("switched DoH server", "url", url)
		return nil
	}
	if m.cur != nil {
		m.disconnectLocked()
	}

	s, err := m.start(url, ips, mode)
	if err != nil {
		m.lastErr = err.Error()
		return err
	}
	m.cur = s
	m.lastErr = ""
	return nil
}

func (m *manager) start(url, ips, mode string) (s *session, err error) {
	begin := time.Now()
	dev, ifname, err := openTun()
	if err != nil {
		return nil, fmt.Errorf("create TUN interface: %w", err)
	}
	s = &session{url: url, ips: ips, mode: mode, since: time.Now(), dev: newTunIO(dev), ifname: ifname, stop: make(chan struct{})}
	defer func() {
		if err == nil {
			return
		}
		s.unpinAll()
		if s.tunnel != nil {
			s.tunnel.Disconnect() // Also closes the TUN device.
		} else {
			s.dev.Close()
		}
	}()

	// Detect the uplink before our routes exist.
	s.prot = &protector{exclude: ifname}
	if mode == modeFull {
		s.prot.onUplink = s.pin
	}
	s.prot.refresh()

	tunReady := time.Since(begin)
	r, err := doh.NewResolver(url, splitIPs(ips), dialerFor(s.prot), nil, m.hub)
	if err != nil {
		return nil, fmt.Errorf("DoH server: %w", err)
	}
	resolverReady := time.Since(begin)
	m.hub.reset()
	s.tunnel, err = intra.NewTunnel(fakeDNSIP+":53", r, s.dev, s.prot, m.hub)
	if err != nil {
		return nil, fmt.Errorf("start tunnel: %w", err)
	}
	go relay(s.tunnel, s.dev)
	go relay(s.dev, s.tunnel)

	tunnelReady := time.Since(begin)
	if s.undo, err = configureNetwork(ifname, mode); err != nil {
		return nil, fmt.Errorf("configure network: %w", err)
	}

	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.prot.refresh()
			}
		}
	}()
	slog.Info("connected", "url", url, "mode", mode, "tun", ifname,
		"uplink", s.prot.ifname.Load(), "took", time.Since(begin).Round(time.Millisecond),
		"tun_ready", tunReady.Round(time.Millisecond), "resolver_ready", resolverReady.Round(time.Millisecond),
		"tunnel_ready", tunnelReady.Round(time.Millisecond))
	return s, nil
}

func (m *manager) disconnect() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.disconnectLocked()
}

func (m *manager) disconnectLocked() {
	s := m.cur
	if s == nil {
		return
	}
	m.cur = nil
	close(s.stop)
	if err := s.undo(); err != nil {
		slog.Warn("restore network", "err", err)
	}
	s.unpinAll()
	s.tunnel.Disconnect()
	slog.Info("disconnected")
}

// probe checks that a DoH server answers, like backend.Probe, without
// touching the network setup.
func (m *manager) probe(url, ips string) error {
	p := &protector{}
	m.mu.Lock()
	if m.cur != nil {
		p = m.cur.prot
	}
	m.mu.Unlock()
	if p.GetResolvers() == "" {
		p.refresh()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	errc := make(chan error, 1)
	go func() {
		// NewResolver's hostname lookup takes no context, so the whole probe
		// runs here and the caller gives up at the deadline.
		r, err := doh.NewResolver(url, splitIPs(ips), dialerFor(p), nil, nil)
		if err != nil {
			errc <- err
			return
		}
		resp, err := r.Query(ctx, probeQuery)
		if err == nil && len(resp) == 0 {
			err = errors.New("empty DoH response")
		}
		errc <- err
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		return errors.New("no answer from the server within 10 seconds")
	}
}

// probeQuery is backend.dohQuery: an A query for youtube.com.
var probeQuery = []byte{
	0, 0, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0,
	7, 'y', 'o', 'u', 't', 'u', 'b', 'e', 3, 'c', 'o', 'm', 0,
	0, 1, 0, 1,
}

// relay is backend.copyUntilEOF: lwIP and the TUN device both deliver one
// packet per Read.
func relay(dst, src io.ReadWriteCloser) {
	buf := make([]byte, tunMTU)
	for {
		_, err := io.CopyBuffer(dst, src, buf)
		if err == nil || errors.Is(err, os.ErrClosed) || errors.Is(err, fs.ErrClosed) || errors.Is(err, network.ErrClosed) {
			return
		}
		time.Sleep(10 * time.Millisecond) // Don't spin on a persistent error.
	}
}

func dialerFor(p *protector) *net.Dialer {
	return protect.MakeDialer(p)
}

func ipString(b []byte) string {
	return net.IP(b).String()
}
