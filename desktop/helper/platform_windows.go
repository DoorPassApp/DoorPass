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
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
	"golang.zx2c4.com/wireguard/tun"
)

const (
	serviceName   = "DoorPassHelper"
	tunName       = "DoorPass"
	ipUnicastIf   = 31 // IP_UNICAST_IF and IPV6_UNICAST_IF
	createNoWin   = 0x08000000
	wintunDLL     = "wintun.dll"
	installSubdir = "DoorPass Helper"
)

var (
	installDir = filepath.Join(os.Getenv("ProgramFiles"), installSubdir)
	dataDir    = filepath.Join(os.Getenv("ProgramData"), "DoorPassHelper")
)

func configPath() string { return filepath.Join(dataDir, "config.json") }

func logOutput(cmd string) io.Writer {
	if cmd != "run" {
		return os.Stderr
	}
	if isService, _ := svc.IsWindowsService(); !isService {
		return os.Stderr
	}
	os.MkdirAll(dataDir, 0o700)
	f, err := os.OpenFile(filepath.Join(dataDir, "helper.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return os.Stderr
	}
	return f
}

func openTun() (tun.Device, string, error) {
	dev, err := tun.CreateTUN(tunName, tunMTU)
	if err != nil {
		return nil, "", err
	}
	return dev, tunName, nil
}

func bindToInterface(fd uintptr, idx int) error {
	h := windows.Handle(fd)
	// IPv4 wants the index in network byte order, IPv6 in host order.
	be := int(uint32(idx)<<24 | uint32(idx)>>24 | (uint32(idx)>>8)&0xff00 | (uint32(idx)<<8)&0xff0000)
	err := windows.SetsockoptInt(h, windows.IPPROTO_IP, ipUnicastIf, be)
	if err != nil {
		err = windows.SetsockoptInt(h, windows.IPPROTO_IPV6, ipUnicastIf, idx)
	}
	return err
}

func adapters() ([]*windows.IpAdapterAddresses, error) {
	size := uint32(16 << 10)
	for {
		buf := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_INCLUDE_GATEWAYS, 0, first, &size)
		if errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var out []*windows.IpAdapterAddresses
		for a := first; a != nil; a = a.Next {
			out = append(out, a)
		}
		return out, nil
	}
}

// defaultInterface picks the connected adapter with an IPv4 gateway and the
// lowest metric, ignoring our own TUN adapter.
func defaultInterface(exclude string) (string, int, string, error) {
	as, err := adapters()
	if err != nil {
		return "", 0, "", err
	}
	var best *windows.IpAdapterAddresses
	for _, a := range as {
		name := windows.UTF16PtrToString(a.FriendlyName)
		if a.OperStatus != windows.IfOperStatusUp || a.IfIndex == 0 || name == exclude || name == tunName {
			continue
		}
		hasV4Gateway := false
		for g := a.FirstGatewayAddress; g != nil; g = g.Next {
			if ip := g.Address.IP(); ip != nil && ip.To4() != nil && !ip.IsUnspecified() {
				hasV4Gateway = true
			}
		}
		if hasV4Gateway && (best == nil || a.Ipv4Metric < best.Ipv4Metric) {
			best = a
		}
	}
	if best == nil {
		return "", 0, "", errors.New("no adapter with a default gateway")
	}
	return windows.UTF16PtrToString(best.FriendlyName), int(best.IfIndex), "", nil
}

// pinUplink is a no-op: with IP_UNICAST_IF, Windows routes by the bound
// adapter's own default route, which the tunnel's /1 routes don't shadow.
func pinUplink(string, string) func() { return func() {} }

func interfaceResolvers(_ string, idx int) []string {
	as, err := adapters()
	if err != nil {
		return nil
	}
	var rs []string
	for _, a := range as {
		if int(a.IfIndex) != idx {
			continue
		}
		for d := a.FirstDnsServerAddress; d != nil; d = d.Next {
			if ip := d.Address.IP(); ip != nil {
				rs = append(rs, ip.String())
			}
		}
	}
	return rs
}

func run1(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWin}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func netsh(args ...string) error {
	return run1("netsh", append([]string{"interface", "ipv4"}, args...)...)
}

// configureNetwork sets the address, DNS and routes on the Wintun adapter.
// All of it disappears with the adapter, so an unclean exit leaves nothing
// behind.
func configureNetwork(ifname, mode string) (func() error, error) {
	q := "name=" + ifname
	steps := [][]string{
		{"set", "address", q, "source=static", "address=" + tunGatewayIP, "mask=255.255.255.0"},
		{"set", "interface", "interface=" + ifname, "metric=1"},
		{"set", "dnsservers", q, "source=static", "address=" + fakeDNSIP, "register=none", "validate=no"},
	}
	routes := []string{}
	if mode == modeFull {
		routes = append(routes, "0.0.0.0/1", "128.0.0.0/1")
	}
	for _, r := range routes {
		steps = append(steps, []string{"add", "route", "prefix=" + r, "interface=" + ifname, "nexthop=0.0.0.0", "metric=0", "store=active"})
	}
	for _, s := range steps {
		if err := netsh(s...); err != nil {
			return nil, err
		}
	}
	run1("ipconfig", "/flushdns")
	return func() error {
		for _, r := range routes {
			netsh("delete", "route", "prefix="+r, "interface="+ifname, "store=active")
		}
		run1("ipconfig", "/flushdns")
		return nil
	}, nil
}

func recoverNetwork() {}

type service struct {
	srv *http.Server
	m   *manager
}

func (s *service) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	errc := make(chan error, 1)
	go func() { errc <- s.srv.ListenAndServe() }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-errc:
			slog.Error("API server stopped", "err", err)
			s.m.disconnect()
			return true, 1
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				s.m.disconnect()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				s.srv.Shutdown(ctx)
				cancel()
				return false, 0
			}
		}
	}
}

func serve(srv *http.Server, m *manager) error {
	if isService, _ := svc.IsWindowsService(); isService {
		return svc.Run(serviceName, &service{srv, m})
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	select {
	case err := <-errc:
		m.disconnect()
		return err
	case <-sig:
	}
	m.disconnect()
	return srv.Close()
}

func isAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o755)
}

func stopService(s *mgr.Service) {
	st, err := s.Control(svc.Stop)
	for i := 0; err == nil && st.State != svc.Stopped && i < 50; i++ {
		time.Sleep(200 * time.Millisecond)
		st, err = s.Query()
	}
}

func install(cfg *config) error {
	if !isAdmin() {
		return errors.New("install must run as Administrator")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	// The helper was called IntraHelper before the rename to DoorPass.
	for _, name := range []string{"IntraHelper", serviceName} {
		if s, err := m.OpenService(name); err == nil {
			stopService(s)
			s.Delete()
			s.Close()
			time.Sleep(time.Second) // Let the SCM release the old service.
		}
	}
	os.RemoveAll(filepath.Join(os.Getenv("ProgramFiles"), "Intra Helper"))
	os.RemoveAll(filepath.Join(os.Getenv("ProgramData"), "IntraHelper"))

	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return err
	}
	exe := filepath.Join(installDir, "doorpass-helper.exe")
	if err := copyFile(self, exe); err != nil {
		return fmt.Errorf("copy helper: %w", err)
	}
	if err := copyFile(filepath.Join(filepath.Dir(self), wintunDLL), filepath.Join(installDir, wintunDLL)); err != nil {
		return fmt.Errorf("copy %s: %w", wintunDLL, err)
	}
	if err := writeConfig(configPath(), cfg); err != nil {
		return err
	}
	// ProgramData is readable by all users by default; the token is not.
	run1("icacls", dataDir, "/inheritance:r", "/grant:r", "*S-1-5-18:(OI)(CI)F", "*S-1-5-32-544:(OI)(CI)F")

	s, err := m.CreateService(serviceName, exe, mgr.Config{
		DisplayName: "DoorPass Helper",
		Description: "Encrypted DNS (DoH) and TLS splitting tunnel for DoorPass.",
		StartType:   mgr.StartAutomatic,
	}, "run")
	if err != nil {
		return err
	}
	defer s.Close()
	s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 2 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}, 3600)
	return s.Start()
}

func uninstall() error {
	if !isAdmin() {
		return errors.New("uninstall must run as Administrator")
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	if s, err := m.OpenService(serviceName); err == nil {
		stopService(s)
		s.Delete()
		s.Close()
	}
	time.Sleep(time.Second)
	return errors.Join(os.RemoveAll(installDir), os.RemoveAll(dataDir))
}
