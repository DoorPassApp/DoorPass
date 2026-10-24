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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const createNoWindow = 0x08000000

// helperBinary finds doorpass-helper.exe (with wintun.dll beside it) in the
// helper folder next to DoorPass.exe, or helper\win-x64 in a development
// build.
func helperBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	for _, p := range []string{
		filepath.Join(dir, "helper", "doorpass-helper.exe"),
		filepath.Join(dir, "helper", "win-x64", "doorpass-helper.exe"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("doorpass-helper.exe is missing from the app")
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// runElevated runs the helper with args as Administrator after one UAC
// prompt, and waits for it.
func runElevated(args ...string) error {
	bin, err := helperBinary()
	if err != nil {
		return err
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = psQuote(a)
	}
	script := fmt.Sprintf("$p = Start-Process -FilePath %s -ArgumentList %s -Verb RunAs -WindowStyle Hidden -Wait -PassThru; exit $p.ExitCode",
		psQuote(bin), strings.Join(quoted, ","))
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(strings.ToLower(msg), "canceled") || strings.Contains(strings.ToLower(msg), "cancelled") {
			return errors.New("Cancelled")
		}
		if msg == "" {
			msg = fmt.Sprintf("helper exited with %v", err)
		}
		return errors.New(msg)
	}
	return nil
}
