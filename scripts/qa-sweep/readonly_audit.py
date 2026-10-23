#!/usr/bin/env python3
"""Plan 133 r2: static read-only audit of candidate POST "read" endpoints before
calling any of them on PROD. For each route it walks the handler body plus
h.<method> calls up to DEPTH levels and lists every repository / server call,
flagging anything that looks like a write, a node command, an outbound call or
a goroutine. Only routes with zero flags may go into the prod allowlist.

  readonly_audit.py ROUTES_TXT PATH [PATH...]
"""
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2] / "go-backend/internal"
H = ROOT / "http/handler"
WRITE = re.compile(r"\b(Create|Insert|Update|Upsert|Delete|Remove|Save|Set[A-Z]|Backfill|Reset|Purge|Mark|Touch|Record|Apply|Sync|Push|Enable|Disable|Pause|Resume|Reconcile|Ensure|Upgrade|Assign|Grant|Revoke|Exec|Commit|Begin|Transfer|Rotate|Increment|Add[A-Z]|Register|Schedule|Trigger|Notify|Send|Invalidate|Refresh|Repair|Migrate|Cleanup|Clean)")
SAFE_METHODS = {"Set", "Header", "WriteJSON", "WriteHeader", "Write"}


def bodies():
    out = {}
    for f in H.glob("*.go"):
        if f.name.endswith("_test.go"):
            continue
        src = f.read_text()
        for m in re.finditer(r"^func \(h \*Handler\) (\w+)\(", src, re.M):
            nxt = re.search(r"^func ", src[m.end():], re.M)
            out[m.group(1)] = (f.name, src[m.start(): m.end() + nxt.start()] if nxt else src[m.start():])
    return out


def audit(name, b, depth=0, seen=None, trail=""):
    seen = seen if seen is not None else set()
    flags, calls = [], []
    if name in seen or name not in b:
        return flags, calls
    seen.add(name)
    fname, body = b[name]
    code = re.sub(r"//[^\n]*", "", body)
    for m in re.finditer(r"h\.(repo|wsServer|quotaGroups|landingSync|metrics)\.(\w+)\(", code):
        call = f"{m.group(1)}.{m.group(2)}"
        calls.append(call)
        if WRITE.search(m.group(2)):
            flags.append(f"{trail}{name}: {call}")
    if re.search(r"\bgo (func|h\.)", code):
        flags.append(f"{trail}{name}: goroutine")
    if re.search(r"http\.(Get|Post|NewRequest)|landingcustom\.New|leikwan\.\w+Client|SendCommand", code):
        flags.append(f"{trail}{name}: outbound/command")
    if depth < 3:
        for callee in sorted(set(re.findall(r"h\.(\w+)\(", code))):
            if callee in ("repo", "wsServer") or callee in SAFE_METHODS:
                continue
            if WRITE.search(callee) and callee in b:
                flags.append(f"{trail}{name}: h.{callee}")
            f2, c2 = audit(callee, b, depth + 1, seen, trail + name + " > ")
            flags += f2
            calls += c2
    return flags, calls


def main():
    routes = {}
    for line in open(sys.argv[1]):
        p, h = line.split()[:2]
        routes[p] = h.split(".", 1)[1]
    b = bodies()
    for path in sys.argv[2:]:
        name = routes.get(path)
        flags, calls = audit(name, b)
        verdict = "READ-ONLY" if not flags else "BLOCK"
        print(f"{verdict:9s} {path} ({name}) repo/ws calls={len(set(calls))}")
        for f in sorted(set(flags))[:12]:
            print("           ", f)


if __name__ == "__main__":
    main()
