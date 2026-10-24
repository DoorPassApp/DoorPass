# DoorPass privacy policy

DoorPass sends nothing until you turn protection on, and it never sends anything to its developers. It has no analytics, telemetry, crash reporting or update checks.

## What is sent while protection is on

Your computer's DNS lookups (the names of the sites and services it connects to) are sent, encrypted with DNS-over-HTTPS, to the DoH server selected in DoorPass. The default is Google Public DNS; you can pick another listed server or enter your own before turning protection on, and change it at any time. The server you choose sees those lookups and its own privacy policy applies, for example:

- Google Public DNS: https://developers.google.com/speed/public-dns/privacy
- Cloudflare 1.1.1.1: https://developers.cloudflare.com/1.1.1.1/privacy/public-dns-resolver/
- Quad9: https://quad9.net/privacy/policy/

To reach the DoH server by name, DoorPass may also send the server's own hostname to the DNS servers your network already uses.

All other traffic goes directly to where your apps send it, as it would without DoorPass. In Full mode it passes through DoorPass's tunnel on your own computer, which only splits the TLS handshake of HTTPS connections; it is not sent anywhere else.

## What stays on your computer

- Settings and the pairing token between the app and its helper, in your user profile.
- The optional query log (off by default): recent domain names, kept in memory by the helper and cleared when you turn the log off or the helper restarts.
- The helper's log file (`/Library/Logs/DoorPassHelper.log` on macOS, `C:\ProgramData\DoorPassHelper\helper.log` on Windows), which records only start, connect, disconnect and errors, never domain names.

## Third-party components

DoorPass is based on Jigsaw's Intra. Intra's optional Firebase analytics and "Choir" blocking reports are Android-only and are not part of DoorPass. On Windows, DoorPass uses the Wintun network adapter from WireGuard, which sends nothing.
