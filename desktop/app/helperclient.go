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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Client for doorpass-helper's local API (see desktop/helper/api.go).

const helperURL = "http://127.0.0.1:47121"

// HelperStatus is the helper's GET /status.
type HelperStatus struct {
	Version   string          `json:"version"`
	Connected bool            `json:"connected"`
	URL       string          `json:"url,omitempty"`
	Mode      string          `json:"mode,omitempty"`
	Since     int64           `json:"since,omitempty"`
	Interface string          `json:"interface,omitempty"`
	Uplink    string          `json:"uplink,omitempty"`
	LastError string          `json:"lastError,omitempty"`
	QueryLog  bool            `json:"queryLog"`
	Stats     json.RawMessage `json:"stats,omitempty"`
}

// HelperError carries the HTTP status so callers can tell "not installed"
// from "installed by another copy" (401).
type HelperError struct {
	Status int
	Msg    string
}

func (e *HelperError) Error() string { return e.Msg }

type helperClient struct {
	token string
	http  *http.Client

	mu     sync.Mutex
	cancel context.CancelFunc
}

func newHelperClient(token string) *helperClient {
	return &helperClient{token: token, http: &http.Client{}}
}

func (c *helperClient) do(method, path string, body any, timeout time.Duration, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, helperURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(data))
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return &HelperError{Status: resp.StatusCode, Msg: msg}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *helperClient) status() (HelperStatus, error) {
	var st HelperStatus
	return st, c.do("GET", "/status", nil, 3*time.Second, &st)
}

func (c *helperClient) connect(url, ips, mode string) (HelperStatus, error) {
	var st HelperStatus
	return st, c.do("POST", "/connect", map[string]string{"url": url, "ips": ips, "mode": mode}, 15*time.Second, &st)
}

func (c *helperClient) disconnect() (HelperStatus, error) {
	var st HelperStatus
	return st, c.do("POST", "/disconnect", nil, 15*time.Second, &st)
}

func (c *helperClient) setQueryLog(on bool) (HelperStatus, error) {
	var st HelperStatus
	return st, c.do("POST", "/querylog", map[string]bool{"enabled": on}, 3*time.Second, &st)
}

// ProbeResult is the helper's POST /probe answer.
type ProbeResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (c *helperClient) probe(url, ips string) (ProbeResult, error) {
	var r ProbeResult
	return r, c.do("POST", "/probe", map[string]string{"url": url, "ips": ips}, 15*time.Second, &r)
}

// startEvents streams query events to onEvent until stopEvents, reconnecting
// after errors. The helper replays its recent events on every connect, so
// onOpen is the cue to clear what was shown before.
func (c *helperClient) startEvents(onEvent func(json.RawMessage), onOpen func()) {
	c.stopEvents()
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	go func() {
		for ctx.Err() == nil {
			c.readEvents(ctx, onEvent, onOpen)
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
			}
		}
	}()
}

func (c *helperClient) readEvents(ctx context.Context, onEvent func(json.RawMessage), onOpen func()) {
	req, err := http.NewRequestWithContext(ctx, "GET", helperURL+"/events", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	onOpen()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if line, ok := strings.CutPrefix(sc.Text(), "data: "); ok && json.Valid([]byte(line)) {
			onEvent(json.RawMessage(line))
		}
	}
}

func (c *helperClient) stopEvents() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
}

var errNoServer = errors.New("Enter a custom server URL first.")
