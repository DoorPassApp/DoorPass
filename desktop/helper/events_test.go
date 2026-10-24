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
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/dns/dnsmessage"

	"localhost/Intra/Android/app/src/go/doh"
	"localhost/Intra/Android/app/src/go/intra"
	"localhost/Intra/Android/app/src/go/intra/split"
)

func buildMessage(t *testing.T, response bool) []byte {
	name := dnsmessage.MustNewName("example.com.")
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 1, Response: response, RecursionDesired: true})
	require.NoError(t, b.StartQuestions())
	require.NoError(t, b.Question(dnsmessage.Question{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}))
	if response {
		require.NoError(t, b.StartAnswers())
		hdr := dnsmessage.ResourceHeader{Name: name, Class: dnsmessage.ClassINET, TTL: 60}
		require.NoError(t, b.AResource(hdr, dnsmessage.AResource{A: [4]byte{93, 184, 215, 14}}))
	}
	msg, err := b.Finish()
	require.NoError(t, err)
	return msg
}

func TestSummarize(t *testing.T) {
	ev := summarize(&doh.Summary{
		Latency:  0.042,
		Query:    buildMessage(t, false),
		Response: buildMessage(t, true),
		Server:   "8.8.8.8",
		Status:   doh.Complete,
	})
	require.Equal(t, "example.com", ev.Name)
	require.Equal(t, "A", ev.Type)
	require.Equal(t, "ok", ev.Status)
	require.Equal(t, "Success", ev.RCode)
	require.Equal(t, int64(42), ev.LatencyMs)
	require.Equal(t, []string{"93.184.215.14"}, ev.Answers)
}

func TestSummarizeFailure(t *testing.T) {
	ev := summarize(&doh.Summary{Query: buildMessage(t, false), Status: doh.SendFailed})
	require.Equal(t, "send-failed", ev.Status)
	require.Empty(t, ev.Answers)
}

func TestHubQueryLogOffByDefault(t *testing.T) {
	h := newHub()
	_, c := h.subscribe()
	defer h.unsubscribe(c)

	h.OnResponse(nil, &doh.Summary{Latency: 0.010, Query: buildMessage(t, false), Status: doh.Complete})
	require.Equal(t, int64(1), h.stats().Queries)
	require.Empty(t, c)
	recent, c2 := h.subscribe()
	h.unsubscribe(c2)
	require.Empty(t, recent)
}

func TestHubStatsAndSubscribe(t *testing.T) {
	h := newHub()
	h.setQueryLog(true)
	_, c := h.subscribe()
	defer h.unsubscribe(c)

	h.OnResponse(nil, &doh.Summary{Latency: 0.010, Query: buildMessage(t, false), Status: doh.Complete})
	h.OnResponse(nil, &doh.Summary{Latency: 0.030, Query: buildMessage(t, false), Status: doh.HTTPError})
	h.OnTCPSocketClosed(&intra.TCPSocketSummary{UploadBytes: 5, DownloadBytes: 7, Retry: &split.RetryStats{Split: 3}})
	h.OnUDPSocketClosed(&intra.UDPSocketSummary{UploadBytes: 1, DownloadBytes: 2})

	s := h.stats()
	require.Equal(t, Stats{Queries: 2, Failures: 1, AvgLatencyMs: 20, TCPConns: 1, SplitRetries: 1, BytesUp: 6, BytesDown: 9}, s)

	var ev QueryEvent
	require.NoError(t, json.Unmarshal(<-c, &ev))
	require.Equal(t, "example.com", ev.Name)

	recent, c2 := h.subscribe()
	h.unsubscribe(c2)
	require.Len(t, recent, 2)

	h.setQueryLog(false)
	recent, c3 := h.subscribe()
	h.unsubscribe(c3)
	require.Empty(t, recent)

	h.reset()
	require.Equal(t, Stats{}, h.stats())
}
