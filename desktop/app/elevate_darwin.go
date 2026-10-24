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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// helperBinary finds doorpass-helper: Contents/Resources/helper in the app
// bundle, or helper/mac-<arch> next to the binary in a development build.
func helperBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	arch := map[string]string{"arm64": "mac-arm64", "amd64": "mac-x64"}[runtime.GOARCH]
	for _, p := range []string{
		filepath.Join(dir, "..", "Resources", "helper", "doorpass-helper"),
		filepath.Join(dir, "helper", arch, "doorpass-helper"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("doorpass-helper is missing from the app")
}

// stageHelper copies the helper to a private temp dir. macOS privacy
// protection (TCC) stops the root shell behind the admin prompt from reading
// ~/Documents, ~/Downloads and mounted volumes, wherever the app happens to
// live.
func stageHelper() (string, func(), error) {
	src, err := helperBinary()
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "doorpass-helper-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	dst := filepath.Join(dir, "doorpass-helper")
	in, err := os.Open(src)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err == nil {
		_, err = io.Copy(out, in)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return dst, cleanup, nil
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func appleScriptString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// runElevated runs the helper with args as root after one admin prompt.
func runElevated(args ...string) error {
	bin, cleanup, err := stageHelper()
	if err != nil {
		return err
	}
	defer cleanup()
	parts := []string{shQuote(bin)}
	for _, a := range args {
		parts = append(parts, shQuote(a))
	}
	script := fmt.Sprintf("do shell script %s with administrator privileges with prompt %s",
		appleScriptString(strings.Join(parts, " ")), appleScriptString("DoorPass needs to install its network helper."))
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "User canceled") || strings.Contains(msg, "(-128)") {
			return errors.New("Cancelled")
		}
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}
