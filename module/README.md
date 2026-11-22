# ASIM — SIM VoWiFi Proxy Router (KernelSU module)

Let an overseas SIM card use **WiFi Calling** from mainland China by routing only that SIM's
ePDG (IKE/IPsec) traffic through a root-run mihomo tunnel to a chosen exit node (e.g. UK),
while:

- the SIM stays powered for EAP-AKA (so system WFC registers normally),
- the card is **prevented from camping on any domestic base station** (no roaming),
- every other flow on the phone (other SIM's data, apps, your daily proxy app) is untouched.

## Install

1. Put the `mihomo` linux-arm64 binary at `module/bin/mihomo` (see build.sh check).
2. `./build.sh` → `asim-<version>.zip`
3. KernelSU manager → install zip. Reboot.

## Uninstall

Use KernelSU manager uninstall; `uninstall.sh` restores network-type masks, clears routing rules
and stops the daemon. Nothing persists.

## How it works

System WFC stack (untouched) → IKE to ePDG → our `ip rule` (pref 8999) captures it into
mihomo's TUN → trojan node (UK) → carrier ePDG → IMS registered → dialer/SMS work.

ePDG FQDN is derived from each SIM's IMSI per 3GPP TS 23.003, so any VoWiFi-capable SIM works.
