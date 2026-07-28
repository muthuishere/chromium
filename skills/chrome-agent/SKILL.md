---
name: chrome-agent
description: >
  READ and WRITE on LinkedIn, X/Twitter, Reddit, HN, YouTube and more through the owner's
  UNDETECTABLE Chromium fork (navigator.webdriver=false, real logged-in deemwar profile) —
  by running browser-research's VERIFIED recipes unchanged through the fork, plus READ any
  video post by transcribing it. Use to: post / comment / like / reply on LinkedIn / X /
  Reddit / HN, read a feed or timeline, run any browser-research recipe, and turn a
  YouTube/Instagram/X video into a readable transcript. Trigger on: post to linkedin,
  comment on linkedin, reply on x, tweet, post to reddit/hackernews, read my linkedin feed,
  run a browser recipe, learn a site's api, transcribe this youtube video, read this video.
  Method: reuse browser-research recipes (internal-API replay) via the undetectable fork.
  Engines: chromium fork (chromesendkeys.cjs) + browser-research recipes + social-media-ops
  + video-transcribe. Profile: ~/chrome-agent-profile.
---

# chrome-agent

The deemwar social organ. One browser instance on the logged-in `~/chrome-agent-profile`
(Suguna Paulraj / CEO), driven through the **undetectable** Chromium fork — real profile, no
webdriver banner. It does what the detectable browser-bridge can't.

## How it works (don't reinvent — reuse)

The **browser-research** skill already has verified, general recipes for every major site
(reads via internal-API replay + `post.js` staged-safe writes). chrome-agent **runs those
recipes unchanged through the fork**, so there's one recipe catalog for the whole fleet.

**The client is now thin (~405 lines).** Async eval, per-tab targeting, and the tab registry
all landed **natively in the fork**, so the client no longer carries them: the fork's
`EVALASYNC` runs an async function body and awaits it in-browser (the old base64
`eval(atob(...))` + window-token stash+poll hack is gone), and a UUID per-tab registry with the
`TAB:<tabId>|` prefix replaced ~250 lines of client-side tab-ownership bookkeeping and a
cross-process mutex. What still lives in the client, by design: the DOM recipes themselves,
profile handling (incl. the default profile), local-file staging, `_ensure_origin`, and `_pace`.
Full detail + the eval contract: `references/method.md`. Net effect:
**posting is internal-API replay — no composer, no clicks, no screenshots, no OS focus.**

## Rate limits — 1 SECOND MINIMUM on scrolling and liking (2026-07-26)

Owner: *"if you scroll fast on x or reddit they will ban atyleast a 1 second delay scrolling and
liking"* and *"also scrolling also 1 seconds within"*. These are the owner's real professional
accounts; a ban is unrecoverable, so both are **floors, not defaults**.

| Action | Enforced where | Minimum |
|---|---|---|
| like / upvote / repost | `_pace` in `chrome-agent` | **2s** between actions |
| feed scrolling | `Math.max(1000, o.delay …)` in the x/youtube/linkedin/instagram/facebook recipes | **1s** per scroll |

**Why liking needed a file, not a `sleep`:** each like already slept 2s *inside* its own call, which
looks sufficient and is not — the gap that matters is *between* calls, and **every call is a separate
process**, so an in-process sleep cannot pace it. A loop of 15 likes had no floor at all. The pace
timestamp therefore lives in `~/.config/chrome-agent/last-engagement`, shared by every lane. It is a
floor: if the action already took longer than the minimum, nothing is added.

**Why scrolling needed a clamp, not a bigger default:** the recipes already slept 1200–1400ms, so
the default was safe — but `o.delay` let a caller pass `{"delay":100}` and scroll ten times a
second. **A safe default is a suggestion the next caller can ignore.** `Math.max(1000, …)` still
allows *slower*, never faster.

Verified: `{}`→1200ms, `{delay:100}`→**1000ms**, `{delay:3000}`→3000ms; and 5 likes across separate
processes took 8s (4 gaps × 2s), with a future timestamp capped rather than stalling.

## Per-session tab — native UUID registry (2026-07-28) — the fork maintains this, you don't

Owner: *"assign a seperate tab … use always your own tabid … let the skill internally maintain for
agents seperately"*.

**Every session gets ONE dedicated tab, keyed by a stable UUID minted by the fork.** On the
session's first page-op the client calls `NEWTAB`, which now **acks its tab's UUID**, and the
client stores that UUID at `~/.config/chrome-agent/tabids/<session>` (session key =
tmux session name, else `$CHROME_AGENT_ID`/env, else `unowned`). Every subsequent
`goto`/`evalAsync`/`shot`/`recipe` rides `TAB:<tabId>|`, so it lands in **that** tab regardless of
window focus, the human's tab, or stray tabs. You do not call `newtab`/`selecttab`/`closetab` by
hand any more.

**Why this is now race-free.** The UUID is a real per-tab handle straight from the fork — no more
`window.name` handles, no `listtabs`-index guessing (indexes shift when any tab closes), and no
client-side `tabs.json` registry or cross-process mutex. If a `TAB:<tabId>|` command names a tab
that has gone away, the fork returns `{"ok":false,"error":"unknown tabId"}` and **never** falls
back to the last-active tab — the old about:blank cross-tab race (a command silently landing in the
human's tab) cannot happen. `listtabs` now returns each tab's `tabId` alongside `window`, `index`,
`title`, `url`, and `active` across all windows, if you ever need to inspect.

This replaced ~250 lines of client-side tab-ownership bookkeeping, the `window.name`/activity-age
ownership probe, and the `mytab`/`whosetab`/`tabs-gc` machinery — all now unnecessary because each
session addresses its own tab by UUID directly.

## Recipes self-navigate (2026-07-21) — you no longer `goto` first

`chrome-agent recipe <site:name>` now puts the tab on the page that recipe needs, automatically.

**Why it exists:** a site recipe reads that site's cookies from the **active tab**, and cookies are
origin-scoped. Running `linkedin:feed` while the tab sat on another site returned
`{"error":"no JSESSIONID - not logged in?"}` for a **perfectly valid session** — a false
logged-out that silently cost 44 hours of LinkedIn posting while X kept publishing. The tab is
shared mutable state (it is wherever the last lane left it), so the bug is intermittent and
ordering-dependent. It is fixed once, here, rather than in every caller's head.

**Origin alone is not enough — the PAGE matters.** With the tab on
`linkedin.com/in/me/recent-activity` (host matched!) `linkedin:feed` still timed out; from `/feed/`
it returned 20 posts. So the guard matches the required PATH: `/feed` for the voyager recipes,
`recent-activity` for `linkedin:my-posts`.

**It deliberately does NOT navigate for:**
- `generic:*`, `capture:*`, `compose:*` — these operate on whatever page you are on
- `linkedin:comment`, `linkedin:comment-delete`, `reddit:comment`, `hackernews:comment` — these act
  on a permalink the CALLER opened; navigating would break them

Verified both directions: parked on `example.com`, `linkedin:feed` self-navigated and returned 20
posts; `generic:page-text` stayed on `example.com` and read it.

**Still true: "not logged in" is a claim about the TAB as much as the account.** If you see an auth
error, check `chrome-agent status` before concluding a session died.

## Verbs

```
chrome-agent up [url] | status | goto <url> | shot [path]
chrome-agent recipes                       # list every runnable recipe (live catalog)
chrome-agent recipe <site:name> [opts-json] [csp] # run ANY browser-research recipe via the fork (3rd arg csp = CSP-safe eval)
chrome-agent do <site:name> [opts] [verify-js]  # VERIFY-OR-LEARN: run + verify; on fail auto-arm capture
chrome-agent evalAsync '<async-js>'        # general async escape hatch (no recipe yet); alias of evalwithoutcsp
chrome-agent evalwithoutcsp '<async-js>'   # DEFAULT eval path — eval(atob()) inside the injected script
chrome-agent evalwithcsp '<js-that-returns>'  # CSP-SAFE eval — source injected directly, survives strict script-src (LinkedIn etc.)
chrome-agent linkedin like [post-url] [--confirm]  # self-verifying (aria-state flip); staged w/o --confirm

# fast-learning:
chrome-agent capture arm|dump|clear        # learn a site's REAL api: arm, act by hand, dump
chrome-agent learn <name> [url]            # visual: screenshot + DOM to Read/grep

# high-level (thin wrappers over verified recipes; STAGED unless --confirm):
chrome-agent linkedin post "<text>" [--confirm]
chrome-agent linkedin post-image "<text>" <img> [--confirm]
chrome-agent linkedin feed
chrome-agent x post "<text>" [--confirm] | x like <url> | x repost <url> | x reply <opts-json>
chrome-agent reddit upvote <url> [--confirm] | reddit comment <opts> | reddit post <opts>
chrome-agent watch <video-url>             # YouTube/IG/X video -> mp3 -> transcript.md
```

Engagement (like/comment/reply/repost/upvote) is built across LinkedIn / X / Reddit as
self-verifying actions — each confirms the effect (aria-state / testid flip) and drops into the
verify-or-learn loop if the button isn't found (logged out or the site re-skinned). **X requires
a live X session** in the profile; when logged out, `x like/repost` correctly returns
`status:LEARNING` (that's the loop catching it, not a bug).

Any write recipe works via the general runner too:
`chrome-agent recipe linkedin:comment '{"postUrl":"…","text":"…","confirm":true}'`,
`chrome-agent recipe x:reply '{…}'`, `chrome-agent recipe reddit:post '{…}'`, etc.

## CSP-safe eval — for strict-CSP sites (2026-07-28)

Strict-CSP pages (LinkedIn, most modern SPAs) send a `script-src` without `unsafe-eval`, which
blocks the default eval path's runtime `eval(atob(...))` → `EvalError`. So DOM recipes
(`linkedin:comment`, replies, any `evalAsync`-based recipe) fail there. Opt-in fix — the caller
chooses per call; it is NOT applied everywhere:

| Verb | Path | When to use |
|---|---|---|
| `evalwithoutcsp '<body>'` (alias `evalAsync`) | default `eval(atob(...))` in the injected script | anywhere but strict-CSP sites — all existing callers unchanged |
| `evalwithcsp '<body-that-returns>'` | source interpolated directly into the injected script (no runtime `eval`/`Function`) — CSP can't block it | **strict-CSP sites like LinkedIn** |
| `recipe <site:name> <opts-json> csp` | opts a DOM recipe into `evalwithcsp` for that one call | run a recipe on a strict-CSP site |

**When to use:** strict-CSP sites like LinkedIn → `evalwithcsp` (or `recipe … csp`); everywhere else
the default is fine. Verified: on a strict-CSP page `evalwithoutcsp 'return 6*7'` → `EvalError`,
`evalwithcsp 'return 6*7'` → `42`.

## Verify-or-learn loop (self-maintaining)

`chrome-agent do <key> [opts] [verify-js]` runs a recipe, then VERIFIES the effect (the recipe
returned no error; and, if given, `verify-js` returns truthy). If verification FAILS — a recipe
drifted, a site re-skinned — it **auto-arms `capture`**, records the drift to
`~/.config/chrome-agent/drift.ndjson`, and returns `status:LEARNING` telling you to perform the
action by hand and `capture dump` the real call to patch `recipes/<site>.js`. So the organ tells
you the moment it breaks and hands you the new API to fix it. `linkedin like` is self-verifying
by construction (it confirms the reaction button's aria-state flipped to "Like").

## Safety

WRITES are **STAGED by default** (browser-research's `post.js` contract): the recipe confirms
session+csrf and returns `staged:true` without publishing — nothing goes out until you pass
`confirm:true` (or `--confirm` on the high-level verbs). Verified: `linkedin post` with no
confirm → `staged:true`, nothing published.

## Rules

- One browser instance on `~/chrome-agent-profile`. Do not modify the chromium fork.
- Deemwar-branded, honest, **no meta/method-reveal**, no Claude/Anthropic mention. Every
  marketing post needs a visual. Verify each write landed (a recipe returns `submitted:true`
  + the resulting urn/url; still sanity-check).
- A recipe erroring = the site changed → fix it in browser-research's `recipes/<site>.js`
  (that's the shared home) and re-verify. New site/action learned twice → promote to a recipe.
- Actions log to `~/.config/chrome-agent/actions.ndjson`.
