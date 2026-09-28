# 049 Monitor WebSocket Fallback

## Goal

Keep the authenticated administrator realtime stream as the preferred path, but
recover automatically through the existing read-only public realtime stream
when a browser cannot establish or retain the cookie-authenticated WebSocket.

## Scope

- Change only the frontend node-monitoring WebSocket connection lifecycle.
- Do not relax backend authentication or change proxy configuration.
- Preserve the existing polling fallback when neither WebSocket path works.
- Release the fix as `3.0.27-fork.3`; do not alter the `3.0.27-fork.2` tag.

## Checklist

- [x] Prove backend, frontend proxy, and Cloudflare return HTTP 101 for the public stream.
- [x] Prove an authenticated public-domain administrator stream remains stable beyond the backend heartbeat deadline.
- [x] Compare 3.0.27, fork.2, and commit `7ffb617` WebSocket-related source and deployment configuration.
- [x] Implement an explicit WebSocket URL builder and authenticated-to-public fallback.
- [x] Run frontend type/build validation and inspect the generated URL logic.
- [ ] Commit, tag, push, and publish `3.0.27-fork.3` without changing fork.2.
- [ ] Upgrade only the formal FLVXX panel and validate realtime metrics for several minutes.
