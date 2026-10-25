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
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/tun"
)

const (
	launchdLabel = "dev.bfast.doorpass-helper"
	helperPath   = "/Library/PrivilegedHelperTools/" + launchdLabel
	plistPath    = "/Library/LaunchDaemons/" + launchdLabel + ".plist"
	dataDir      = "/Library/Application Support/DoorPassHelper"
	logPath      = "/Library/Logs/DoorPassHelper.log"
	// Per-service DNS settings from before we connected. Its presence on
	// startup means the helper died while connected, so we restore it.
	dnsBackupPath = dataDir + "/dns-backup.json"
)

func configPath() string { return filepath.Join(dataDir, "config.json") }

func logOutput(cmd string) io.Writer { return os.Stderr } // launchd redirects to logPath.

func openTun() (tun.Device, string, error) {
	dev, err := tun.CreateTUN("utun", tunMTU)
	if err != nil {
		return nil, "", err
	}
	name, err := dev.Name()
	if err != nil {
		dev.Close()
		return nil, "", err
	}
	return dev, name, nil
}

func bindToInterface(fd uintptr, idx int) error {
	err := unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, idx)
	if err != nil {
		err = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF, idx)
	}
	return err
}

func defaultInterface(exclude string) (string, int, string, error) {
	out, err := exec.Command("netstat", "-rn", "-f", "inet").Output()
	if err != nil {
		return "", 0, "", err
	}
	name, gw := parseDefaultRoute(string(out), exclude)
	if name == "" {
		return "", 0, "", errors.New("no default route")
	}
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return "", 0, "", err
	}
	return name, ifi.Index, gw, nil
}

// pinUplink adds a default route scoped to the physical interface. Sockets
// bound with IP_BOUND_IF are routed by the routes scoped to that interface,
// and once 0/1 and 128/1 point at the tunnel, the primary interface has none,
// so they would fail with "network is unreachable".
func pinUplink(name, gw string) func() {
	args := []string{"-q", "-n", "add", "-inet", "default"}
	if gw == "" || strings.HasPrefix(gw, "link#") {
		args = append(args, "-interface", name)
	} else {
		args = append(args, gw)
	}
	args = append(args, "-ifscope", name)
	if err := run1("route", args...); err != nil {
		slog.Warn("pin uplink route", "err", err) // "File exists" means it is already there.
		return func() {}
	}
	slog.Info("pinned uplink route", "interface", name, "gateway", gw)
	return func() {
		run1("route", "-q", "-n", "delete", "-inet", "default", "-ifscope", name)
	}
}

func interfaceResolvers(name string, _ int) []string {
	var rs []string
	if out, err := exec.Command("ipconfig", "getoption", name, "domain_name_server").Output(); err == nil {
		rs = append(rs, strings.TrimSpace(string(out)))
	}
	// Manually configured servers from before we overrode them.
	for _, servers := range loadDNSBackup() {
		rs = append(rs, servers...)
	}
	return rs
}

func run1(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func configureNetwork(ifname, mode string) (func() error, error) {
	if err := run1("ifconfig", ifname, "inet", tunGatewayIP, tunRouterIP, "netmask", "255.255.255.0", "mtu", fmt.Sprint(tunMTU), "up"); err != nil {
		return nil, err
	}
	// Two /1 routes beat the default route without replacing it, so the
	// physical default stays in place for protected (IP_BOUND_IF) sockets.
	routes := []string{tunSubnet}
	if mode == modeFull {
		routes = append(routes, "0.0.0.0/1", "128.0.0.0/1")
	}
	for _, r := range routes {
		if err := run1("route", "-q", "-n", "add", "-inet", r, "-interface", ifname); err != nil {
			return nil, err // Routes go away with the utun interface.
		}
	}
	if err := overrideDNS(); err != nil {
		restoreDNS()
		return nil, err
	}
	flushDNSCache()
	return func() error {
		err := restoreDNS()
		for _, r := range routes {
			run1("route", "-q", "-n", "delete", "-inet", r, "-interface", ifname)
		}
		flushDNSCache()
		return err
	}, nil
}

var dnsMu sync.Mutex

func loadDNSBackup() map[string][]string {
	b, err := os.ReadFile(dnsBackupPath)
	if err != nil {
		return nil
	}
	var m map[string][]string
	json.Unmarshal(b, &m)
	return m
}

// overrideDNS points every enabled network service at the fake DNS address,
// saving the previous settings first.
func overrideDNS() error {
	dnsMu.Lock()
	defer dnsMu.Unlock()
	if _, err := os.Stat(dnsBackupPath); err == nil {
		return errors.New("a DNS backup already exists; restore it first")
	}
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return err
	}
	backup := map[string][]string{}
	for _, svc := range parseNetworkServices(string(out)) {
		cur, err := exec.Command("networksetup", "-getdnsservers", svc).Output()
		if err != nil {
			continue
		}
		backup[svc] = filterResolvers(parseDNSServers(string(cur)))
	}
	b, _ := json.Marshal(backup)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(dnsBackupPath, b, 0o600); err != nil {
		return err
	}
	set := 0
	for svc := range backup {
		if err := run1("networksetup", "-setdnsservers", svc, fakeDNSIP); err != nil {
			slog.Warn("set DNS", "service", svc, "err", err) // Some VPN services refuse; that's fine.
			continue
		}
		set++
	}
	if set == 0 {
		return errors.New("could not set DNS on any network service")
	}
	return nil
}

func restoreDNS() error {
	dnsMu.Lock()
	defer dnsMu.Unlock()
	backup := loadDNSBackup()
	if backup == nil {
		return nil
	}
	var errs []error
	for svc, servers := range backup {
		args := append([]string{"-setdnsservers", svc}, servers...)
		if len(servers) == 0 {
			args = append(args, "Empty")
		}
		if err := run1("networksetup", args...); err != nil {
			errs = append(errs, err)
		}
	}
	os.Remove(dnsBackupPath)
	return errors.Join(errs...)
}

func flushDNSCache() {
	run1("dscacheutil", "-flushcache")
	run1("killall", "-HUP", "mDNSResponder")
}

func recoverNetwork() {
	if _, err := os.Stat(dnsBackupPath); err == nil {
		slog.Warn("restoring DNS settings left over from an unclean exit")
		if err := restoreDNS(); err != nil {
			slog.Error("restore DNS", "err", err)
		}
		flushDNSCache()
	}
}

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>%s</string>
	<key>ProgramArguments</key>
	<array><string>%s</string><string>run</string></array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><true/>
	<key>StandardOutPath</key><string>%s</string>
	<key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst) // Atomic, so a running copy keeps its inode.
}

// The helper was called intra-helper before the rename to DoorPass.
const (
	legacyLabel   = "dev.bfast.intra-helper"
	legacyDataDir = "/Library/Application Support/IntraHelper"
)

// removeLegacy unloads and deletes the pre-rename helper. Stopping it restores
// DNS; a backup it could not restore (it had crashed) moves to our data dir so
// recoverNetwork picks it up.
func removeLegacy() {
	plist := "/Library/LaunchDaemons/" + legacyLabel + ".plist"
	if _, err := os.Stat(plist); err != nil {
		return
	}
	run1("launchctl", "bootout", "system/"+legacyLabel)
	if _, err := os.Stat(legacyDataDir + "/dns-backup.json"); err == nil {
		os.MkdirAll(dataDir, 0o700)
		os.Rename(legacyDataDir+"/dns-backup.json", dnsBackupPath)
	}
	os.Remove(plist)
	os.Remove("/Library/PrivilegedHelperTools/" + legacyLabel)
	os.RemoveAll(legacyDataDir)
	slog.Info("removed the pre-rename intra-helper")
}

func install(cfg *config) error {
	if os.Geteuid() != 0 {
		return errors.New("install must run as root")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	removeLegacy()
	run1("launchctl", "bootout", "system/"+launchdLabel) // Not loaded is fine.
	if err := os.MkdirAll(filepath.Dir(helperPath), 0o755); err != nil {
		return err
	}
	if err := copyFile(self, helperPath, 0o755); err != nil {
		return fmt.Errorf("copy helper: %w", err)
	}
	if err := writeConfig(configPath(), cfg); err != nil {
		return err
	}
	plist := fmt.Sprintf(plistTemplate, launchdLabel, helperPath, logPath, logPath)
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return err
	}
	return run1("launchctl", "bootstrap", "system", plistPath)
}

func uninstall() error {
	if os.Geteuid() != 0 {
		return errors.New("uninstall must run as root")
	}
	run1("launchctl", "bootout", "system/"+launchdLabel) // SIGTERM restores DNS.
	recoverNetwork()
	return errors.Join(os.Remove(plistPath), os.Remove(helperPath), os.RemoveAll(dataDir))
}

// keepTun: a utun device is created instantly, so each session gets its own.
const keepTun = false
