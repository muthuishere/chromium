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

Honest state after that fix:

| site | verified | by |
|---|---|---|
| linkedin.com | yes | `recipe:linkedin:feed` |
| x.com | yes | `recipe:x:timeline` |
| news.ycombinator.com | yes | `auth` (no read recipe exists) |
| reddit.com | **no** | `reddit:listing` → `TypeError: Failed to fetch` |
| facebook.com | **no** | feed read returns 0 items (signed out, or drift) |
| instagram.com | **no** | read returns 0 items |
| youtube.com | **no** | `youtube:channel-videos` returns 0 from the homepage — it likely wants a channel in `opts`, which `verify` has no way to supply yet |

Four `never` stamps is the verb working. Three of those four previously read as a fresh green date.

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

## Still open

### P2 — real capability gaps

- **news.ycombinator.com can write but cannot read.** It posts and comments and has no read recipe,
  which collides with its own verification rule ("HN serves 200 on a dead post — read the item
  back"). The one gap where a site's traps file demands a verb the site does not have.
- **facebook.com, instagram.com, youtube.com are read-only.** Their `write.md` now says so rather
  than leaving a hole an agent fills with a guess.
- **reddit:listing is broken** (see above). Either fix the recipe or record the trap.
- **`verify` cannot pass opts**, so a read recipe that needs an argument (`youtube:channel-videos`
  wants a channel) can only ever report 0 items. Needs a per-site verify fixture — one known-good
  opts blob per domain — before its `never` means "broken" rather than "unaskable".

### P3 — remaining

- `up` from a truly headless context is *pointed at*, not *proven*. Nobody has run the headless path
  end to end under cron; do that before promising it.

### P4 — the playbook system

- **No promotion path from learned to canon.** apl ADR-0008 says promotion is deliberate and
  reviewed and ships no tooling, so `~/.config/chrome-agent/<domain>/` accumulates true-but-
  unpromoted knowledge until someone reads it. A `promote` that diffs learned against canon makes
  that a review instead of an archaeology dig.
- **Traps are hand-written and survive regeneration only via the generator's tables.** Deliberate —
  only the verb surface has a source of truth — but a trap written straight into a generated file is
  lost on the next run. Worth a guard.

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
