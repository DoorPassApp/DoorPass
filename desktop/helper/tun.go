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
	"sync"

	"golang.zx2c4.com/wireguard/tun"
)

const (
	tunMTU = 1500
	// Headroom in front of each packet. macOS utun prepends a 4-byte address
	// family header, which wireguard's tun package writes into this space.
	tunOffset  = 16
	tunBufSize = tunOffset + 1<<16
)

// tunIO adapts a wireguard tun.Device (batched, offset-based) to the plain
// one-packet-per-call io.ReadWriteCloser that intra.Tunnel expects, the same
// shape as the Android TUN file descriptor.
type tunIO struct {
	dev tun.Device

	rbufs [][]byte
	sizes []int

	wmu   sync.Mutex
	wbuf  []byte
	wbufs [][]byte
}

func newTunIO(dev tun.Device) *tunIO {
	return &tunIO{
		dev:   dev,
		rbufs: [][]byte{make([]byte, tunBufSize)},
		sizes: []int{0},
		wbuf:  make([]byte, tunBufSize),
		wbufs: make([][]byte, 1),
	}
}

// Read must only be called from one goroutine at a time.
func (t *tunIO) Read(p []byte) (int, error) {
	for {
		n, err := t.dev.Read(t.rbufs, t.sizes, tunOffset)
		if n == 1 {
			return copy(p, t.rbufs[0][tunOffset:tunOffset+t.sizes[0]]), nil
		}
		if err != nil {
			return 0, err
		}
	}
}

func (t *tunIO) Write(p []byte) (int, error) {
	if len(p) > tunBufSize-tunOffset {
		return 0, errors.New("packet too large")
	}
	t.wmu.Lock()
	defer t.wmu.Unlock()
	n := copy(t.wbuf[tunOffset:], p)
	t.wbufs[0] = t.wbuf[:tunOffset+n]
	if _, err := t.dev.Write(t.wbufs, tunOffset); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (t *tunIO) Close() error {
	return t.dev.Close()
}
