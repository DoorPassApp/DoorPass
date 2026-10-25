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
	"bytes"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.zx2c4.com/wireguard/tun"
)

// fakeDev is a tun.Device fed from a channel.
type fakeDev struct {
	in     chan []byte
	closed chan struct{}
	once   sync.Once
}

func newFakeDev() *fakeDev { return &fakeDev{in: make(chan []byte, 8), closed: make(chan struct{})} }

func (d *fakeDev) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	select {
	case p := <-d.in:
		sizes[0] = copy(bufs[0][offset:], p)
		return 1, nil
	case <-d.closed:
		return 0, os.ErrClosed
	}
}
func (d *fakeDev) Write(bufs [][]byte, offset int) (int, error) { return len(bufs), nil }
func (d *fakeDev) File() *os.File                               { return nil }
func (d *fakeDev) MTU() (int, error)                            { return tunMTU, nil }
func (d *fakeDev) Name() (string, error)                        { return "fake", nil }
func (d *fakeDev) Events() <-chan tun.Event                     { return nil }
func (d *fakeDev) BatchSize() int                               { return 1 }
func (d *fakeDev) Close() error                                 { d.once.Do(func() { close(d.closed) }); return nil }

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// Packets go to the attached session; a stale session's detach must not cut
// off the session that replaced it; the device survives sessions.
func TestPumpHandsPacketsToCurrentSession(t *testing.T) {
	dev := newFakeDev()
	p := newPump(dev, "fake")
	defer p.close()

	var first, second syncBuf
	set1, detach1 := p.attach()
	set1(&first)
	dev.in <- []byte("a")
	require.Eventually(t, func() bool { return first.String() == "a" }, time.Second, 5*time.Millisecond)

	set2, detach2 := p.attach()
	set2(&second)
	detach1.Close() // Old session tearing down late.
	dev.in <- []byte("b")
	require.Eventually(t, func() bool { return second.String() == "b" }, time.Second, 5*time.Millisecond)
	require.Equal(t, "a", first.String())

	detach2.Close()
	dev.in <- []byte("c") // Dropped: no session.
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, "b", second.String())
}
