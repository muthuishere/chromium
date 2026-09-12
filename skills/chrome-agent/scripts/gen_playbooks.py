#!/usr/bin/env python3
"""Generate browser playbooks FROM the recipe registry.

Lives with chrome-agent because chrome-agent owns site knowledge (apl ADR-0009):
it runs the recipes, so it is the only thing that can prove a verb exists.
Playbooks name NO identity — a site does not have one; a person chooses it, and
may use two. apl binds browser:<label> to a site, not this file.

Hand-written playbooks drift into fiction: the first cut of this set listed
linkedin:like, x:like and x:repost, none of which exist. Generating from the
registry means a documented verb is a verb that is actually registered.

    python3 gen_playbooks.py <registry-recipes-dir> <out-dir>
"""
import os, re, sys, datetime

KEY = re.compile(r'"([a-z0-9]+):([a-z0-9_-]+)"\s*:')
DESC = re.compile(r'describe\s*:\s*"([^"]+)"')

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
SHARED_TRAP = ("**One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.\n"
               "  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.")


def collect(recipes_dir):
    """key -> (verb, describe, is_write). post.js is the write surface."""
    out = {}
    for fn in sorted(os.listdir(recipes_dir)):
        if not fn.endswith(".js"):
            continue
        text = open(os.path.join(recipes_dir, fn), encoding="utf-8", errors="replace").read()
        is_write = fn == "post.js"
        for m in KEY.finditer(text):
            site, verb = m.group(1), m.group(2)
            if site not in DOMAIN:
                continue
            tail = text[m.end():m.end() + 600]
            d = DESC.search(tail)
            out.setdefault(DOMAIN[site], []).append(
                (f"{site}:{verb}", verb, d.group(1) if d else "", is_write))
    return out


def write(path, body):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    open(path, "w").write(body)


def main():
    recipes_dir, out_dir = sys.argv[1], sys.argv[2]
    today = datetime.date.today().isoformat()
    sites = collect(recipes_dir)
    for domain, entries in sorted(sites.items()):
        reads = [e for e in entries if not e[3]]
        writes = [e for e in entries if e[3]]
        d = os.path.join(out_dir, domain)

        write(os.path.join(d, "meta.md"),
              "---\ndomain: %s\nidentity: browser:deemwar\naliases: %s\nstaged: true\n"
              "last_verified: %s\nsource: generated from the browser-research recipe registry\n---\n\n"
              "# %s\n\nVerbs below are the recipes actually registered for this site. A verb that is\n"
              "not listed does not exist — do not improvise one.\n\n"
              "Capability files: `read.md` · `write.md` · `login.md` · `traps.md`\n"
              % (domain, ALIASES.get(domain, domain.split(".")[0]), today, domain))

        def block(es):
            return "".join("  %s: chrome-agent recipe %s\n" % (v, k) for k, v, _, _ in es)

        rb = block(reads) + "  eval: chrome-agent evalwithcsp '<js>'\n"
        rl = "".join("- `%s` — %s\n" % (k, dsc or "no description in the registry")
                     for k, _, dsc, _ in reads) or "- (no read recipe registered for this site)\n"
        write(os.path.join(d, "read.md"),
              "---\nverbs:\n%s---\n\n# %s — read\n\n%s\n"
              "A read is a **sample**, not a set — these surfaces are personalised and paginated.\n"
              "Say what you actually saw; never imply completeness.\n\n"
              "Confirm which page you are on before believing a read: `goto` then `status`.\n"
              % (rb, domain, rl))

        if writes:
            wl = "".join("- `%s` — %s\n" % (k, dsc or "no description in the registry")
                         for k, _, dsc, _ in writes)
            body = ("---\nverbs:\n%s---\n\n# %s — write\n\n"
                    "**Staged by default.** Every write is prepared and held until `--confirm`.\n"
                    "That is what keeps publishing owner-gated by construction; do not route around it.\n\n"
                    "Confirm the identity first — `browser-for %s` names the handle that owns this\n"
                    "site. Posting from the wrong one is the failure this design exists to prevent.\n\n"
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

        traps = TRAPS.get(domain, [])
        tl = "".join("- %s\n" % t for t in traps + [SHARED_TRAP])
        note = "" if traps else ("\nNo site-specific trap has been recorded yet. That means nobody has\n"
                                 "been bitten and written it down — not that this site is honest.\n")
        write(os.path.join(d, "traps.md"),
              "# %s — traps\n\nWhat this site lies about. Each entry cost someone real time.\n\n%s%s\n"
              "**Verification rule:** read the artifact back from the live page. A 2xx proves nothing\n"
              "here, and on some of these sites neither does a 5xx.\n" % (domain, tl, note))

        print("%-22s %d read, %d write" % (domain, len(reads), len(writes)))


if __name__ == "__main__":
    main()
