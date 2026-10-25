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
	"errors"
	"io"
	"io/fs"
	"os"
	"sync/atomic"
	"time"

	"golang.getoutline.org/sdk/network"
	"golang.zx2c4.com/wireguard/tun"
)

// pump owns the TUN device, which can outlive a session (see keepTun): one
// goroutine reads packets from it and hands them to the current session's
// tunnel, and drops them while there is none.
type pump struct {
	dev  *tunIO
	name string
	sink atomic.Pointer[io.Writer]
}

func newPump(dev tun.Device, name string) *pump {
	p := &pump{dev: newTunIO(dev), name: name}
	go p.run()
	return p
}

func (p *pump) run() {
	buf := make([]byte, tunBufSize)
	for {
		n, err := p.dev.Read(buf)
		if err != nil {
			if isClosed(err) {
				return
			}
			time.Sleep(10 * time.Millisecond) // Don't spin on a persistent error.
			continue
		}
		if w := p.sink.Load(); w != nil {
			(*w).Write(buf[:n])
		}
	}
}

// attach makes w receive the device's packets; the returned closer detaches
// it again (and only it), so it can be handed to the session's tunnel.
func (p *pump) attach() (set func(io.Writer), detach io.Closer) {
	var mine *io.Writer
	set = func(w io.Writer) {
		mine = &w
		p.sink.Store(mine)
	}
	detach = closerFunc(func() error {
		p.sink.CompareAndSwap(mine, nil)
		return nil
	})
	return set, detach
}

func (p *pump) close() error {
	p.sink.Store(nil)
	return p.dev.Close()
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

func isClosed(err error) bool {
	return errors.Is(err, os.ErrClosed) || errors.Is(err, fs.ErrClosed) || errors.Is(err, network.ErrClosed)
}
