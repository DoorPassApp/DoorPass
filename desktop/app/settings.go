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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Server is one DoH server from servers.json (generated from Intra's
// Android resources).
type Server struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	IPs  string `json:"ips"`
}

// Settings are the user's choices, kept in settings.json.
type Settings struct {
	URL         string `json:"url"` // A server URL, or "custom".
	CustomURL   string `json:"customUrl"`
	Mode        string `json:"mode"` // "full" or "dns"
	AutoConnect bool   `json:"autoConnect"`
	QueryLog    bool   `json:"queryLog"` // Domain names are only recorded while checking something.
}

// dataDir is the same per-user folder the Electron builds used
// (~/Library/Application Support/DoorPass, %APPDATA%\DoorPass), so an upgrade
// keeps the settings and the helper pairing.
func dataDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "DoorPass")
}

func dataFile(name string) string { return filepath.Join(dataDir(), name) }

func defaultSettings(servers []Server) Settings {
	return Settings{URL: servers[1].URL, Mode: "full"} // Cloudflare, as on Android.
}

func loadSettings(servers []Server) Settings {
	s := defaultSettings(servers)
	if b, err := os.ReadFile(dataFile("settings.json")); err == nil {
		json.Unmarshal(b, &s)
	}
	return s
}

func saveSettings(s Settings) error {
	if err := os.MkdirAll(dataDir(), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(dataFile("settings.json"), b, 0o600)
}

var tokenRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// loadToken returns the token shared with the helper at install time; it
// keeps other local programs and web pages from driving the helper.
func loadToken() (string, error) {
	if b, err := os.ReadFile(dataFile("helper-token")); err == nil {
		if t := strings.TrimSpace(string(b)); tokenRE.MatchString(t) {
			return t, nil
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	t := hex.EncodeToString(buf)
	if err := os.MkdirAll(dataDir(), 0o700); err != nil {
		return "", err
	}
	return t, os.WriteFile(dataFile("helper-token"), []byte(t), 0o600)
}

// migrateFromIntra copies settings and the helper token from the builds made
// before the rename to DoorPass, once.
func migrateFromIntra() {
	old := filepath.Join(filepath.Dir(dataDir()), "Intra")
	for _, name := range []string{"helper-token", "settings.json"} {
		to := dataFile(name)
		if _, err := os.Stat(to); err == nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(old, name))
		if err != nil {
			continue
		}
		os.MkdirAll(dataDir(), 0o700)
		os.WriteFile(to, b, 0o600)
	}
}
