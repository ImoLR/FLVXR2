# 086 — Agent ignores its own config.json passed via `-C` (fork.37)

## Problem
Node 48 crash-looped after OTA (2026-10-09 10:20Z):
`'TLS' expected a map, got 'float64'`.

- `install.sh` writes `ExecStart=<dir>/<svc> -C <dir>/config.json`, so the AGENT config is handed to
  the gost config parser (`x/config/parsing/parser` → viper → `config.Config`).
- `config.Config.TLS` (`json:"tls"`) collides with the agent key `"tls"` (int), which the agent writes into
  config.json itself (`updateProtocolSettings`) once the panel pushes protocol settings.
- Any restart (reboot, OTA, crash) on such a node is fatal. Fleet-wide latent risk.

## Fix design
Agent side only (`go-gost`):
- `go-gost/x/config/parsing/parser/agentcfg.go` `ResolveAgentConfigFile(cfgFile, agentConfigPath)` (in the parser package because `package main` cannot be unit-tested: its `init()` calls `flag.Parse()`): when `-C` names a file that is
  the agent config — same file as the `config.json` the agent loads (`os.SameFile`), or a JSON object with
  `addr` + `secret` and none of the gost top-level keys (`services`, `chains`, …) — it is NOT given to the gost parser.
  - `<dir of that file>/gost.json` exists and is a valid JSON object → use it explicitly (same services a unit without
    `-C` starts with on our installs, where WorkingDirectory = install dir).
  - otherwise → empty gost config (services arrive from the panel), with a warning when gost.json is unreadable.
  - In both cases the parser's default search (`/etc/gost/`, `$HOME/.gost/`, `.`) is disabled
    (new `parser.Args.SkipDefaultLoad`), so an unrelated gost install on the same host is never picked up.
  - One log line says the agent config was ignored and what is used instead.
- Inline JSON `-C '{...}'`, empty `-C`, and genuine gost config files are unchanged (no `SkipDefaultLoad`).

Why gost.json and not "empty": it is what node 48 effectively runs today without `-C`, and it lets local services
come up before the panel reconnects. Why fall back to empty on a broken gost.json: before this change a `-C config.json`
unit never read gost.json at startup, so a truncated gost.json must not become a new fatal path.
Known limit: if the agent's own config.json also contained gost services they would now be ignored (install.sh never writes that).


## Tasks
- [x] Implement resolver + `SkipDefaultLoad` + wiring in `program.go` / `main.go`
- [x] Unit tests: agent config with `"tls":0`, `"tls":1`, no tls key; gost.json present/absent/broken; genuine gost config; inline JSON; SameFile detection; parser SkipDefaultLoad
- [x] go-gost + touched go-gost/x package tests pass; go-backend baseline still 15 (same set)
- [x] Local binary check: throwaway dir, config.json with `"tls":1`, gost.json with one service → service listens
- [ ] Commit, push branch, tag `3.0.27-fork.37`, CI + release assets verified
- [ ] Prod backup + prune + panel upgrade to fork.37, health + node_metric verified
- [ ] Node 48: record state, OTA to fork.37, reconnect + version
- [ ] Node 48 canary: restore original `-C /etc/flvxx/config.json` unit (config.json has `"tls":1`), restart → online, services listening, IPv6 OK
- [ ] Plan marked complete + summary

## Results
- Unit tests `x/config/parsing/parser` (7 tests; raw parse of `"tls":0/1` reproduces the TLS decode error; mutation check: dropping
  `SkipDefaultLoad` makes the no-gost.json test fail). `x/config/...`, `x/socket`, `x/nftables` pass; agent builds amd64 + arm64.
- go-backend `go test ./...`: 15 failures, identical set to the fork.36 run (no backend change).
- Local binary (in `unshare -n`): config.json with `"tls":1` + gost.json service on 127.0.0.1:18080 → fork.37 build logs the
  "agent config, ignored; loading …/gost.json" warning and listens; the unmodified fork.36 build fatals with
  `'TLS' expected a map, got 'float64'` in the same dir.
