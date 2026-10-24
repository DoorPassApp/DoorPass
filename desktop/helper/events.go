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
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"localhost/Intra/Android/app/src/go/doh"
	"localhost/Intra/Android/app/src/go/intra"
)

// QueryEvent is one DoH transaction, as shown in the app's query list.
type QueryEvent struct {
	Time      int64    `json:"time"` // Unix milliseconds
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Status    string   `json:"status"`
	RCode     string   `json:"rcode,omitempty"`
	LatencyMs int64    `json:"latencyMs"`
	Server    string   `json:"server,omitempty"`
	Answers   []string `json:"answers,omitempty"`
}

// Stats are counters for the current session.
type Stats struct {
	Queries      int64 `json:"queries"`
	Failures     int64 `json:"failures"`
	AvgLatencyMs int64 `json:"avgLatencyMs"`
	TCPConns     int64 `json:"tcpConns"`
	SplitRetries int64 `json:"splitRetries"`
	BytesUp      int64 `json:"bytesUp"`
	BytesDown    int64 `json:"bytesDown"`
}

var statusNames = map[int]string{
	doh.Complete:      "ok",
	doh.SendFailed:    "send-failed",
	doh.HTTPError:     "http-error",
	doh.BadQuery:      "bad-query",
	doh.BadResponse:   "bad-response",
	doh.InternalError: "internal-error",
}

// hub collects DoH and socket events from the tunnel and keeps counters.
// Only while the query log is on does it also keep the most recent queries
// (domain names) and fan them out to subscribers; it is off by default.
type hub struct {
	logQueries atomic.Bool

	queries, failures, latencySum atomic.Int64
	tcpConns, splitRetries        atomic.Int64
	bytesUp, bytesDown            atomic.Int64

	mu     sync.Mutex
	recent [][]byte
	subs   map[chan []byte]struct{}
}

const recentEvents = 200

var _ doh.Listener = (*hub)(nil)
var _ intra.Listener = (*hub)(nil)

func newHub() *hub {
	return &hub{subs: map[chan []byte]struct{}{}}
}

func (h *hub) reset() {
	for _, c := range []*atomic.Int64{&h.queries, &h.failures, &h.latencySum, &h.tcpConns, &h.splitRetries, &h.bytesUp, &h.bytesDown} {
		c.Store(0)
	}
	h.mu.Lock()
	h.recent = nil
	h.mu.Unlock()
}

func (h *hub) stats() Stats {
	s := Stats{
		Queries:      h.queries.Load(),
		Failures:     h.failures.Load(),
		TCPConns:     h.tcpConns.Load(),
		SplitRetries: h.splitRetries.Load(),
		BytesUp:      h.bytesUp.Load(),
		BytesDown:    h.bytesDown.Load(),
	}
	if s.Queries > 0 {
		s.AvgLatencyMs = h.latencySum.Load() / s.Queries
	}
	return s
}

func (h *hub) OnQuery(url string) doh.Token { return nil }

// setQueryLog turns the query log on or off; turning it off forgets the
// queries recorded so far.
func (h *hub) setQueryLog(on bool) {
	h.logQueries.Store(on)
	if !on {
		h.mu.Lock()
		h.recent = nil
		h.mu.Unlock()
	}
}

func (h *hub) OnResponse(_ doh.Token, s *doh.Summary) {
	h.queries.Add(1)
	h.latencySum.Add(int64(s.Latency * 1000))
	if s.Status != doh.Complete {
		h.failures.Add(1)
	}
	if !h.logQueries.Load() {
		return
	}
	b, err := json.Marshal(summarize(s))
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recent = append(h.recent, b)
	if len(h.recent) > recentEvents {
		h.recent = h.recent[len(h.recent)-recentEvents:]
	}
	for c := range h.subs {
		select {
		case c <- b:
		default: // Slow subscriber; drop rather than block the tunnel.
		}
	}
}

func (h *hub) OnTCPSocketClosed(s *intra.TCPSocketSummary) {
	h.tcpConns.Add(1)
	h.bytesUp.Add(s.UploadBytes)
	h.bytesDown.Add(s.DownloadBytes)
	if s.Retry != nil && s.Retry.Split > 0 {
		h.splitRetries.Add(1)
	}
}

func (h *hub) OnUDPSocketClosed(s *intra.UDPSocketSummary) {
	h.bytesUp.Add(s.UploadBytes)
	h.bytesDown.Add(s.DownloadBytes)
}

// subscribe returns the recent events and a channel for new ones.
func (h *hub) subscribe() ([][]byte, chan []byte) {
	c := make(chan []byte, 64)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subs[c] = struct{}{}
	return append([][]byte(nil), h.recent...), c
}

func (h *hub) unsubscribe(c chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, c)
}

func summarize(s *doh.Summary) QueryEvent {
	ev := QueryEvent{
		Time:      time.Now().UnixMilli(),
		Status:    statusNames[s.Status],
		LatencyMs: int64(s.Latency * 1000),
		Server:    s.Server,
	}
	var p dnsmessage.Parser
	if _, err := p.Start(s.Query); err == nil {
		if q, err := p.Question(); err == nil {
			ev.Name = strings.TrimSuffix(q.Name.String(), ".")
			ev.Type = strings.TrimPrefix(q.Type.String(), "Type")
		}
	}
	if len(s.Response) == 0 {
		return ev
	}
	h, err := p.Start(s.Response)
	if err != nil {
		return ev
	}
	ev.RCode = strings.TrimPrefix(h.RCode.String(), "RCode")
	if err := p.SkipAllQuestions(); err != nil {
		return ev
	}
	for {
		rh, err := p.AnswerHeader()
		if err != nil {
			break
		}
		switch rh.Type {
		case dnsmessage.TypeA:
			if r, err := p.AResource(); err == nil {
				ev.Answers = append(ev.Answers, ipString(r.A[:]))
			}
		case dnsmessage.TypeAAAA:
			if r, err := p.AAAAResource(); err == nil {
				ev.Answers = append(ev.Answers, ipString(r.AAAA[:]))
			}
		case dnsmessage.TypeCNAME:
			if r, err := p.CNAMEResource(); err == nil {
				ev.Answers = append(ev.Answers, strings.TrimSuffix(r.CNAME.String(), "."))
			}
		default:
			if err := p.SkipAnswer(); err != nil {
				return ev
			}
		}
	}
	return ev
}
