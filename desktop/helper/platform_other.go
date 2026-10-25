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

//go:build !darwin && !windows

package main

import (
	"errors"
	"io"
	"os"

	"golang.zx2c4.com/wireguard/tun"
)

// Stubs so the module still builds and tests on Linux CI.

var errUnsupported = errors.New("doorpass-helper supports macOS and Windows only")

func configPath() string                                    { return "/etc/doorpass-helper/config.json" }
func logOutput(string) io.Writer                            { return os.Stderr }
func openTun() (tun.Device, string, error)                  { return nil, "", errUnsupported }
func bindToInterface(uintptr, int) error                    { return errUnsupported }
func defaultInterface(string) (string, int, string, error)  { return "", 0, "", errUnsupported }
func pinUplink(string, string) func()                       { return func() {} }
func interfaceResolvers(string, int) []string               { return nil }
func configureNetwork(string, string) (func() error, error) { return nil, errUnsupported }
func recoverNetwork()                                       {}
func install(*config) error                                 { return errUnsupported }
func uninstall() error                                      { return errUnsupported }

const keepTun = false
