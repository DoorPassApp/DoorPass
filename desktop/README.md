# DoorPass (macOS and Windows)

DoorPass is a desktop app based on Jigsaw's Intra for Android (Apache 2.0). It encrypts every DNS lookup with DNS-over-HTTPS (DoH) and, in Full mode, splits the TLS ClientHello of HTTPS connections so simple server name indication (SNI) filters cannot match it. It reuses Intra's Go networking core (`Android/app/src/go/`) unchanged.

The installers are about 13 MB (one universal macOS DMG for Apple silicon and Intel) and 7 MB (Windows): the app is a Go program that draws its window with the web view the operating system already has (WebKit on macOS, WebView2 on Windows 10/11) instead of bundling Chromium as the Electron builds up to 0.2.0 did.

Upgrading from the earlier "Intra" desktop builds is in place: DoorPass copies the old settings and pairing token, and installing its helper removes the old `intra-helper` daemon or `IntraHelper` service.

## How it fits together

```
 DoorPass app (DoorPass.app / DoorPass.exe, runs as you)
   desktop/app: Go + Wails v3 on the system web view (WebKit / WebView2)
   tray menu · window · settings · live query list
          │  HTTP on 127.0.0.1:47121, bearer token
          ▼
 doorpass-helper (root launchd daemon / Windows service "DoorPassHelper")
   desktop/helper: TUN device + routes + DNS settings
          │
          ▼
 Intra Go core: intra.Tunnel (lwIP) → doh.Resolver, split.DialWithSplitRetry
```

The helper creates a TUN interface (`utun` on macOS, Wintun on Windows) and gives it the same layout the Android app uses: address `10.111.222.1`, fake DNS server `10.111.222.3`. It then points the system DNS at that fake address. In Full mode it also adds the routes `0.0.0.0/1` and `128.0.0.0/1` through the tunnel. Its own sockets are bound to the physical interface (`IP_BOUND_IF` on macOS, `IP_UNICAST_IF` on Windows), so they never loop back into the tunnel. That plays the role of Android's `VpnService.protect()`.

On macOS the helper saves each network service's DNS servers to `/Library/Application Support/DoorPassHelper/dns-backup.json` before overriding them. It restores them on disconnect, and again at startup if it ever died while connected. On Windows the DNS setting and routes live on the Wintun adapter itself, so they disappear with it.

The app installs the helper on first launch, after one administrator prompt. The helper copies itself to `/Library/PrivilegedHelperTools/dev.bfast.doorpass-helper` or `C:\Program Files\DoorPass Helper\`. The token that pairs app and helper sits in the app's user data folder and in the helper's root-only config. Requests carrying an `Origin` header or a foreign `Host` are refused, so web pages cannot drive the helper.

Clicking the menu bar / tray icon opens a small popover with the round door button (on/off), the status and a settings button that opens the main window; right-clicking the icon opens a menu with the server list, Open DoorPass and Quit. Closing or minimizing the main window hides DoorPass to the menu bar / tray, out of the Dock and taskbar; when opened at login it starts there. Quitting (tray menu, or Cmd+Q while the window is open on macOS) turns protection off.

The query log, the list of domain names being looked up, is off by default, and the helper records no domain names while it is off. Turn it on in Settings only while checking something; turning it off again clears it. The counters (queries, latency, failures, TLS splits) always run. The helper's own log file only records start, connect and disconnect lines.

## Modes

Full mode is the Android behaviour: all IPv4 traffic passes through Intra's user-space TCP/IP stack, DNS goes over DoH, and HTTPS connections get TLS splitting. DNS-only mode routes just the fake DNS address, so only lookups are touched and everything else keeps its normal path. IPv6 traffic is not routed in either mode, the same as on Android.

## Building

```bash
desktop/build.sh          # one universal macOS DMG and the Windows x64 installer
desktop/build.sh mac      # or just one platform
desktop/build.sh win
```

The version comes from `desktop/VERSION`. This needs Go (the version in `go.mod`), the Xcode Command Line Tools, `x86_64-w64-mingw32-gcc` for the Windows helper's C code (`brew install mingw-w64`), and `makensis` for the Windows installer; without it, the script runs Debian's `nsis` in Docker (OrbStack). The Windows app itself needs no C compiler. The macOS app is one universal binary for Apple silicon and Intel, signed ad hoc (not with a Developer ID). Installers and `SHA256SUMS.txt` go to `/Volumes/Lexar/DoorPass/releases/<version>/` when the Lexar drive is mounted, else to `desktop/dist/<version>/`; intermediate files stay in `desktop/build/`.

For development on a Mac: build the helper once (`desktop/build.sh mac`), then `cd desktop/app && go run .`. The app finds the helper in `app/helper/mac-<arch>/`.

Go tests: `go test -race ./desktop/helper` from the repo root; the app is its own Go module in `desktop/app`, so Intra's Android build and CI don't see it.

Icons are drawn as SVG in `desktop/design/render-icons.js` (a door: closed when off, open with the doorway lit warm gold when on). After editing it, run `npx -y electron@44.7.0 render-icons.js` from `desktop/design` to regenerate every PNG in `app/assets`; Electron is used only as an SVG renderer there.

## Installing on a team machine

On macOS, open the DMG and drag DoorPass to Applications. The build is unsigned, so the first launch needs right-click, then Open (or System Settings, then Privacy & Security, then Open Anyway). Then click **Install helper** and enter an administrator password.

On Windows, run `DoorPass-<version>-win-x64-setup.exe`. SmartScreen warns about an unknown publisher; choose More info, then Run anyway. In the app, click **Install helper** and accept the User Account Control prompt.

To remove the helper, use Settings, then Helper, then **Uninstall helper** in the app before deleting the app.

## Logs

The macOS log is `/Library/Logs/DoorPassHelper.log`. The Windows log is `C:\ProgramData\DoorPassHelper\helper.log`.
