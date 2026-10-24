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
	"errors"
	"net"
	"runtime"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/dock"
)

// Status is what the window shows: the helper's status plus the settings and
// the selected server.
type Status struct {
	Helper          string          `json:"helper"` // ok | outdated | missing | token-mismatch
	Version         string          `json:"version,omitempty"`
	Connected       bool            `json:"connected"`
	URL             string          `json:"url,omitempty"`
	Mode            string          `json:"mode,omitempty"`
	Since           int64           `json:"since,omitempty"`
	Interface       string          `json:"interface,omitempty"`
	Uplink          string          `json:"uplink,omitempty"`
	LastError       string          `json:"lastError,omitempty"`
	Busy            string          `json:"busy,omitempty"` // connecting | disconnecting
	QueryLog        bool            `json:"queryLog"`
	Stats           json.RawMessage `json:"stats,omitempty"`
	Error           string          `json:"error,omitempty"`
	Server          Server          `json:"server"`
	ConnectedServer *Server         `json:"connectedServer"`
	Settings        Settings        `json:"settings"`
}

// DoorPass holds the app state. Service is the part the window calls.
type DoorPass struct {
	app     *application.App
	win     *application.WebviewWindow
	popover *application.WebviewWindow
	tray    *application.SystemTray
	dock    *dock.DockService
	client  *helperClient
	servers []Server

	mu       sync.Mutex
	settings Settings
	last     Status
	quitting bool

	trayKey string // What the tray menu currently shows.
	iconOn  *bool
}

func (d *DoorPass) serverFor(url string) Server {
	for _, s := range d.servers {
		if s.URL == url {
			return s
		}
	}
	return Server{Name: "Custom server", URL: url}
}

func (d *DoorPass) activeServer() Server {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.settings.URL == "custom" {
		return d.serverFor(d.settings.CustomURL)
	}
	return d.serverFor(d.settings.URL)
}

// refresh polls the helper, keeps its query log setting in line with ours
// (the helper forgets it when it restarts), and pushes the result to the
// window, tray and Dock.
func (d *DoorPass) refresh() Status {
	d.mu.Lock()
	settings := d.settings
	d.mu.Unlock()

	st := Status{Settings: settings, Server: d.activeServer()}
	hs, err := d.client.status()
	// A helper that answers slowly is busy, not missing: keep what we knew
	// rather than asking to install it again.
	var nerr net.Error
	if err != nil && errors.As(err, &nerr) && nerr.Timeout() {
		d.mu.Lock()
		last := d.last
		d.mu.Unlock()
		if last.Helper == "ok" {
			return last
		}
	}
	if err == nil && hs.Version == version && hs.QueryLog != settings.QueryLog {
		hs, err = d.client.setQueryLog(settings.QueryLog)
	}
	var herr *HelperError
	switch {
	case err == nil:
		st.Helper = "ok"
		if hs.Version != version {
			st.Helper = "outdated"
		}
		st.Version, st.Connected, st.URL, st.Mode, st.Since = hs.Version, hs.Connected, hs.URL, hs.Mode, hs.Since
		st.Interface, st.Uplink, st.LastError, st.QueryLog, st.Stats = hs.Interface, hs.Uplink, hs.LastError, hs.QueryLog, hs.Stats
		st.Busy = hs.Busy
		if hs.URL != "" {
			s := d.serverFor(hs.URL)
			st.ConnectedServer = &s
		}
	case errors.As(err, &herr) && herr.Status == 401:
		st.Helper, st.Error = "token-mismatch", err.Error()
	default:
		st.Helper, st.Error = "missing", err.Error()
	}

	d.mu.Lock()
	d.last = st
	d.mu.Unlock()
	d.app.Event.Emit("status", st)
	d.updateTray(st)
	return st
}

func (d *DoorPass) connect() (Status, error) {
	s := d.activeServer()
	if s.URL == "" {
		return d.last, errNoServer
	}
	d.mu.Lock()
	mode := d.settings.Mode
	d.mu.Unlock()
	if _, err := d.client.connect(s.URL, s.IPs, mode); err != nil {
		return d.refresh(), err
	}
	return d.refresh(), nil
}

func (d *DoorPass) disconnect() (Status, error) {
	if _, err := d.client.disconnect(); err != nil {
		return d.refresh(), err
	}
	return d.refresh(), nil
}

func (d *DoorPass) waitForHelper() error {
	// A new service's first start can be slow on Windows (Defender scans it).
	for end := time.Now().Add(60 * time.Second); time.Now().Before(end); time.Sleep(500 * time.Millisecond) {
		if st, err := d.client.status(); err == nil && st.Version == version {
			return nil
		}
	}
	return errors.New("The helper was installed but did not start. See the helper log.")
}

func (d *DoorPass) startEvents() {
	d.client.startEvents(
		func(ev json.RawMessage) { d.app.Event.Emit("query", ev) },
		func() { d.app.Event.Emit("queries-reset") },
	)
}

// ---- Window, tray, Dock ---------------------------------------------------

func (d *DoorPass) showWindow() {
	if d.win == nil {
		return
	}
	if d.popover != nil {
		d.popover.Hide()
	}
	d.dock.ShowAppIcon()
	// Show first: a window hidden while minimized stays minimized otherwise.
	d.win.Show()
	if d.win.IsMinimised() {
		d.win.UnMinimise()
	}
	d.win.Focus()
}

// hideWindow leaves DoorPass in the menu bar / tray only: no window, no Dock
// icon (macOS), no taskbar button (Windows).
func (d *DoorPass) hideWindow() {
	if d.win == nil {
		return
	}
	d.win.Hide()
	d.dock.HideAppIcon()
}

func (d *DoorPass) updateTray(st Status) {
	on := st.Connected
	d.mu.Lock()
	iconChanged := d.iconOn == nil || *d.iconOn != on
	d.iconOn = &on
	settings := d.settings
	d.mu.Unlock()

	if iconChanged {
		if runtime.GOOS == "darwin" {
			d.tray.SetTemplateIcon(asset(map[bool]string{true: "trayOnTemplate@2x.png", false: "trayOffTemplate@2x.png"}[on]))
			d.app.SetIcon(asset(map[bool]string{true: "icon.png", false: "icon-off.png"}[on]))
		} else {
			d.tray.SetIcon(asset(map[bool]string{true: "tray-on@2x.png", false: "tray-off@2x.png"}[on]))
		}
	}

	label := "Off"
	switch {
	case st.Helper != "ok":
		label = "Helper not installed"
	case on && st.ConnectedServer != nil:
		label = "On: " + st.ConnectedServer.Name
	case on:
		label = "On"
	}
	key := label + "|" + settings.URL + "|" + settings.Mode + "|" + st.Helper
	d.mu.Lock()
	same := key == d.trayKey
	d.trayKey = key
	d.mu.Unlock()
	if same {
		return
	}
	hint := "click to turn on"
	switch {
	case st.Helper != "ok":
		hint = "click to set up"
	case on:
		hint = "click to turn off"
	}
	d.tray.SetTooltip("DoorPass: " + label + " (" + hint + ")")

	menu := d.buildMenu(label)
	d.tray.SetMenu(menu)
}

// buildMenu is DoorPass's menu, shown from the popover's gear and by
// right-clicking the tray icon: mode, server, Preferences, About, Quit.
func (d *DoorPass) buildMenu(status string) *application.Menu {
	d.mu.Lock()
	settings := d.settings
	d.mu.Unlock()
	set := func(patch map[string]any) func(*application.Context) {
		return func(*application.Context) {
			go func() {
				if _, err := d.updateSettings(patch); err != nil {
					d.app.Event.Emit("error", err.Error())
				}
			}()
		}
	}

	menu := d.app.NewMenu()
	if status != "" {
		menu.Add(status).SetEnabled(false)
		menu.AddSeparator()
	}
	menu.AddRadio("Full: encrypted DNS + TLS splitting", settings.Mode != "dns").OnClick(set(map[string]any{"mode": "full"}))
	menu.AddRadio("DNS only", settings.Mode == "dns").OnClick(set(map[string]any{"mode": "dns"}))
	sub := menu.AddSubmenu("Server")
	for _, s := range d.servers {
		sub.AddRadio(s.Name, settings.URL == s.URL).OnClick(set(map[string]any{"url": s.URL}))
	}
	menu.AddSeparator()
	menu.Add("Preferences…").OnClick(func(*application.Context) { d.showWindow() })
	menu.Add("About DoorPass").OnClick(func(*application.Context) {
		d.app.Dialog.Info().SetTitle("DoorPass " + version).
			SetMessage("Encrypted DNS (DNS-over-HTTPS) and TLS splitting.\nBased on Intra by Jigsaw (Apache License 2.0).").Show()
	})
	menu.AddSeparator()
	menu.Add("Quit DoorPass").OnClick(func(*application.Context) { d.app.Quit() })
	return menu
}

// shouldQuit turns protection off first, so the network never stays pointed
// at a tunnel nobody can see.
func (d *DoorPass) shouldQuit() bool {
	d.mu.Lock()
	d.quitting = true
	connected := d.last.Connected
	d.mu.Unlock()
	d.client.stopEvents()
	if connected {
		d.client.disconnect()
	}
	return true
}

// ---- Settings -------------------------------------------------------------

func (d *DoorPass) updateSettings(patch map[string]any) (Status, error) {
	d.mu.Lock()
	s := d.settings
	str := func(k string, dst *string) {
		if v, ok := patch[k].(string); ok {
			*dst = v
		}
	}
	boolean := func(k string, dst *bool) {
		if v, ok := patch[k].(bool); ok {
			*dst = v
		}
	}
	str("url", &s.URL)
	str("customUrl", &s.CustomURL)
	str("mode", &s.Mode)
	boolean("autoConnect", &s.AutoConnect)
	boolean("queryLog", &s.QueryLog)
	if s.Mode != "dns" {
		s.Mode = "full"
	}
	d.settings = s
	connected := d.last.Connected
	d.mu.Unlock()

	if err := saveSettings(s); err != nil {
		return d.refresh(), err
	}
	if v, ok := patch["openAtLogin"].(bool); ok {
		var err error
		if v {
			err = d.app.Autostart.EnableWithOptions(application.AutostartOptions{Arguments: []string{"--hidden"}})
		} else {
			err = d.app.Autostart.Disable()
		}
		if err != nil {
			return d.refresh(), err
		}
	}
	// Apply a new server or mode right away when already on.
	_, server := patch["url"]
	_, custom := patch["customUrl"]
	_, mode := patch["mode"]
	if connected && (server || custom || mode) {
		return d.connect()
	}
	return d.refresh(), nil
}

// ---- Service: the methods the window calls (main.Service.*) ---------------

type Service struct{ d *DoorPass }

func (s *Service) GetServers() []Server { return s.d.servers }

func (s *Service) GetStatus() Status { return s.d.refresh() }

func (s *Service) Connect() (Status, error) { return s.d.connect() }

func (s *Service) Disconnect() (Status, error) { return s.d.disconnect() }

func (s *Service) Probe() (ProbeResult, error) {
	srv := s.d.activeServer()
	if srv.URL == "" {
		return ProbeResult{}, errNoServer
	}
	return s.d.client.probe(srv.URL, srv.IPs)
}

func (s *Service) InstallHelper() (Status, error) {
	if err := runElevated("install", "--token-file", dataFile("helper-token")); err != nil {
		return s.d.refresh(), err
	}
	if err := s.d.waitForHelper(); err != nil {
		return s.d.refresh(), err
	}
	s.d.startEvents()
	return s.d.refresh(), nil
}

func (s *Service) UninstallHelper() (Status, error) {
	s.d.client.stopEvents()
	if err := runElevated("uninstall"); err != nil {
		return s.d.refresh(), err
	}
	return s.d.refresh(), nil
}

func (s *Service) UpdateSettings(patch map[string]any) (Status, error) {
	return s.d.updateSettings(patch)
}

// OpenWindow shows the main window (the popover's settings button).
func (s *Service) OpenWindow() { s.d.showWindow() }

// OpenMenu opens DoorPass's menu in the popover at (x, y), just below the
// gear button.
func (s *Service) OpenMenu(x, y int) {
	cm := s.d.app.ContextMenu.New()
	cm.Menu = s.d.buildMenu("")
	s.d.app.ContextMenu.Add("doorpass", cm)
	s.d.popover.OpenContextMenu(&application.ContextMenuData{Id: "doorpass", X: x, Y: y})
}

func (s *Service) GetLoginItem() bool {
	on, _ := s.d.app.Autostart.IsEnabled()
	return on
}
