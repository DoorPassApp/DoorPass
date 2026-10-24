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
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// API (all requests need "Authorization: Bearer <token>"):
//
//	GET  /status       Status
//	POST /connect      {"url", "ips", "mode"}   start, or switch the DoH server
//	POST /disconnect
//	POST /probe        {"url", "ips"}           {"ok", "error"}
//	POST /querylog     {"enabled"}              record domain names (default off)
//	GET  /events       text/event-stream of QueryEvent, recent ones first

type connectRequest struct {
	URL  string `json:"url"`
	IPs  string `json:"ips"`
	Mode string `json:"mode"`
}

func newServer(cfg *config, m *manager) (*http.Server, error) {
	_, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return nil, err
	}
	allowedHosts := map[string]bool{"127.0.0.1:" + port: true, "localhost:" + port: true}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, m.status())
	})
	mux.HandleFunc("POST /connect", func(w http.ResponseWriter, r *http.Request) {
		var req connectRequest
		if err := decode(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := m.connect(req.URL, req.IPs, req.Mode); err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, m.status())
	})
	mux.HandleFunc("POST /disconnect", func(w http.ResponseWriter, r *http.Request) {
		m.disconnect()
		writeJSON(w, http.StatusOK, m.status())
	})
	mux.HandleFunc("POST /probe", func(w http.ResponseWriter, r *http.Request) {
		var req connectRequest
		if err := decode(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		res := map[string]any{"ok": true}
		if err := m.probe(req.URL, req.IPs); err != nil {
			res = map[string]any{"ok": false, "error": err.Error()}
		}
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("POST /querylog", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		m.hub.setQueryLog(req.Enabled)
		writeJSON(w, http.StatusOK, m.status())
	})
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		recent, c := m.hub.subscribe()
		defer m.hub.unsubscribe(c)
		for _, b := range recent {
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		flusher.Flush()
		ping := time.NewTicker(15 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case b := <-c:
				fmt.Fprintf(w, "data: %s\n\n", b)
			case <-ping.C:
				fmt.Fprint(w, ": ping\n\n")
			}
			flusher.Flush()
		}
	})

	token := []byte("Bearer " + cfg.Token)
	guard := func(w http.ResponseWriter, r *http.Request) {
		// Browsers always send Origin on cross-site requests and the Electron
		// main process never does, so any Origin means a web page is trying.
		// The Host check defeats DNS rebinding.
		if r.Header.Get("Origin") != "" || !allowedHosts[r.Host] {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), token) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	}

	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           http.HandlerFunc(guard),
		ReadHeaderTimeout: 5 * time.Second,
	}, nil
}

func decode(r *http.Request, v *connectRequest) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return err
	}
	u, err := url.Parse(v.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("url must be an https:// DoH URL")
	}
	for _, ip := range splitIPs(v.IPs) {
		if strings.TrimSpace(ip) == "" {
			return fmt.Errorf("ips must be a comma-separated list")
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
