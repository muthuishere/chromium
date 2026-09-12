#!/usr/bin/env python3
"""sites — resolve, list, validate and install the per-domain site definitions (ADR 0004).

This is the ONE place the resolution order lives:

    $CHROME_AGENT_SITES            explicit override (tests, CI)
    ~/.config/chrome-agent/sites/  INSTALLED copy — editable, wins
    <skill>/sites/                 SHIPPED copy — the embedded asset

The installed copy winning is the whole point: when a site re-skins at 2am on a server, the fix is
one JSON file in ~/.config, with no redeploy and no repo. `sync` puts the shipped files there and
REFUSES to clobber a file you changed unless you say --force, because silently reverting an
operator's hotfix is the same class of failure as silently ageing a last_verified stamp.

Verbs:
    resolve <domain>            the merged definition, as JSON (exit 1 if unknown)
    field   <domain> a.b.c      one field, raw (for shell)
    list [--json]               every known domain, where it came from, and its status
    validate [<file>...]        schema + probe sanity; exit 1 on any error
    sync [--force] [--dry-run]  shipped -> installed
    path <domain>               which file would be used
"""
import json, os, sys

HOME = os.path.expanduser("~")
SKILL = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SHIPPED = os.path.join(SKILL, "sites")
INSTALLED = os.path.join(HOME, ".config", "chrome-agent", "sites")
OVERRIDE = os.environ.get("CHROME_AGENT_SITES") or ""

REQUIRED = ["domain", "home", "login", "auth", "status"]
STATUSES = ("verified", "unverified")


def dirs():
    """Highest priority first."""
    return [d for d in (OVERRIDE, INSTALLED, SHIPPED) if d and os.path.isdir(d)]


def norm(d):
    d = (d or "").strip().lower()
    if "://" in d:
        d = d.split("://", 1)[1]
    d = d.split("/")[0]
    return d[4:] if d.startswith("www.") else d


def find(domain):
    domain = norm(domain)
    for d in dirs():
        p = os.path.join(d, domain + ".json")
        if os.path.exists(p):
            return p
    # An alias is a second name for the same file, so it costs a scan -- worth it, since a human
    # types "twitter" and "hn" far more often than the canonical host.
    for d in dirs():
        for fn in sorted(os.listdir(d)):
            if not fn.endswith(".json"):
                continue
            try:
                j = json.load(open(os.path.join(d, fn)))
            except Exception:
                continue
            if domain in [a.lower() for a in j.get("aliases", [])]:
                return os.path.join(d, fn)
    return None


def load(domain):
    p = find(domain)
    if not p:
        return None, None
    try:
        return json.load(open(p)), p
    except Exception as e:
        sys.stderr.write("sites: %s is not valid JSON: %s\n" % (p, e))
        return None, p


def dig(obj, path):
    cur = obj
    for part in path.split("."):
        if isinstance(cur, dict) and part in cur:
            cur = cur[part]
        else:
            return None
    return cur


def problems(j, path):
    """What is wrong with this definition. Empty list = usable."""
    out = []
    for k in REQUIRED:
        if not j.get(k):
            out.append("missing required field: %s" % k)
    if j.get("status") not in STATUSES:
        out.append("status must be one of %s (got %r)" % (list(STATUSES), j.get("status")))
    if norm(j.get("domain", "")) != norm(os.path.basename(path)[:-5]):
        out.append("domain %r does not match filename %s" % (j.get("domain"), os.path.basename(path)))
    probe = dig(j, "auth.probe_js") or ""
    if not probe.strip():
        out.append("auth.probe_js is empty — a site with no probe can never report better than unknown")
    else:
        if "return" not in probe:
            out.append("auth.probe_js never returns — it must return {signed_in, as?}")
        # These are the rules the schema calls load-bearing. A probe that navigates or clicks is
        # not a probe, it is an action, and it will be run against a live logged-in session.
        for bad, why in (("location.href =", "probe must not navigate"),
                         ("location.assign", "probe must not navigate"),
                         ("location.replace", "probe must not navigate"),
                         (".click(", "probe must not click"),
                         ("document.write", "probe must not write to the page")):
            if bad in probe:
                out.append("auth.probe_js: %s (found %r)" % (why, bad))
        if "document.cookie" in probe and "try" not in probe:
            out.append("auth.probe_js reads document.cookie without try/catch — it raises "
                       "SecurityError on an opaque origin and the probe would throw")
    for k in ("login", "logout"):
        v = j.get(k)
        if v is not None and not isinstance(v, dict):
            out.append("%s must be an object" % k)
    lo = j.get("logout") or {}
    if lo and lo.get("method") not in (None, "url", "dom", "cookies"):
        out.append("logout.method must be url|dom|cookies (got %r)" % lo.get("method"))
    if lo.get("method") == "url" and not lo.get("url"):
        out.append("logout.method=url needs logout.url")
    if lo.get("method") == "dom" and not lo.get("selector"):
        out.append("logout.method=dom needs logout.selector")
    return out


def cmd_list(as_json):
    seen, rows = set(), []
    for d in dirs():
        origin = ("override" if d == OVERRIDE else "installed" if d == INSTALLED else "shipped")
        for fn in sorted(os.listdir(d)):
            if not fn.endswith(".json") or fn in seen:
                continue
            seen.add(fn)
            try:
                j = json.load(open(os.path.join(d, fn)))
            except Exception:
                rows.append({"domain": fn[:-5], "status": "BROKEN JSON", "from": origin,
                             "read": "", "writes": 0})
                continue
            rows.append({"domain": j.get("domain", fn[:-5]), "status": j.get("status", "?"),
                         "from": origin, "read": dig(j, "read.verb") or "",
                         "writes": len(j.get("write") or []), "source": j.get("source", "")})
    rows.sort(key=lambda r: r["domain"])
    if as_json:
        print(json.dumps(rows, indent=2))
    else:
        for r in rows:
            print("  %-24s %-11s %-10s read=%-28s writes=%d"
                  % (r["domain"], r["status"], r["from"], r["read"] or "-", r["writes"]))
    return 0


def cmd_validate(files):
    if not files:
        files = []
        for d in dirs():
            files += [os.path.join(d, f) for f in sorted(os.listdir(d)) if f.endswith(".json")]
    bad = 0
    for f in files:
        try:
            j = json.load(open(f))
        except Exception as e:
            print("FAIL %s: not valid JSON: %s" % (f, e)); bad += 1; continue
        errs = problems(j, f)
        if errs:
            bad += 1
            print("FAIL %s" % f)
            for e in errs:
                print("     - %s" % e)
        else:
            print("ok   %-40s %s" % (os.path.basename(f), j.get("status")))
    print("\n%d file(s) checked, %d bad" % (len(files), bad))
    return 1 if bad else 0


def cmd_sync(force, dry):
    os.makedirs(INSTALLED, exist_ok=True)
    acted = {"installed": [], "kept": [], "replaced": [], "unchanged": []}
    for fn in sorted(os.listdir(SHIPPED)):
        if not fn.endswith(".json"):
            continue
        src, dst = os.path.join(SHIPPED, fn), os.path.join(INSTALLED, fn)
        new = open(src).read()
        if not os.path.exists(dst):
            if not dry:
                open(dst, "w").write(new)
            acted["installed"].append(fn); continue
        cur = open(dst).read()
        if cur == new:
            acted["unchanged"].append(fn)
        elif force:
            if not dry:
                open(dst, "w").write(new)
            acted["replaced"].append(fn)
        else:
            acted["kept"].append(fn)
    print(json.dumps({"installed_dir": INSTALLED, "dry_run": dry, "force": force, **acted}, indent=2))
    if acted["kept"]:
        sys.stderr.write("sites sync: %d locally-edited file(s) left alone — "
                         "--force replaces them\n" % len(acted["kept"]))
    return 0


def main(argv):
    if not argv:
        print(__doc__); return 1
    cmd, rest = argv[0], argv[1:]
    if cmd == "resolve":
        j, p = load(rest[0] if rest else "")
        if not j:
            return 1
        print(json.dumps(j)); return 0
    if cmd == "field":
        j, _ = load(rest[0] if rest else "")
        if not j:
            return 1
        v = dig(j, rest[1]) if len(rest) > 1 else None
        if v is None:
            return 1
        print(v if isinstance(v, str) else json.dumps(v)); return 0
    if cmd == "path":
        p = find(rest[0] if rest else "")
        if not p:
            return 1
        print(p); return 0
    if cmd == "list":
        return cmd_list("--json" in rest)
    if cmd == "validate":
        return cmd_validate([a for a in rest if not a.startswith("--")])
    if cmd == "sync":
        return cmd_sync("--force" in rest, "--dry-run" in rest)
    sys.stderr.write("sites: unknown verb %r\n" % cmd); return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
