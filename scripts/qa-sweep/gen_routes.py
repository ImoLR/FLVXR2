#!/usr/bin/env python3
"""Plan 133 r2: list every API route (mux.HandleFunc in handler.go + map-registered
landing / landing-egress / leikwan / line-chain routes) as 'PATH Handler.name'."""
import pathlib
import re

H = pathlib.Path(__file__).resolve().parents[2] / "go-backend/internal/http/handler"
out = []
src = (H / "handler.go").read_text()
for m in re.finditer(r'mux\.HandleFunc\("([^"]+)",\s*(?:h\.\w+\()*h\.(\w+)', src):
    out.append((m.group(1), m.group(2)))
for f, prefix, anchor in (("landing.go", "/api/v1/landing/", "func (h *Handler) registerLanding("),
                          ("landing_egress.go", "/api/v1/landing/egress/", "func (h *Handler) registerLandingEgress("),
                          ("leikwan.go", "/api/v1/leikwan/", "func (h *Handler) registerLeiKwan("),
                          ("line_chain.go", "/api/v1/forward/chain/", "func (h *Handler) registerLineChain(")):
    s = (H / f).read_text()
    i = s.index(anchor)
    j = s.index("for path, fn := range", i) if f != "landing_egress.go" else s.index("for path, fn := range routes", i)
    block = s[i:j + 2000] if f != "landing_egress.go" else s[i:j]
    block = block[:block.index("{\n", block.index("map[string]http.HandlerFunc")) + 2 + block[block.index("map[string]http.HandlerFunc"):].index("}") - 0] if False else block
    mp = block[block.index("map[string]http.HandlerFunc"):]
    mp = mp[:mp.index("\n\t}") if f == "landing_egress.go" else mp.index("} {")]
    for m in re.finditer(r'"([\w/\-]+)":\s*(?:h\.\w+\(\w+,\s*)?h\.(\w+)', mp):
        out.append((prefix + m.group(1), m.group(2)))
for p, n in sorted(set(out)):
    print(p, "Handler." + n)
