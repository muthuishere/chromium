# chrome-agent — what to build next

Written 2026-09-12 after apl (`deemwar-products/apl`) stopped vendoring this script and started
merely referring to it (apl ADR-0009); **P0 and P1 were built the same day** and are recorded below
as shipped, with how each was proven. Everything here was found while wiring apl up, not imagined.

The split: chrome-agent owns site knowledge and the browser; apl owns which identity acts and joins
the two. Anything that is "how a site behaves" belongs here; anything that is "who is acting" does
not.

---

## Shipped 2026-09-12

### P0 — `FORK` is no longer hardcoded

`CHROME_AGENT_FORK`, defaulting to the owner's checkout, so nothing on this machine changed. A
missing fork exits **3** naming the absent file; an unbuilt fork says so and gives the `autoninja`
line. The preflight runs at the top of the dispatcher, not only inside `cj` — a `die` inside `cj`
runs in a command substitution, so its exit never reached the caller and its JSON was swallowed by
whatever was capturing stdout.

**No stock-Chrome degraded mode, deliberately.** The spool/tabId protocol is a fork patch: stock
Chrome would not lose undetectability, it would lose every verb. If that changes it belongs behind
its own engine flag, never behind a silent fallback.

### P1.2 — `recipes --json`

`recipes-list.mjs` is now the single source of "what verbs exist": registry recipes **and**
chrome-agent's own DOM verbs, each entry carrying `{key, site, verb, describe, write, world,
source, cli}`. 46 entries, 20 of them writes. `gen_playbooks.py` calls the CLI instead of
regex-parsing `registry.js` across a repo boundary, and `write` comes from the registry's own flag
rather than from which file a key happened to live in.

That merge also killed the P2 claim below it: the registry has no reaction recipe for any site, but
this CLI has `linkedin:like`, `x:like`, `x:repost`, `reddit:upvote` as self-verifying DOM verbs. A
generator that saw only the registry concluded reacting was impossible and wrote that into a
playbook. Both sources, one list, each entry naming its own.

### P1.3 — `auth <domain>` (and `login`, `profile create`)

```
chrome-agent auth  <domain>   # read-only {signed_in, as?} — exit 0 signed in, 2 not
chrome-agent login <domain>   # opens the window there for a HUMAN; types NOTHING, ever
chrome-agent profile create <dir>
```

**Domain-scoped, never profile-scoped** — a profile is signed into many sites, so one boolean for
the profile is a lie the moment one cookie expires while another holds.

Proven live: `linkedin.com → Suguna Paulraj`, `x.com → deemwarmonads`, `github.com → deemwario`,
`news.ycombinator.com → deemwar`; `youtube.com` and `reddit.com` correctly report signed out.

Two traps, both hit while building this:

- **`li_at` and `auth_token` are HttpOnly.** The first cut gated on `document.cookie` and reported
  a perfectly live LinkedIn session as logged out — the same false-negative that once cost 44 hours
  of posting. Ask the site's API (`/voyager/api/me`) and let the browser attach the cookie.
- **reddit's `/api/v1/me.json` returns 200 when signed out**, body `{"features":{…}}` with no
  `name`. The status code is not the verdict. Same shape as the HN trap, different site.
- YouTube: the readable cookies (`PREF`, `__Secure-3PAPISID`) are present when signed **out**; the
  meaningful ones are HttpOnly. `ytcfg.data_.LOGGED_IN` is YouTube's own answer — use it.

### P1.4 — documented exit codes

`0 ok · 1 usage · 2 not signed in · 3 browser unreachable · 4 site refused`, machine-readable via
`chrome-agent exit-codes --json`. 2 vs 3 is the split that matters: "a human must sign in" and
"start the browser" are different jobs, and a caller that cannot tell them apart retries the wrong
one forever.

### P4.1 — `verify <domain>` makes `last_verified` mean something

Runs the site's real read recipe (or its auth probe where no read recipe exists — HN) and stamps
`last_verified: <date> (<how>)` **only on success**. The generator preserves a stamp that carries a
`(how)` and resets a bare date back to `never`: a generation date proves a generator ran.

The first cut of this verb **stamped three lies**, and they are the reason it checks three things
now. `_ensure_origin` navigates only for the four sites it knows, so `verify instagram.com` ran
`instagram:post` against whatever page the tab was already on — it scraped **reddit**, found images,
and reported instagram verified. `facebook:feed` returned `postCount: 0` with no error and passed
too. So verify now navigates to the domain first, rejects a result whose own `url` is not that
domain, and rejects an empty read: "the verb ran" is not "the verb works".

Four sites went straight back to `never` after that — three of which had been reading as a fresh
green date. The second pass then found that three of those four `never`s were the *probe's* fault,
not the site's; see **`verify` fixtures** below for the state that survived both fixes.

### P3 — two of the sharp edges

- **Stale `SingletonLock`**: `up` now reads the lock's `<host>-<pid>` target, and if no live process
  owns it clears `Singleton{Lock,Cookie,Socket}` and says so. The failure mode was silence —
  chromium printed `Opening in existing browser session.`, exited 0, and nothing serviced the spool.
- **Launch failures are named, not waited out**: `No rendezvous client` (a background/cron/ssh
  context with no Mach bootstrap) now exits 3 pointing at `launchctl asuser` **or**
  `CHROMIUM_AGENT_HEADLESS=1`, which the launcher already supports and which is the real answer for
  cron.
- **The ledger carries identity**: `actions.ndjson` lines now include `profile` and `agent`. One
  global file with two profiles in use could not say who posted.
- Spool keying is unchanged in behaviour but now derives from ONE function (`_spool_for`), so
  `profile create` reports the spool the next `up` will really use. Both historic bugs re-tested:
  trailing slash (`~/chrome-agent-profile/` → the default spool) and basename collision
  (`~/work/profile` ≠ `~/personal/profile`).

---

## Also shipped, second pass 2026-09-12

### P2.1 — Hacker News can read now

`hackernews:top [n]` and `hackernews:item <url-or-id>` are CLI-side DOM reads (HN has no API worth
replaying and no CSP to fight). `item` returns comments with depth **and** the two states a 200
hides: `[flagged]` and `[dead]`. That closes the contradiction where HN's own traps file demanded
"open the item and read it back" from a site with no read verb. Proven: front page 30/30,
`item 49671329` → 235 comments, depth-tagged.

### P2.3 — `reddit:listing` was building a hostname, not a path

`base.replace(/\/$/, "")` strips the only path segment a listing ROOT has, so `base + ".json"`
produced `https://www.reddit.com.json` — a different **host**. DNS fails, `fetch` rejects with
`TypeError: Failed to fetch`, and nothing in that message says the URL was wrong, so it read as a
broken browser for weeks. Every listing root hit it; deeper permalinks were fine, which is why it
survived. Fixed in browser-research (`aca4eb5`); reddit now verifies green.

### P4.2 — `promote`: learned → canon as a review

```
chrome-agent note <domain> "<what you learned>"
chrome-agent promote [<domain>] [--notes-only] [--apply]
```

ADR-0008 requires promotion to be deliberate and reviewed and shipped no tooling, so everything
learned accumulated in a log nobody opens. `promote` diffs notes (and recorded drift) against the
playbook and appends only below the keep-marker — deduped, so promoting twice is a no-op.

**Drift is offered but not promoted by default.** A drift line is a failure report, not yet a trap:
`reddit:upvote drifted: post-url required` is a usage mistake, not something the site lies about.
Three such entries are sitting in the review queue right now, deliberately unapplied.

Six real traps from today went through the loop into canon: the two reddit ones above, `li_at` and
`auth_token` being HttpOnly, YouTube's readable-when-signed-out cookies, and HN's 200-on-dead.

### P4.3 — a trap written into a generated file is no longer destroyed

`traps.md` now carries `<!-- keep: hand-written below — the generator never touches this -->`.
The generator owns everything above it and nothing below. A pre-marker file's non-generated lines
are carried into a "fold these in or delete" block rather than dropped. Regeneration is idempotent
and the promoted traps survive it — both checked.

### P3.1 — headless is proven, cron is not

`CHROMIUM_AGENT_HEADLESS=1` launched a throwaway profile end to end: `up` → `undetected` →
`hackernews top` returned 3 posts, no window. So the answer `up` now points at when it hits
`No rendezvous client` is real. **It was proven from a shell, not from cron** — a launchd job has a
different bootstrap namespace, which is the whole reason that error exists. Do not promise cron
until someone runs it there.

### `verify` fixtures — the last of the false reds

Three of the four `never` stamps were the probe's fault, not the site's: these recipes scrape THE
CURRENT PAGE, so `youtube:channel-videos` on the homepage returns 0 items and looks like drift.
Each domain now names the verb **and the page**; a chrome-agent verb is run through the CLI rather
than `recipe()`. One injection got caught on the way: the verb table's `cli` field is a usage
template, and `[n]` reached the page as JS (`ReferenceError: n is not defined`), so placeholders are
stripped and `hackernews top` accepts digits only.

| site | verified | by |
|---|---|---|
| linkedin.com | yes | `recipe:linkedin:feed` |
| x.com | yes | `recipe:x:timeline` |
| reddit.com | yes | `recipe:reddit:listing` (after the fix above) |
| youtube.com | yes | `recipe:youtube:channel-videos` from a results page |
| instagram.com | yes | `recipe:instagram:profile` from a profile page |
| news.ycombinator.com | yes | `recipe:hackernews:top` |
| facebook.com | **no** | feed read returns 0 items — the profile is signed out of facebook |

facebook is the one honest red: `auth facebook.com` agrees, and no probe should turn that green.

---

## Still open

- **facebook.com and instagram.com remain read-only, youtube too.** Their `write.md` says so. Adding
  a write means adding a recipe, not driving the DOM from a lane.
- **Cron/launchd is unproven** (see P3.1). Run the headless path from an actual launchd job before
  anything depends on it.
- **The drift review queue is unstructured.** `promote` can only offer a drift line verbatim; there
  is no way to edit one into a trap without hand-writing it. Fine for three entries, not for thirty.
- **`auth` covers eight domains.** Anything else exits 1 rather than guessing — deliberate, but the
  table has to grow with the playbooks.
- **The ledger is 14 MB and has no rotation.** It now carries identity, which makes it worth keeping
  and therefore worth rotating.

---

## What belongs here, and what does not

**Here:** verbs, traps, login procedure, profile/spool mechanics, the fork dependency, anything
provable by running the browser.

**Not here:** which `browser:<label>` may act on a site. That is a statement about an identity, not
about the site — apl owns it (`apl identity set browser:<label> --site <domain>`), and playbooks
name none. A site is reachable as more than one person; the moment a default identity lands in a
playbook, the multi-identity design silently becomes single-identity. (The generator used to write
`identity: browser:deemwar` into every `meta.md` — exactly that failure, now removed.)

## The apl-facing contract — now filled

ADR-0007 makes `login` a capability and its table had one blank row for `browser:<label>`. The three
verbs above fill it with no new concept on apl's side: the browser adapter implements the existing
`PrepareLogin` interface and `exec`s this CLI, exactly as `apl login whatsapp:biz` execs `wacli
auth`. And because `login`/`auth` are domain-scoped, `apl login browser:deemwar --site linkedin.com`
means precisely one thing.

`profile create` is the genuinely profile-scoped one: apl should never `mkdir` a profile itself — a
bare directory is a logged-out profile that looks configured.
