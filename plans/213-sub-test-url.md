# 213 — Subscription latency-test URL → Cloudflare

## Why

On 2026-11-05 user usb2 reported that every node in their FLVX subscription showed as timed out in PC Clash. The cause: on all FLVX landings, the 全局·强制 rule 「屏蔽大陆网站」 (geosite:cn) also matches google@cn domains, including `www.gstatic.com/generate_204`, the url-test URL the subscription writes into Clash / Stash / sing-box / Surge / Surfboard.

The data-side fix went live the same day: a 「放行谷歌系」 rule (`geosite:google@cn` → outbound:1) now sits first in 全局·强制. This plan removes the dependency on Google for latency tests altogether, so one landing rule can't make every node look dead.

## Change

- `sublink.TestURL`: `https://www.gstatic.com/generate_204` → `https://cp.cloudflare.com/generate_204` (returns 204 and is not in geosite:cn).
- Golden files updated, URL string only.

## Tasks

- [x] Switch TestURL and update the golden files
- [x] `go test ./internal/sublink/...` passes
- [x] Checked through landing node 「vless 直连」 (Boil Hinet 158) after the rule fix: cp.cloudflare.com 204, www.gstatic.com 204
- [ ] Release with the next batch (panel only, no DB change), then pull a Clash subscription and confirm `url: https://cp.cloudflare.com/generate_204`
