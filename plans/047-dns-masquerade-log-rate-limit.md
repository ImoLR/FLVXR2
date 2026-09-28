# DNS compatibility and log-volume hardening

- [x] Capture upstream SHAs, dependency manifests, fork merge base, and submodule state.
- [x] Compare FLVX and FLVXR2 DNS, UDP, probe, generated-config, and service-unit paths.
- [x] Trace the systemd-resolved warning in systemd 257.13 and reproduce the packet rewrite in an isolated network namespace.
- [x] Restrict FLVXR2 masquerading to DNAT connections so host-local DNS keeps a loopback source address.
- [x] Add regression coverage for the nftables rule expression and isolated packet path.
- [x] Reduce routine TCP probe logging and deduplicate repeated high-frequency runtime errors while retaining first occurrence and recovery transitions.
- [x] Add a per-service systemd journal rate-limit safety net without changing resolver or host networking configuration.
- [x] Run formatting, focused tests, full module tests where feasible, and document the evidence and residual limitations.
- [x] Revalidate the migrated patch on the new control host with isolated IPv4/IPv6 TCP/UDP, loopback-stub, static-resolver, migration, domain-probe, dedupe, and recovery tests; record unrelated upstream release-gate failures separately.
