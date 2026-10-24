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

// DoorPass is the desktop app: window, menu bar / tray and settings. It runs
// as the user on the system web view (WebKit on macOS, WebView2 on Windows)
// and drives doorpass-helper, which does the privileged networking.
package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"os"
	"slices"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/dock"
)

var version = "dev" // Set with -ldflags "-X main.version=...".

//go:embed frontend
var frontendFS embed.FS

//go:embed assets/*.png
var assetFS embed.FS

//go:embed servers.json
var serversJSON []byte

func asset(name string) []byte {
	b, err := assetFS.ReadFile("assets/" + name)
	if err != nil {
		log.Printf("missing asset %s", name)
	}
	return b
}

func main() {
	var servers []Server
	if err := json.Unmarshal(serversJSON, &servers); err != nil {
		log.Fatal(err)
	}
	migrateFromIntra()
	token, err := loadToken()
	if err != nil {
		log.Fatal(err)
	}
	d := &DoorPass{client: newHelperClient(token), servers: servers, settings: loadSettings(servers), dock: dock.New()}
	frontend, _ := fs.Sub(frontendFS, "frontend")

	d.app = application.New(application.Options{
		Name:        "DoorPass",
		Description: "Encrypted DNS and TLS splitting, based on Jigsaw's Intra.",
		Icon:        asset("icon.png"),
		Services:    []application.Service{application.NewService(&Service{d}), application.NewService(d.dock)},
		Assets:      application.AssetOptions{Handler: application.BundledAssetFileServer(frontend)},
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyRegular,
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               "dev.bfast.doorpass",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { d.showWindow() },
		},
		ShouldQuit: d.shouldQuit,
	})

	d.tray = d.app.SystemTray.New()

	// Opened at login: start in the menu bar / tray only.
	startHidden := slices.Contains(os.Args[1:], "--hidden")
	d.win = d.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "DoorPass",
		Width:            900,
		Height:           680,
		MinWidth:         640,
		MinHeight:        480,
		URL:              "/",
		Hidden:           startHidden,
		BackgroundColour: application.NewRGB(0xf3, 0xfa, 0xf2),
		Mac:              application.MacWindow{Appearance: application.NSAppearanceNameAqua},
		Windows:          application.WindowsWindow{Theme: application.Light},
	})
	// Closing or minimizing hides DoorPass to the menu bar / tray, out of the
	// Dock and taskbar. Quit is in the tray menu (and Cmd+Q while the window
	// is open on macOS).
	d.win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		d.mu.Lock()
		quitting := d.quitting
		d.mu.Unlock()
		if !quitting {
			e.Cancel()
			d.hideWindow()
		}
	})
	d.win.OnWindowEvent(events.Common.WindowMinimise, func(*application.WindowEvent) { d.hideWindow() })
	// Opening DoorPass again from Finder or Launchpad: macOS would show the
	// window natively, behind Wails' back, and Wails would then ignore the
	// next close. Show it through showWindow instead.
	d.app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) { d.showWindow() })

	// Clicking the menu bar / tray icon opens a small popover with the on/off
	// button; right-click opens the menu. The popover hides when it loses
	// focus.
	d.popover = d.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "popover",
		Title:            "DoorPass",
		Width:            190,
		Height:           190,
		URL:              "/popover.html",
		Frameless:        true,
		AlwaysOnTop:      true,
		Hidden:           true,
		DisableResize:    true,
		BackgroundColour: application.NewRGB(0xff, 0xff, 0xff),
		Mac:              application.MacWindow{Appearance: application.NSAppearanceNameAqua},
		Windows:          application.WindowsWindow{Theme: application.Light, HiddenOnTaskbar: true},
	})
	d.popover.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) { d.popover.Hide() })
	d.tray.AttachWindow(d.popover).WindowOffset(4)

	d.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go d.start(startHidden)
	})

	if err := d.app.Run(); err != nil {
		log.Fatal(err)
	}
}

func (d *DoorPass) start(hidden bool) {
	if hidden {
		d.hideWindow()
	} else {
		d.showWindow() // Come to the front even when started from a terminal.
	}
	st := d.refresh()
	if st.Helper == "ok" {
		d.startEvents()
		d.mu.Lock()
		auto := d.settings.AutoConnect
		d.mu.Unlock()
		if auto && !st.Connected {
			d.connect()
		}
	}
	for range time.Tick(3 * time.Second) {
		d.refresh()
	}
}
