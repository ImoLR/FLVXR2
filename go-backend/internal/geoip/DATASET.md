# Embedded country geolocation data

- Source: [DB-IP IP to Country Lite](https://download.db-ip.com/free/dbip-country-lite-2026-10.csv.gz), version **2026-10**.
- Copyright DB-IP.com; [IP Geolocation by DB-IP](https://db-ip.com).
- License: [Creative Commons Attribution 4.0 International](https://creativecommons.org/licenses/by/4.0/).
- Adaptation: CSV ranges converted to sorted IPv4/IPv6 binary records and gzip compressed; country assignments unchanged.
- Source gzip SHA-256: `097426b8ddae89157d444a59ac1847e873f7943c32d52becc4371c8b0273af80`.
- Embedded gzip SHA-256: `8bc15d81776f265b853bb39bc2eeb921a809941231de17c330e2240e117705f9`.
- Records: IPv4 362,122, IPv6 348,712. Embedded size: 4,298,059 bytes.

This is a geolocation dataset, not a registry-allocation country list. Lite coverage and accuracy are limited; administrators can correct the pre-filled country and city.

Refresh from `go-backend/` with `python3 scripts/refresh-geoip.py YYYY-MM`, then run `go test ./internal/geoip` and rebuild the backend. Downloads happen only during this explicit refresh, never at runtime.
