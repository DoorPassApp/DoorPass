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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseNetworkServices(t *testing.T) {
	out := "An asterisk (*) denotes that a network service is disabled.\nWi-Fi\n*Thunderbolt Bridge\nUSB 10/100 LAN\n\n"
	require.Equal(t, []string{"Wi-Fi", "USB 10/100 LAN"}, parseNetworkServices(out))
}

func TestParseDNSServers(t *testing.T) {
	require.Empty(t, parseDNSServers("There aren't any DNS Servers set on Wi-Fi.\n"))
	require.Equal(t, []string{"1.1.1.1", "2606:4700:4700::1111"}, parseDNSServers("1.1.1.1\n2606:4700:4700::1111\n"))
}

func TestParseDefaultRoute(t *testing.T) {
	out := `Routing tables

Internet:
Destination        Gateway            Flags               Netif Expire
default            link#23            UCSIg           bridge100      !
default            192.168.110.1      UGScg                 en0
0/1                link#30            UCS                 utun7
127                127.0.0.1          UCS                   lo0
`
	netif, gw := parseDefaultRoute(out, "utun7")
	require.Equal(t, "en0", netif)
	require.Equal(t, "192.168.110.1", gw)
	netif, _ = parseDefaultRoute(out, "en0")
	require.Equal(t, "", netif)
}

func TestFilterResolvers(t *testing.T) {
	require.Equal(t, []string{"192.168.1.1", "fe80::1"},
		filterResolvers([]string{"", "192.168.1.1", fakeDNSIP, "dns.google", "fe80::1"}))
}
