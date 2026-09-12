#!/usr/bin/env python3
"""promote — turn learned notes and recorded drift into canon, as a REVIEW.

apl ADR-0008 says promotion from learned to canon is deliberate and reviewed, and ships no tooling
for it. Without tooling, everything an agent learns accumulates in a log nobody opens, and canon
ages into fiction while the truth sits two directories away. This makes the review a diff:

    chrome-agent note <domain> "what you learned"   # capture it when you learn it
    chrome-agent promote [<domain>]                 # what is known but not written down
    chrome-agent promote <domain> --apply           # append it into the playbook's kept block

It never rewrites canon -- it only appends below traps.md's keep-marker, the one block the
generator will not touch, so a promoted trap survives the next regeneration.

Sources: ~/.config/chrome-agent/learned/<domain>.ndjson (notes) and drift.ndjson (recipes that
failed verification and armed the learning loop). Both are deduped against the existing traps.md,
so promoting twice is a no-op rather than a second copy.
"""
import json, os, re, sys

HOME = os.path.expanduser("~")
CFG = os.path.join(HOME, ".config", "chrome-agent")
LEARNED = os.path.join(CFG, "learned")
DRIFT = os.path.join(CFG, "drift.ndjson")
SKILL = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PLAYBOOKS = os.path.join(SKILL, "playbooks")
KEEP = "<!-- keep: hand-written below — the generator never touches this -->"
SITE = {"linkedin": "linkedin.com", "x": "x.com", "facebook": "facebook.com",
        "instagram": "instagram.com", "reddit": "reddit.com", "youtube": "youtube.com",
        "hackernews": "news.ycombinator.com"}


def norm(t):
    """Compare on words only: punctuation and wrapping must not create a duplicate."""
    return re.sub(r"[^a-z0-9 ]+", " ", (t or "").lower())[:400].split()


def already_says(traps, text):
    want = norm(text)
    if not want:
        return True
    have = " ".join(norm(traps))
    return " ".join(want[:12]) in have


def notes_for(domain):
    f = os.path.join(LEARNED, domain + ".ndjson")
    if not os.path.exists(f):
        return []
    out = []
    for i, line in enumerate(open(f)):
        line = line.strip()
        if not line:
            continue
        try:
            out.append((i, json.loads(line)))
        except Exception:
            pass
    return out


def drift_for(domain):
    if not os.path.exists(DRIFT):
        return []
    seen, out = set(), []
    for line in open(DRIFT):
        try:
            d = json.loads(line)
        except Exception:
            continue
        key = d.get("key", "")
        if SITE.get(key.split(":")[0]) != domain:
            continue
        sig = (key, d.get("why", ""))
        if sig in seen:                     # the loop records every retry; one entry per failure
            continue
        seen.add(sig)
        out.append(d)
    return out


def traps_text(domain):
    f = os.path.join(PLAYBOOKS, domain, "traps.md")
    return open(f).read() if os.path.exists(f) else ""


def pending(domain, source="all"):
    traps = traps_text(domain)
    items = []
    if source in ("all", "note"):
      for i, n in notes_for(domain):
        if n.get("promoted") or already_says(traps, n.get("text", "")):
            continue
        items.append({"source": "note", "line": i, "ts": n.get("ts"), "text": n.get("text", "")})
    if source not in ("all", "drift"):
        return items
    for d in drift_for(domain):
        text = "`%s` drifted: %s" % (d.get("key"), d.get("why", ""))
        if already_says(traps, text):
            continue
        items.append({"source": "drift", "ts": d.get("ts"), "text": text})
    return items


def apply(domain, items):
    f = os.path.join(PLAYBOOKS, domain, "traps.md")
    if not os.path.exists(f):
        return "no playbook at %s" % f
    t = open(f).read()
    if KEEP not in t:
        t = t.rstrip() + "\n\n" + KEEP + "\n"
    head, tail = t.split(KEEP, 1)
    add = "".join("- %s  _(promoted %s, from %s)_\n"
                  % (i["text"], (i.get("ts") or "")[:10], i["source"]) for i in items)
    open(f, "w").write(head + KEEP + tail.rstrip("\n") + "\n" + add)
    # Mark promoted notes so the next review does not show them again.
    lf = os.path.join(LEARNED, domain + ".ndjson")
    if os.path.exists(lf):
        lines = open(lf).read().splitlines()
        for i in [x["line"] for x in items if x["source"] == "note"]:
            try:
                d = json.loads(lines[i]); d["promoted"] = True
                lines[i] = json.dumps(d, separators=(",", ":"))
            except Exception:
                pass
        open(lf, "w").write("\n".join(lines) + "\n")
    return None


def main():
    argv = sys.argv[1:]
    args = [a for a in argv if not a.startswith("--")]
    do_apply = "--apply" in argv
    # A drift entry is a FAILURE REPORT, not yet a trap -- "post-url required" is a usage mistake,
    # not something the site lies about. --source lets a review promote the notes and leave the
    # drift log to be read by a human, which is the common case.
    source = "all"
    for a in argv:
        if a.startswith("--source="):
            source = a.split("=", 1)[1]
    if "--notes-only" in argv:
        source = "note"
    domains = [args[0]] if args else sorted(
        d for d in os.listdir(PLAYBOOKS) if os.path.isdir(os.path.join(PLAYBOOKS, d)))
    report, total = {}, 0
    for dom in domains:
        items = pending(dom, source)
        total += len(items)
        if not items:
            continue
        report[dom] = items
        if do_apply:
            err = apply(dom, items)
            if err:
                report[dom] = {"error": err}
    print(json.dumps({"applied": do_apply, "source": source, "pending": total, "domains": report},
                     indent=2, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    sys.exit(main())
