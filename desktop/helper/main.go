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

// doorpass-helper is the privileged half of DoorPass. It runs as a launchd
// daemon (macOS) or a Windows service, owns the TUN interface and Intra's Go
// tunnel, and is driven by the Electron app over a token-protected HTTP API on
// 127.0.0.1.
//
//	doorpass-helper run                       serve (launchd / service manager / foreground)
//	doorpass-helper install --token-file F    copy to a system location and register (root/admin)
//	doorpass-helper uninstall                 unregister and remove (root/admin)
//	doorpass-helper version
package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

var version = "dev" // Set with -ldflags "-X main.version=...".

const defaultAddr = "127.0.0.1:47121"

type config struct {
	Token string `json:"token"`
	Addr  string `json:"addr"`
}

func loadConfig(path string) (*config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(c.Token) < 32 {
		return nil, errors.New("config token is missing or too short")
	}
	if c.Addr == "" {
		c.Addr = defaultAddr
	}
	return &c, nil
}

func writeConfig(path string, c *config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(path, b, 0o600)
}

func readToken(tokenFile string) (string, error) {
	if tokenFile == "" {
		return "", errors.New("--token-file is required")
	}
	b, err := os.ReadFile(tokenFile)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(b))
	if _, err := hex.DecodeString(tok); err != nil || len(tok) < 32 {
		return "", errors.New("token must be at least 32 hex characters")
	}
	return tok, nil
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: doorpass-helper run [--config FILE] | install --token-file FILE | uninstall | version")
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	cfgPath := fs.String("config", configPath(), "config file")
	tokenFile := fs.String("token-file", "", "file holding the API token (install)")
	debug := fs.Bool("debug", false, "debug logging")
	fs.Parse(args)

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(logOutput(cmd), &slog.HandlerOptions{Level: level})))

	var err error
	switch cmd {
	case "run":
		err = run(*cfgPath)
	case "install":
		var tok string
		if tok, err = readToken(*tokenFile); err == nil {
			err = install(&config{Token: tok, Addr: defaultAddr})
		}
	case "uninstall":
		err = uninstall()
	case "version":
		fmt.Println(version)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "doorpass-helper:", err)
		slog.Error(cmd+" failed", "err", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}
	recoverNetwork()
	m := newManager()
	srv, err := newServer(cfg, m)
	if err != nil {
		return err
	}
	slog.Info("doorpass-helper started", "version", version, "addr", cfg.Addr)
	return serve(srv, m)
}
