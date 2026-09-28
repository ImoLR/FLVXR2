# Baseline fork Agent hardening

- [x] Port the DNAT-scoped masquerade fix to the 3.0.27 fork baseline without importing 3.0.28 features.
- [ ] Port recurring probe-error deduplication and the systemd journal rate-limit safety net as a separate commit.
- [ ] Re-run focused, full-module, namespace networking, and cross-architecture build checks.
- [ ] Publish through the existing release workflow without changing the release architecture.
