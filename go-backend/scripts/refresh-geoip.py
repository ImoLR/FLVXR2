#!/usr/bin/env python3
"""Refresh the embedded DB-IP Country Lite data (CC BY 4.0), using stdlib only."""
import csv
import gzip
import hashlib
import io
import ipaddress
import pathlib
import re
import struct
import sys
import urllib.request


def main():
    if len(sys.argv) != 2 or not re.fullmatch(r"\d{4}-(0[1-9]|1[0-2])", sys.argv[1]):
        sys.exit("Usage: python3 scripts/refresh-geoip.py YYYY-MM")
    version = sys.argv[1]
    url = f"https://download.db-ip.com/free/dbip-country-lite-{version}.csv.gz"
    request = urllib.request.Request(url, headers={"User-Agent": "curl/8.0"})
    with urllib.request.urlopen(request, timeout=90) as response:
        source = response.read()
    tables = {4: [], 6: []}
    with gzip.GzipFile(fileobj=io.BytesIO(source)) as archive:
        for start, end, country in csv.reader(io.TextIOWrapper(archive)):
            first, last = ipaddress.ip_address(start), ipaddress.ip_address(end)
            if first.version != last.version or first > last or not re.fullmatch("[A-Z]{2}", country):
                raise ValueError(f"Invalid DB-IP record: {start},{end},{country}")
            tables[first.version].append((first.packed, last.packed, country.encode("ascii")))
    packed = bytearray(b"FLVXGEO1" + struct.pack(">II", len(tables[4]), len(tables[6])))
    for family in (4, 6):
        previous = None
        for first, last, country in sorted(tables[family]):
            if previous is not None and first <= previous:
                raise ValueError("Overlapping DB-IP ranges")
            packed.extend(first + last + country)
            previous = last
    compressed = gzip.compress(packed, compresslevel=9, mtime=0)
    if len(compressed) > 10_000_000:
        raise ValueError("Embedded country database exceeds 10 MB")
    directory = pathlib.Path(__file__).resolve().parents[1] / "internal" / "geoip"
    destination = directory / "country.db.gz"
    temporary = destination.with_suffix(".tmp")
    temporary.write_bytes(compressed)
    temporary.replace(destination)
    (directory / "DATASET.md").write_text(
        f"# Embedded country geolocation data\n\n"
        f"- Source: [DB-IP IP to Country Lite]({url}), version **{version}**.\n"
        f"- Copyright DB-IP.com; [IP Geolocation by DB-IP](https://db-ip.com).\n"
        f"- License: [Creative Commons Attribution 4.0 International](https://creativecommons.org/licenses/by/4.0/).\n"
        f"- Adaptation: CSV ranges converted to sorted IPv4/IPv6 binary records and gzip compressed; country assignments unchanged.\n"
        f"- Source gzip SHA-256: `{hashlib.sha256(source).hexdigest()}`.\n"
        f"- Embedded gzip SHA-256: `{hashlib.sha256(compressed).hexdigest()}`.\n"
        f"- Records: IPv4 {len(tables[4]):,}, IPv6 {len(tables[6]):,}. Embedded size: {len(compressed):,} bytes.\n\n"
        "This is a geolocation dataset, not a registry-allocation country list. Lite coverage and accuracy are limited; administrators can correct the pre-filled country and city.\n\n"
        "Refresh from `go-backend/` with `python3 scripts/refresh-geoip.py YYYY-MM`, then run `go test ./internal/geoip` and rebuild the backend. Downloads happen only during this explicit refresh, never at runtime.\n",
        encoding="utf-8",
    )
    print(f"DB-IP {version}: {len(tables[4]) + len(tables[6])} records, {len(compressed)} embedded bytes")


if __name__ == "__main__":
    main()
