# Panel Agent version lock

- [x] Inventory every Panel-generated Agent install, offline-install, single-upgrade, and batch-upgrade path.
- [x] Lock all generated Agent release assets to the running Panel `FLUX_VERSION`.
- [x] Remove `latest` fallback when `install.sh` downloads an explicitly resolved Agent version.
- [x] Add focused tests for exact fork version URLs, forbidden fallback sources, and mismatched requested versions.
- [x] Run focused backend, script, and build verification.
- [ ] Commit and publish the independent `3.0.27-fork.5` release without changing earlier tags.
- [ ] Verify all release assets before changing production.
- [ ] Upgrade only the production FLVXX frontend/backend and verify the live generated commands.
- [ ] Record the final AWS HK native amd64 install command without executing it.
