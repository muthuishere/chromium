# chrome-agent — what to build next

Written 2026-09-12, after apl (`deemwar-products/apl`) stopped vendoring this script and started
merely referring to it (apl ADR-0009). Everything below is a gap **observed while wiring that up**,
not a wishlist — each item names how it was found.

The split now: chrome-agent owns site knowledge and the browser; apl owns which identity acts and
joins the two. So anything here that is "how a site behaves" belongs in this repo, and anything that
is "who is acting" does not.

---

## P0 — the one thing that blocks everything else

### 1. `FORK` is hardcoded, so this runs on exactly one machine

```sh
FORK="$HOME/muthu/gitworkspace/chromium"     # chrome-agent:16
CJS="$FORK/chromesendkeys.cjs"
LAUNCH="$FORK/chromium-agent-launch.cjs"
```

The CLI is portable; the browser is not. A different session or directory on this Mac is fine, a
different **machine** is not — it needs a 1.4 GB fork checked out at that exact path, plus a built
`out/Default`.

This is the single reason `browser:` identities cannot ship to anyone else. apl can sell
`github:` and `whatsapp:` today and cannot sell `browser:`, and that is entirely this line.

**Build:** `CHROME_AGENT_FORK` with the current value as default, and a real error when the fork is
absent — naming what is missing, not a bare "file not found" from node. Then decide whether a
stock Chrome/Brave profile is a supported degraded mode (loses undetectability, keeps the verbs).

---

## P1 — what apl needs from here specifically

### 2. `recipes --json`

`chrome-agent recipes` already prints all 42 with descriptions — good. But `--json` is silently
ignored, so apl's playbook generator **regex-parses `registry.js`** to learn the verb list:

```py
KEY = re.compile(r'"([a-z0-9]+):([a-z0-9_-]+)"\s*:')   # gen_playbooks.py
```

That reaches across a repo boundary into someone else's source layout and breaks on any formatting
change. The tool already knows the answer.

**Build:** `recipes --json` emitting `{key, site, verb, describe, write: bool, world}`. Then the
generator calls the CLI instead of reading JS, and `write: true` replaces "the key came from
post.js" as the way writes are identified.

### 3. A session/auth verb — apl currently cannot tell if a profile is signed in

There is no `auth`, `status --auth`, `whoami` or `signedin` verb. So apl's browser probe can only
check that a profile *directory* exists, which means a `browser:` handle can never report better
than `unknown` — even when the session is dead. Every playbook says "an expired session shows a
login wall"; nothing can assert that programmatically.

**Build:** `chrome-agent auth <domain>` → `{signed_in: bool, as?: string}`, read-only, no navigation
side effects if possible. apl's `Probe` consumes it directly and `apl accounts --check` starts
reporting a real state. This is the highest-value item after P0.

### 4. Documented, stable exit codes

apl maps child exit codes to user/auth/network errors. This script has no documented exit contract,
so apl cannot distinguish "the site said no" from "the browser was not running".

**Build:** a small table — 0 ok, 1 usage, 2 not signed in, 3 browser unreachable, 4 site refused —
and keep it stable.

---

## P2 — capability gaps in the recipe set

Found by generating the playbooks from the registry: several sites claim less than assumed.

- **No reaction recipe exists for any site.** No `linkedin:like`, `x:like`, `x:repost`. I assumed
  they existed and wrote them into a playbook; they do not. Reacting is the most common low-risk
  social action and it is entirely missing.
- **facebook.com, instagram.com, youtube.com are read-only** — one or two read recipes each, no
  writes. Their `write.md` now says so rather than leaving a gap an agent fills with a guess.
- **news.ycombinator.com has writes but no read recipe** — it can post and comment but cannot read
  a thread back, which collides with its own verification rule ("HN serves 200 on a dead post").

---

## P3 — operational sharp edges, all hit for real on 2026-08-30/31

- **`up` fails when launched from a background/non-GUI context.** Chromium dies with
  `bootstrap_look_up … No rendezvous client, terminating process`. Relevant the moment this runs
  under cron, a daemon, or a headless session.
- **A killed browser leaves a stale `SingletonLock`**, and the next `up` silently no-ops — the log
  says `Opening in existing browser session.` and nothing services the spool. Needs detection and
  a clear message, not silence. (Clearing `Singleton{Lock,Cookie,Socket}` is the fix.)
- **The ledger and learn dir are global**: `~/.config/chrome-agent/actions.ndjson`, `learn/`, and
  `/tmp/chrome-agent-cj.err` are shared across profiles with no identity column. Once two profiles
  are in use — they are — one audit trail cannot say which identity did what.
- **Spool keying is solved, keep it that way.** Spool and launch log derive from a hash of the full
  profile path; the default profile keeps its historic paths. Two earlier bugs lived here (a
  trailing slash forked one profile into two spools; `basename` collided `~/work/profile` with
  `~/personal/profile`). Any change here needs both cases re-tested.

---

## P4 — the playbook system itself

- **`last_verified` is generated, not proven.** It is stamped with the generation date, which says
  nothing about whether the verbs still work. Build `chrome-agent verify <domain>` — run that site's
  read recipe, stamp the date on success, and fail loudly rather than silently ageing.
- **No promotion path from learned to canon.** apl ADR-0008 says promotion is deliberate and
  reviewed, and provides no tooling, so learned notes in `~/.config/chrome-agent/<domain>/` will
  accumulate true-but-unpromoted knowledge until someone reads them. A `promote` that diffs learned
  against canon would make that a review rather than an archaeology dig.
- **Traps are hand-written and survive regeneration only if added to the generator's tables.** That
  is deliberate — only the verb surface has a source of truth — but it means a trap written directly
  into a generated file is lost on the next run. Worth a guard.

---

## What belongs here, and what does not

**Here:** verbs, traps, login procedure, the profile/spool mechanics, the fork dependency, anything
provable by running the browser.

**Not here:** which `browser:<label>` may act on a site. That is a statement about an identity, not
about the site — apl owns it (`apl identity set browser:<label> --site <domain>`), and playbooks
deliberately name no identity. A site is reachable as more than one person; the moment a default
identity lands in a playbook, the multi-identity design silently becomes single-identity.

---

## The apl-facing contract, if you build profile + login

apl already has the slot for this. ADR-0007 makes `login` a capability — "prepare the child process
that ESTABLISHES the credential" — and its table has one blank row:

| `browser:<label>` | *nothing* — chrome-agent has no sign-in command; apl says so |

Fill that row and apl needs **no new concept**: its browser adapter implements the existing
`PrepareLogin` interface and `exec`s whatever this CLI exposes, exactly as `apl login whatsapp:biz`
execs `wacli auth` and lets the QR render in the user's terminal.

Three verbs would close it:

```
chrome-agent profile create <dir>        # make a profile, do not launch
chrome-agent login <domain>              # open THIS profile at that site, headful, for a human
chrome-agent auth  <domain>              # read-only: {signed_in: bool, as?: string}
```

**`login` must not type anything.** It opens the window and gets out of the way. apl's job is to
exec it and then verify with `auth` — never to supply a credential. That is ADR-0008's manual-login
rule, and it is the reason a password never reaches an agent's context.

### One thing ADR-0007 under-specifies, and this is where it shows

`apl login whatsapp:biz` is unambiguous: one account, one pairing. **A browser profile is not signed
into a thing — it is signed into many sites.** So `apl login browser:deemwar` has no single meaning,
and the answer has to be per site:

```
apl login browser:deemwar --site linkedin.com
```

which implies `login` and `auth` here are **domain-scoped, not profile-scoped**. Build them that way
from the start; a profile-scoped `signed_in: true` would be a lie the moment one site's cookie
expires while another's holds — and a green light nobody verified is the failure this whole design
exists to prevent.

`profile create` is the one that is genuinely profile-scoped. apl should never `mkdir` a profile
itself: today it reports `dangling` when the directory is missing, and with `profile create` it can
exec this instead of quietly creating a logged-out profile that looks configured.
