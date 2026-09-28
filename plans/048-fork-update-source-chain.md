# Fork update-source chain

- [x] Trace panel, Agent, installer, offline-package, compose, frontend, and release-workflow update sources.
- [x] Point operational install and upgrade paths at `ImoLR/FLVXR2` while retaining upstream attribution and history.
- [x] Treat `3.0.27-fork.N` as the stable maintenance line and compare fork revisions numerically without ordering unrelated official versions.
- [x] Require the Agent upgrade path to fetch and validate the matching SHA-256 asset.
- [x] Run focused, full backend/Agent, frontend build, script, API, and release-workflow checks.

Post-commit release step: publish immutable `3.0.27-fork.2` through the existing workflow, without rewriting `3.0.27-fork.1`.

The update-chain tests, Agent suite, frontend build, scripts, Compose files, workflow YAML, release-generation simulation, GitHub API/assets, and both Agent architectures pass. The broader backend suite still reports pre-existing failures in unrelated federation, tunnel, renewal, migration/backup, and monitoring contracts; none of their implementation files are changed here.
