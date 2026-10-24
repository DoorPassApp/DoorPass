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

// filterResolvers drops empty entries, non-IP entries and the fake DNS address.
func filterResolvers(ips []string) []string {
	var out []string
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if net.ParseIP(ip) == nil || ip == fakeDNSIP {
			continue
		}
		out = append(out, ip)
	}
	return out
}
