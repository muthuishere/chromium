#!/usr/bin/env python3
"""Generate browser playbooks FROM the recipe registry.

Lives with chrome-agent because chrome-agent owns site knowledge (apl ADR-0009):
it runs the recipes, so it is the only thing that can prove a verb exists.
Playbooks name NO identity — a site does not have one; a person chooses it, and
may use two. apl binds browser:<label> to a site, not this file.

Hand-written playbooks drift into fiction: the first cut of this set listed
linkedin:like, x:like and x:repost as registry recipes, which they are not --
they are chrome-agent's own DOM verbs. Generating means a documented verb is a
verb something can actually run, and says which half runs it.

The verb list comes from `chrome-agent recipes --json`, not from parsing someone else's
source. The earlier version regex-matched keys out of registry.js across a repo boundary and
inferred "is this a write?" from the FILE a key lived in (post.js) -- which broke the moment a
write lived elsewhere, and missed every verb chrome-agent implements itself (the reaction verbs
are CLI-side, so the registry-only view concluded liking was impossible and said so in a
playbook). The CLI knows; ask it.

    python3 gen_playbooks.py <out-dir>
"""
import json, os, subprocess, sys

CLI = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "chrome-agent")

# recipe prefix -> the domain it drives
DOMAIN = {
    "linkedin": "linkedin.com", "x": "x.com", "facebook": "facebook.com",
    "instagram": "instagram.com", "reddit": "reddit.com", "youtube": "youtube.com",
    "hackernews": "news.ycombinator.com",
}
ALIASES = {
    "linkedin.com": "linkedin, li, company page, personal profile",
    "x.com": "x, twitter, tweet, timeline, retweet",
    "facebook.com": "facebook, fb, meta",
    "instagram.com": "instagram, ig, insta, reel",
    "reddit.com": "reddit, subreddit, r/",
    "youtube.com": "youtube, yt, video, channel",
    "news.ycombinator.com": "hacker news, hackernews, hn, ycombinator",
}
# Traps that cost real time. Empty list = none recorded yet; say so, don't invent.
TRAPS = {
    "linkedin.com": [
        "**Strict CSP kills `evalAsync`** — `script-src` without `unsafe-eval`. A plain eval fails\n  silently: no error, no result. Use `evalwithcsp`.",
        "**The comment API returns HTTP 500 while succeeding.** The comment IS created. Retrying\n  double-posts. Verify the artifact, never the status code.",
        "**Logged out looks like a page, not an error** — it redirects to\n  `/login/?session_redirect=…`. See `login.md`.",
    ],
    "x.com": [
        "**Strict CSP** — `evalAsync` fails silently; use `evalwithcsp`.",
        "**Rate limiting appears as an empty timeline**, not an error. Empty never means \"nothing\n  there\"; it means try later.",
    ],
    "news.ycombinator.com": [
        "**HN serves HTTP 200 on a dead or flagged post.** The status code is not evidence. Open the\n  item and read it back as a logged-out visitor would see it.",
    ],
}
# Everything after this line in traps.md is hand-written and survives regeneration.
KEEP = "<!-- keep: hand-written below — the generator never touches this -->\n"
SHARED_TRAP = ("**One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.\n"
               "  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.")


def collect():
    """domain -> [(key, verb, describe, is_write, source)], straight from the CLI."""
    out = {}
    raw = subprocess.run([CLI, "recipes", "--json"], capture_output=True, text=True, check=True).stdout
    for r in json.loads(raw):
        domain = DOMAIN.get(r["site"])
        if not domain:
            continue
        out.setdefault(domain, []).append(
            (r["key"], r["verb"], r["describe"], r["write"], r["source"]))
    return out


def write(path, body):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    open(path, "w").write(body)


def main():
    out_dir = sys.argv[1]
    sites = collect()
    for domain, entries in sorted(sites.items()):
        reads = sorted(e for e in entries if not e[3])
        writes = sorted(e for e in entries if e[3])
        d = os.path.join(out_dir, domain)

        # NO identity line. A site is reachable as more than one person, and the moment a default
        # identity appears in a playbook the multi-identity design is single-identity in practice.
        # apl owns that binding (`apl identity set browser:<label> --site <domain>`).
        #
        # last_verified starts at `never`: a generation date proves a generator ran, not that the
        # site still answers. Only `chrome-agent verify <domain>` stamps a real date, on success.
        # An existing stamp is preserved -- regenerating the verb list must not un-verify a site.
        meta = os.path.join(d, "meta.md")
        stamp = "never (run: chrome-agent verify %s)" % domain
        if os.path.exists(meta):
            for line in open(meta):
                if line.startswith("last_verified:"):
                    prev = line.split(":", 1)[1].strip()
                    # Only a stamp written by `verify` is kept -- it carries how it was proven,
                    # e.g. "2026-09-12 (recipe:linkedin:feed)". A bare date is an old generation
                    # stamp: it proves nothing, so it goes back to `never` rather than ageing on.
                    if "(" in prev:
                        stamp = prev
                    break
        write(meta,
              "---\ndomain: %s\naliases: %s\nstaged: true\n"
              "last_verified: %s\nsource: generated from `chrome-agent recipes --json`\n---\n\n"
              "# %s\n\nVerbs below are the ones that actually exist for this site — registry recipes\n"
              "plus chrome-agent's own verbs. A verb that is not listed does not exist — do not\n"
              "improvise one.\n\n"
              "Capability files: `read.md` · `write.md` · `login.md` · `traps.md`\n"
              % (domain, ALIASES.get(domain, domain.split(".")[0]), stamp, domain))

        def block(es):
            return "".join(
                "  %s: %s\n" % (v, ("chrome-agent recipe %s" % k) if src == "registry"
                                    else ("chrome-agent %s %s" % (k.split(":")[0], v)))
                for k, v, _, _, src in es)

        rb = block(reads) + "  eval: chrome-agent evalwithcsp '<js>'\n"
        rl = "".join("- `%s` — %s\n" % (k, dsc or "no description recorded")
                     for k, _, dsc, _, _ in reads) or "- (no read recipe registered for this site)\n"
        write(os.path.join(d, "read.md"),
              "---\nverbs:\n%s---\n\n# %s — read\n\n%s\n"
              "A read is a **sample**, not a set — these surfaces are personalised and paginated.\n"
              "Say what you actually saw; never imply completeness.\n\n"
              "Confirm which page you are on before believing a read: `goto` then `status`.\n"
              % (rb, domain, rl))

        if writes:
            wl = "".join("- `%s` — %s%s\n" % (k, dsc or "no description recorded",
                                             "" if src == "registry" else "  _(chrome-agent verb, not a registry recipe)_")
                         for k, _, dsc, _, src in writes)
            body = ("---\nverbs:\n%s---\n\n# %s — write\n\n"
                    "**Staged by default.** Every write is prepared and held until `--confirm`.\n"
                    "That is what keeps publishing owner-gated by construction; do not route around it.\n\n"
                    "Confirm the identity first — apl names the handle that owns this site\n"
                    "(`apl identity get --site %s`). Posting from the wrong one is the failure this\n"
                    "design exists to prevent.\n\n"
                    "%s\nVerify by reading the artifact back from the live page. See `traps.md`.\n"
                    % (block(writes), domain, domain, wl))
        else:
            body = ("# %s — write\n\n**No write recipe is registered for this site.** Reads only.\n\n"
                    "Do not improvise a write by driving the DOM: that is how selectors rot and how\n"
                    "an unreviewed action gets published under the owner's name. If a write is needed,\n"
                    "add a recipe to the registry first, then regenerate this playbook.\n" % domain)
        write(os.path.join(d, "write.md"), body)

        write(os.path.join(d, "login.md"),
              "# %s — login\n\n**Manual, by a human, in the profile. Always** (ADR-0008).\n\n"
              "Nothing here stores, types or automates a password, and nothing touches 2FA.\n"
              "Automated sign-in is the most reliable way to get an account restricted, and a stored\n"
              "credential would put a secret inside an agent's context.\n\n"
              "**Expired session:** reads land on a login wall instead of content. That is the signal —\n"
              "not an error, not an empty page.\n\n"
              "**What to do:** stop and name the profile that needs a human.\n\n"
              "```\napl accounts --check\n```\n\nDo not retry in a loop meanwhile.\n" % domain)

        # TRAPS: the generator owns everything ABOVE the keep-marker and nothing below it.
        # Only the verb surface has a source of truth, so traps stay hand-written -- which used to
        # mean a trap typed straight into this file was silently destroyed by the next run. The
        # marker makes that block durable, and `chrome-agent promote` appends into it.
        traps = TRAPS.get(domain, [])
        tl = "".join("- %s\n" % t for t in traps + [SHARED_TRAP])
        note = "" if traps else ("\nNo site-specific trap has been recorded yet. That means nobody has\n"
                                 "been bitten and written it down — not that this site is honest.\n")
        tpath = os.path.join(d, "traps.md")
        kept = ""
        if os.path.exists(tpath):
            prev = open(tpath).read()
            if KEEP in prev:
                kept = prev.split(KEEP, 1)[1]
            else:
                # A pre-marker traps.md may hold hand-written text. Keep only the lines this
                # generator would not have written -- copying the whole file back would duplicate
                # the generated half, and dropping it would lose the part that cost someone time.
                mine = set(l.strip() for l in (tl + note).splitlines() if l.strip())
                extra = [l for l in prev.splitlines()
                         if l.strip() and l.strip() not in mine
                         and not l.startswith(("# ", "**Verification rule:**"))
                         and "cost someone real time" not in l
                         and "proves nothing" not in l and "neither does a 5xx" not in l]
                if extra:
                    kept = ("\n## Carried over from a pre-marker traps.md — fold these in or delete\n\n"
                            + "\n".join(extra) + "\n")
        write(tpath,
              "# %s — traps\n\nWhat this site lies about. Each entry cost someone real time.\n\n%s%s\n"
              "**Verification rule:** read the artifact back from the live page. A 2xx proves nothing\n"
              "here, and on some of these sites neither does a 5xx.\n\n%s%s"
              % (domain, tl, note, KEEP, kept or "\n"))

        print("%-22s %d read, %d write" % (domain, len(reads), len(writes)))


if __name__ == "__main__":
    main()
