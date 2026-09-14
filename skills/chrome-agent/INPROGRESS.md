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

## Third pass 2026-09-12 — off this machine: ADRs, site assets, lifecycle

Four ADRs were written **before** the code, because these are decisions:
`docs/adr/0003-chrome-agent-on-a-server.md` (headless + a time-boxed login share),
`0004-site-definitions-as-installed-assets.md`, `0005-profile-and-session-lifecycle.md`,
`0006-chrome-agent-cli-separate-from-the-fork.md` (the CLI/engine split — the one still open for
argument).

### Sites are data — 19 definitions, one file each

`sites/<domain>.json` holds login url, the signed-in probe, the logout route, the read verb and the
traps. `scripts/sites.py` owns the resolution order: `$CHROME_AGENT_SITES` → `~/.config/chrome-agent/
sites/` (**installed, editable, wins**) → shipped. A re-skinned site is now a one-file fix on the
server with no redeploy — proven by pointing an override dir at a different probe and watching the
verdict change.

Six agent teams ran in parallel to produce them: one converting the seven proven domains out of the
CLI, three researching new ones, two spiking the server and the share.

**All 19 probes were then run live in the fork. Every one returned a real verdict; none threw.**
`medium.com` and `indiehackers.com` proved their signed-IN path and are `verified`; the rest proved
only the signed-OUT path and say so in `probe_checked`. `auth` now labels an unverified definition
in its own output, so a hypothesis cannot read like proof.

Two probe bugs came out of review, both fixed in the file and the fallback:

- **LinkedIn failed OPEN.** Its `catch` returned `signed_in: true`, so a network blip, a CSP refusal
  or a stale JSESSIONID on a logged-out page all reported green — on the one site where a false
  green already cost 44 hours of silent non-posting.
- **X gated on `ct0`**, which is set during the login *flow*, before authentication completes. It
  now waits for the profile link and reports `inconclusive` instead of guessing.

### `logout`, and the lock check that had never fired

`logout <domain>` takes `url | dom | cookies` from the site file and **verifies with `auth`
afterwards**. `cookies` warns in its own output that the site was never told, so the server-side
session outlives it — "we cleared local cookies" and "the site ended your session" are different
facts and only one is a revocation. All three branches tested, the DOM and cookie ones against a
synthetic fixture so no real session was destroyed to prove it.

`profile list` / `profile delete` landed with real guards — and found a dead one: **both lock checks
used `-e` on a SYMLINK whose target never exists**, so "is a browser running on this profile?" had
never once been true. `-L` fixed it; `profile delete` now correctly refuses a live profile.

### ADR 0003 §4 was wrong before anything was built on it

The spike read this fork's own source and killed the profile-carry escape hatch: macOS and Linux
**both tag cookie ciphertext `v10`** (`keychain_key_provider.mm:28-66` vs
`posix_key_provider.cc:17-23`), so Linux decrypts the Mac's cookies with the constant `peanuts`,
padding fails, and the rows are skipped as `kDecryptFailed` — **with no error surfaced anywhere.**
History and Local Storage survive; every session cookie does not. Carrying is Linux→Linux only, the
tarball is then a plaintext credential, and `auth` per site on arrival is mandatory. The ADR carries
the correction with citations rather than a quiet edit.

### Also

`up --headless`; `login` refuses under headless and points at `share`; `doctor` answers every
question whose wrong answer is a hang (fork present, built, spool, tools, sites dir); the playbook
generator now emits a playbook for a site that has a definition but no recipe, so all 19 have one.

### `read <domain>` — all 19 sites answer, and the weak ones say so

12 of the 19 definitions have no read recipe, and "we know this site but cannot read it" is a
useless kind of knowing. `read` runs the site's own recipe when there is one and points
`generic:page-text` at the page the definition names when there is not — flagging `"generic": true`
plus how to promote it, because a page-text scrape must never be mistaken for an API replay.

Swept across every site: linkedin 20 posts, reddit 50, HN 30, instagram 12, and real text from
bsky/mastodon/stackoverflow/dev.to/producthunt/medium/indiehackers/github. `quora` (347 chars) and
`discord` (268) return almost nothing — both are login walls, which is the correct answer for a
signed-out profile and exactly what `auth` says about them. `facebook` reads empty for the same
reason.

### `doctor` asks the engine what it can do — and immediately caught something

ADR 0006 says a CLI newer than its engine must not discover that as a hang. The spool protocol has
no VERSION verb (that is a fork change, not a CLI one), so `doctor` asks the engine what it can
actually DO, each question under its own timeout: eval, evalasync, tabId-in-listtabs, screenshot
ack. Fatal (evalasync, tabId) flips `ready` to false; degraded does not — calling a lost screenshot
"not ready" would train an operator to ignore the word.

**It flagged SCREENSHOT on the first run — and that finding was TRANSIENT, not a break.** The
verb timed out (`timed out waiting for result …`) while the browser was carrying 38 tabs, 32 of them
leaked by this session's own tests. After those were closed it acks normally: both the bash and Go
probes report `screenshot_ack: true`, and `chrome-agent shot` wrote a real 7,594-byte PNG
(2026-09-13). So the correct statement is not "the fork is broken" but "SCREENSHOT degrades under
tab pressure, and `doctor` sees it" — which is still worth knowing, and still worth a tab reaper.

### The ledger rotates

14.7 MB with no rotation — every read result stored verbatim. It rolls on size (8 MB default),
gzips the roll (14.7 MB → 3.5 MB), keeps 5, and never deletes the live file. `ledger status|rotate`.
An unbounded audit log is one nobody opens and eventually one that fills a server disk.

### `install`

`chrome-agent install` creates `~/.config/chrome-agent/{sites,learned,share}`, syncs the site
definitions into the editable dir, and symlinks the CLI onto PATH — a symlink, not a copy, because
a copy is a second version of the CLI that ages silently.

### The share — the tunnel half is proven, the X half is not

`chrome-agent share start|status|stop|reconcile` (`scripts/share.sh`, `docs/share.md`). Verified
independently tonight, not just reported: a 60s selftest served a file over
`https://…trycloudflare.com` (HTTP 200 fetched from off-tunnel), the TTL fired **unattended after
the starting shell had exited**, the URL then returned 530, no cloudflared and no timer process
remained, and both `share-start` and `share-stop reason=ttl` are in the ledger with the profile.

**Two bugs the TTL test caught, each of which silently disabled the security property:** a
`nohup … &` timer died with its parent shell — the TTL stopped being enforced in exactly the case it
exists for — and cancelling that timer with `pkill -f <marker>` killed the wrong process, including
the teardown's own shell, leaving the tunnel up with nothing logged. Neither is visible in a
happy-path test. Both were found by letting the TTL actually fire.

**`share start` refuses on macOS**, deliberately: a share exists because a server has no screen, and
a Mac has one. A degraded mac mode is precisely the one that would get used casually and then go
stale on a tunnel. Its exit codes were aligned to this CLI's contract (wrong platform is 1/usage,
not 2 — 2 means "not signed in" and this is reachable as `chrome-agent share`).

**Unproven, and it is the important half:** Xvfb → x11vnc → noVNC, the fork running headful on a
virtual display, and a human actually logging in through it. Nothing in this project has run on
Linux yet. Also noted: noVNC's `?password=` is the VNC password, so the effective secret is 8 hex
characters — fine for a 15-minute window behind an unguessable hostname, not fine for anything
longer, and the fix if this graduates is Cloudflare Access, not a longer password.

### `selftest.sh` — 28 checks, none destructive

`bash scripts/selftest.sh` exercises the contract (exit codes, JSON shapes, the fork guard), both
historic spool-keying bugs, the profile-lifecycle guards, the site resolution order, and — when a
browser is up — auth/logout/read. It never posts, never likes, never logs out of a real site and
never deletes a real profile: the logout and read paths run against a synthetic `example.com`
definition in a temp dir, because proving a code path should not cost the owner a session.
28 pass, 0 fail.

### Ubuntu, in a container — and it found three real bugs

The installer and the CLI were run against **real Ubuntu 24.04** in Docker (no server touched, no
prod involved). `server-install.sh` installed node 18, python3.12, Xvfb, x11vnc, websockify, noVNC
and cloudflared 2026.9.1, and its preflight correctly reported the only thing missing: a Linux fork
binary. Then the CLI itself was run there, and three things broke that would have broken on the
first real deploy:

1. **`SKILL` was hardcoded to `$HOME/.claude/skills/chrome-agent`** — a path that exists on exactly
   one machine. Every helper hangs off it, so `sites list`, `recipes --json` and `promote` all died
   with "No such file or directory": **6 of 23 selftest checks failed, all from one line.** Same
   class as the hardcoded `FORK`, same fix — the script now resolves its own location through
   symlinks (by hand: `readlink -f` is GNU-only and this has to work on macOS too).
2. **The recipe registry path was hardcoded to the owner's browser-research checkout**, in two
   files, under two different env var names. A server without that checkout silently reported
   **6 verbs instead of 48** and every `recipe <key>` failed. There is now one resolver
   (`recipes-path.mjs`) and `chrome-agent recipes vendor`, which carries the registry into
   `~/.config/chrome-agent/recipes` — the same installed-copy-wins model as the site definitions,
   and `install` does it automatically.
3. **The vendored registry is ESM in `.js` files.** In its home repo an ancestor `package.json`
   says `{"type":"module"}`; vendored alone it does not, so node read every file as CommonJS
   ("Cannot use import statement outside a module") — which surfaces as "6 verbs" again, with a
   completely different cause. The marker now travels with the copy.

After all three: **23/23 selftest on Ubuntu, 48 verbs from the vendored copy with no
browser-research checkout on the box, and a real recipe payload builds.** What is still untested on
Linux is exactly what needs a Chromium build: the browser itself.

---

## Fourth pass 2026-09-14 — the client installs its own browser, and every action is paced

### The engine ships, and the client no longer needs the fork
Releases are public for **linux-x64** and **macOS arm64** (signed + notarized; Intel macs will not be
built). `chrome-agent engine install` fetches `manifest.json` + `SHA256SUMS`, refuses any mismatch,
unpacks with symlinks intact (the macOS framework fails its code signature without them) and swaps
the tree into `~/.local/share/chrome-agent/engine` atomically. Browser resolution is now
`$CHROMIUM_SENDKEYS_OUT` > installed engine > `$CHROME_AGENT_FORK/out/Default`; `RequireFork` is gone,
and a missing browser names `engine install` as the fix. Proven on this Mac: install into a throwaway
dir → codesign strict passes on the Go-extracted .app → `up` → H.264/AAC `probably`, webdriver false.

### `up` is native Go — no node on the runtime path
The launcher execs the browser directly with `chromium-agent-launch.cjs`'s flags, in its own session
(setsid). **P3 background launch:** today's 12:28 node launch from a background shell died with
`No rendezvous client`; the Go launcher started the dev build on the owner profile from the same kind
of shell. **P3 stale lock:** a Singleton* set whose pid is dead is cleared before launch, and a browser
that exits immediately (another instance owns the profile) is reported instead of waited on.

### Pacing — read / react / mutate, per (profile, site)
`internal/pacing`: read 3–8s, react 20–60s + 30/day, mutate 2–5 min + 10/day. Jitter is drawn once
and stored, a file lock makes parallel lanes on one profile queue, short waits are served inline and
anything past 120s (or a spent cap) exits **5 rate-limited** with `retry_after` before a byte reaches
the site. A STAGED write is paced as a read. Overrides: `"pacing"` in a site file, or
`~/.config/chrome-agent/pacing.json` per machine. `pacing <domain>` shows the budget; ledger lines now
carry `domain` + `class`. Raw `eval`/`evalcsp` are deliberately unpaced primitives.

### P2 — reactions exist in Go
`linkedin like`, `x like`, `x repost`, `reddit upvote` — staged without `--confirm`, never toggle an
existing reaction off, `ok` only when the state flipped. Staged runs on live pages 2026-09-14 read the
before-state on all four; nothing has been clicked by the Go verbs yet (owner-gated).

### Site knowledge reaches machines that synced before
`sites sync` kept every changed file forever because it could not tell an old shipped copy from an
edit. It now records what it wrote (`.shipped.json`) and upgrades untouched copies. Found when the
fixed YouTube probe (`as: "Avatar image"`) kept running the stale installed file. All 19 `auth` probes
ran live on the owner profile: clean verdicts everywhere; 10 unverified sites proven signed-out.

---

## Still open

- **Neither release has run on a second machine** (`verified_on_second_machine: false`). Linux fork.2
  passed its gates on the build host; macOS on this Mac.
- **`--confirm` on the Go react verbs is unexercised** — staged is proven, the click is owner-gated.
- **reddit.com and instagram.com report signed out** on the owner profile although their files say
  `verified` — a human sign-in, or the probe has drifted.
- **The share's X half is the next thing to prove**: a human logging in through noVNC on a virtual
  display, then `chrome-agent auth <domain>` green on the server. The tunnel half is done.
- **17 of 19 site definitions have an unproven signed-IN path.** They need a human session, once,
  per site. `probe_checked` records exactly which half is proven.
- **`discord.com` probably does not belong.** Its API authenticates on a header, not cookies, so the
  one thing this fork is good at — replaying a site's own API with the human's session — is
  unavailable; what is left is hashed-class DOM scraping. Keep it probe-only or drop it.
- **`write` is empty for all 12 new sites.** No publish path was justified without driving the
  browser, which was correctly off-limits to the research agents.
- **No read recipes for the new sites** — `read <domain>` covers them with the generic reader, which
  is a floor, not a ceiling: it cannot paginate, cannot read a thread, and returns a login wall as
  ~300 characters of nothing. Each file's `notes` names the endpoint a real recipe should replay.
- **facebook.com stays red**, honestly: the profile is signed out and `auth` agrees.
- **Cron/launchd still unproven.**
- **SCREENSHOT degrades under tab pressure** — it timed out at 38 tabs and recovered at 6. Not a
  fork bug; a resource one. **Addressed in the Go client (2026-09-13):** `tabs reap` closes idle
  SESSION tabs (staged until `--yes`), never a tab with no registration, and never the caller's
  own; the tabid file is now touched on reuse so "idle" means unused, not merely old. The selection
  rule is unit-tested; the close path (CLOSETAB by tabId) was proven by hand earlier, and a live run
  of the Go verb is still owed.
- **No protocol version.** `doctor`'s capability probe is the workaround; a real `VERSION` verb in
  the fork is the fix, and it is a fork change.

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
