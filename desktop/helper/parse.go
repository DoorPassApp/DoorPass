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
	"bufio"
	"net"
	"strings"
)

// Parsers for the text output of macOS network tools. They live outside the
// darwin build so they are covered by tests on every platform.

// parseNetworkServices parses `networksetup -listallnetworkservices` and
// returns the enabled services.
func parseNetworkServices(out string) []string {
	var services []string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "An asterisk") || strings.HasPrefix(line, "*") {
			continue
		}
		services = append(services, line)
	}
	return services
}

// parseDNSServers parses `networksetup -getdnsservers <service>`. An empty
// result means the service uses the DNS servers handed out by DHCP.
func parseDNSServers(out string) []string {
	var servers []string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if net.ParseIP(line) != nil {
			servers = append(servers, line)
		}
	}
	return servers
}

// parseDefaultRoute parses `netstat -rn -f inet` and returns the interface and
// gateway of the primary default route, skipping interface-scoped routes and
// the interface named exclude.
func parseDefaultRoute(out, exclude string) (netif, gateway string) {
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[0] != "default" {
			continue
		}
		flags := f[2]
		if !strings.Contains(flags, "G") || strings.Contains(flags, "I") || f[3] == exclude {
			continue
		}
		return f[3], f[1]
	}
	return "", ""
}

// filterResolvers keeps the DNS servers worth asking: real unicast addresses,
// IPv4 first. It drops the fake DNS address and the placeholders Windows
// lists on adapters without IPv6 DNS (fec0:0:0:ffff::1-3, site-local), which
// never answer and would stall lookups for seconds.
func filterResolvers(ips []string) []string {
	var v4, v6 []string
	for _, s := range ips {
		s = strings.TrimSpace(s)
		ip := net.ParseIP(s)
		if ip == nil || s == fakeDNSIP || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || siteLocal.Contains(ip) {
			continue
		}
		if ip.To4() != nil {
			v4 = append(v4, s)
		} else {
			v6 = append(v6, s)
		}
	}
	return append(v4, v6...)
}

var _, siteLocal, _ = net.ParseCIDR("fec0::/10")
