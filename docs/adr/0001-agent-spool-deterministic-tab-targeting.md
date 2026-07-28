# ADR 0001 — Deterministic per-session tab targeting for the agent sendkeys spool

- **Status:** Accepted — IMPLEMENTED & VERIFIED 2026-07-28
- **Date:** 2026-07-28
- **Owner:** Muthu (fork maintainer) — "write a spec, I will make it fixed"
- **Author:** deemwar CEO agent (reporter). Symptoms verified live 2026-07-27/28.
- **Severity:** HIGH — this is the sole blocker on deemwar's social/outreach posting rail
  (multi-step recipes: comment, reply, post). 21 overnight automation firings could not post a
  single verified outreach touch because of it.

---

## Implementation note (2026-07-28)

Shipped a **UUID-per-tab registry** instead of a monotonic counter (never shifts when a sibling
closes, never collides across restarts). Each tab carries a `base::Uuid` on its `WebContents`
(`base::SupportsUserData`); `ResolveTabId` enumerates all windows, so a closed tab is simply absent.

- `TAB:<tabId>|<line>` pins any command to one tab, focus-independent. No prefix → the old
  last-active default (back-compat). A supplied-but-unknown `tabId` **hard-errors**
  (`{"ok":false,"error":"unknown tabId"}`) and never falls back — this is what kills the
  `about:blank` cross-tab race. `NEWTAB:<id>|<url>` acks the new tab's uuid; `LISTTABS` reports
  every tab's uuid; `CLOSETAB`/`SELECTTAB` accept a uuid too.
- **§3 screenshot** fixed: `SCREENSHOT:<id>|<path>` now acks `{ok,path,bytes}|{ok,error}` on every
  path, and captures **background** tabs (holds a capturer count to force an offscreen render), so
  it no longer hangs.
- The `chrome-agent` client shed ~250 lines of tab-ownership shell + its mutex; each session now
  gets one dedicated tab (`NEWTAB`→uuid, every op rides `TAB:<uuid>|`).

Verified by committed integration self-tests (`chrome-agent-selftest.cjs` 13/13, incl. the
adversarial-focus and two-tab cases below) plus `chrome-agent-media-selftest.cjs` 5/5.

---

## Context — the symptom

`chrome-agent` recipes that need more than one step against a page (navigate → find element →
type → click) fail non-deterministically. The canonical failure, reproduced twice back-to-back:

```
$ chrome-agent goto  "https://www.linkedin.com/posts/…-activity-7391046446197940224-…"
$ chrome-agent status
{'url': 'https://www.linkedin.com/posts/…-activity-7391046446197940224-…/', 'webdriver': False}   # tab IS on the post
$ chrome-agent recipe linkedin:comment '{"postUrl":"…","text":"…","confirm":true}'
{"site":"linkedin","recipe":"comment","error":"no comment editor found",
 "diag":{"openedBox":false},"url":"about:blank","candidateEditors":[],"commentButtons":[]}
```

`goto` + `status` show the correct post loaded, but the recipe's own `EVAL` reports
`url: about:blank` — **the navigate and the eval ran against different tabs.** Related symptoms
from the same root cause:

- `chrome-agent shot <path>` / `SCREENSHOT:` consumes the command but **writes no PNG file**
  (also the fork's headless `--screenshot` hangs). This forced our card renderer off the fork
  onto `playwright-cli` as a stopgap.
- A background research agent independently hit "tab kept reverting to `about:blank`,
  `evalAsync`/`shot` failing" in the same window.
- With two automation lanes sharing the one fork, posts have been corrupted onto the wrong tab.

The `chrome-agent` layer presents a "one dedicated tab per caller" abstraction, but it is a
fiction — the fork underneath only ever addresses *the last-active tab of any window*, so a single
stray tab or a second lane defeats it.

## Root cause — per-line "last-active tab" resolution, with no session pinning

The spool watcher resolves the target **independently for every spool line**, always to the
last-active tab:

- `chrome/browser/sendkeys_watcher.cc:636-640` — `DispatchLineOnUIThread()` does, per line:
  `browser = GetLastActiveBrowserWindowInterfaceWithAnyProfile();`
  `tab = browser->GetActiveTabInterface();` → `contents = tab->GetContents();`
  and then dispatches `GOTO:`/`SCREENSHOT:`/`EVAL:` against that `contents`
  (`sendkeys_watcher.cc:782-787`).
- Several injectors **re-resolve** last-active again internally
  (`InjectGoto` and siblings each call `GetLastActiveBrowserWindowInterfaceWithAnyProfile()` at
  `sendkeys_watcher.cc:883,899,915,936,956`).
- The protocol is documented as intentionally single-surface — `CHROMIUM_SENDKEYS_SPEC.md`
  §"Target resolution": *"`GetLastActiveBrowserWindowInterfaceWithAnyProfile()` →
  `GetActiveTabInterface()` … single-window/single-tab-at-a-time … no multi-window targeting."*

Consequence: a recipe issues `GOTO:<url>` (resolves to tab A, navigates it), then issues
`EVAL:<id>|<js>` a moment later (re-resolves to whatever is last-active *now*). If anything has
changed the active tab in between — a stray `about:blank` new-tab, a background navigation, a
second lane's command, or `GetActiveTabInterface()` returning a different tab than the one
`status` reads — the `EVAL` runs on tab B (`about:blank`), and the recipe reports "no comment
editor found." There is **no handle by which a caller can say "run all of these against the tab I
just navigated."**

The **SCREENSHOT** defect is the *same* root cause: `InjectScreenshot()`
(`sendkeys_watcher.cc:977-990`) captures the surface of that same last-active `contents`; when it
is a blank/backgrounded tab there is no renderable surface, the copy fails
(`"sendkeys: screenshot copy failed"`, line 990), and — critically — the failure path does not
`WriteResultFile()` (contrast the EVAL path, `sendkeys_watcher.cc:974`), so the `chromesendkeys.cjs`
client just polls `results/<id>.json` forever and reports "no file." So SCREENSHOT is not a
separate bug to chase first; fix targeting and give it a result ack and it comes back with it.

## Decision — thread an explicit target tab id through the protocol

Make a command sequence bind to **one** `WebContents` deterministically, independent of window
focus, stray tabs, and other lanes. Keep the current last-active behavior as the default so
nothing existing breaks.

### 1. A stable tab identity + a way to list/create tabs
- Mint a stable id per tab (a fork-local monotonic `tabId`, or reuse an existing
  `SessionID`/`WebContents` handle). Maintain a registry mapping `tabId → WebContents` that
  survives focus changes (update on tab close/create).
- New spool commands (fire-and-forget in, `results/<id>.json` out, same two-way pattern as EVAL):
  - `TABS:<id>` → returns `[{tabId, url, active}]` for all tabs across all windows.
  - `NEWTAB:<id>|<url?>` → opens a tab, returns its `tabId` (the caller's dedicated surface).
  - (optional) `CLOSETAB:<id>|<tabId>`.

### 2. Optional `tabId` prefix on every targeting command
Extend the line grammar so a command MAY name its target tab; when present, resolve via the
registry instead of last-active:
- `GOTO:<tabId>|<url>` · `EVAL:<id>|<tabId>|<js>` · `SCREENSHOT:<tabId>|<path>` ·
  `CLICK:<tabId>|…` · `TEXT:<tabId>|…` · `KEY:<tabId>|…`
- Back-compat: if no `<tabId>` is given, fall back to the existing
  `GetLastActiveBrowserWindowInterfaceWithAnyProfile()->GetActiveTabInterface()` path. Existing
  callers are unaffected.
- Implementation touch points: `DispatchLineOnUIThread()` (parse an optional leading tabId and
  resolve `contents` from the registry), and the injectors that re-resolve last-active
  (`InjectGoto` etc. at lines 883/899/915/936/956) must instead receive and honor the already
  resolved `contents`/tabId rather than re-querying last-active.

### 3. Make SCREENSHOT write a file AND an ack
- `InjectScreenshot()` targets the pinned tab (via #2), writes the PNG to the given path, and on
  both success and failure calls `WriteResultFile(id, {ok, path, bytes} | {error})` so the client
  can verify the artifact instead of polling forever. Fix or explicitly document the hanging
  headless `--screenshot` path.

### 4. (Secondary) stray-tab hygiene
A `TABSGC` command (or auto-close of `about:blank` tabs with no owner) reduces the surface that
steals last-active. Non-critical once #1–#2 land, but cheap insurance.

## How `chrome-agent` uses it (so the "per-agent tab" becomes real)
On first use a caller calls `NEWTAB` → gets a `tabId`, stores it for its session, and passes that
`tabId` on **every** subsequent `goto`/`eval`/`screenshot`/`click`. Two lanes each hold their own
`tabId` and can interleave without cross-contamination. The recipe's `goto` and its follow-up
`eval` are guaranteed to hit the same surface, so `url: about:blank` can no longer happen.

## Acceptance tests (how to know it's fixed)
1. **Targeting under adversarial focus:** open ≥2 windows and a stray `about:blank` tab, make the
   blank tab last-active, then `goto <tabId> <postUrl>` and `eval <tabId> 'location.href'` → returns
   `<postUrl>`, 10/10 runs (today: fails ~immediately with `about:blank`).
2. **Two concurrent lanes:** two spool clients, each pinned to its own `tabId`, run
   goto→eval→type→click interleaved → zero cross-tab contamination.
3. **Screenshot:** `screenshot <tabId> /tmp/x.png` → `file /tmp/x.png` reports a valid PNG of that
   tab, and `results/<id>.json` = `{"ok":true,…}` (today: no file, client hangs).
4. **End-to-end:** `chrome-agent recipe linkedin:comment` lands the comment on the real post with a
   stray `about:blank` tab present the whole time.

## Empirical: the external `selecttab` workaround is NOT sufficient (verified 2026-07-28)

Attempted the obvious workaround — `listtabs` → open a dedicated tab on the target post →
`selecttab` it → confirm it is `active` → run the recipe — to avoid waiting on this fix. **It
failed repeatedly.** Even with (a) my own retro loop killed so no lane was competing, (b) the
spool idle, and (c) `listtabs` confirming my post tab was `active` in the same breath before the
call, `linkedin:comment`'s eval still returned `url: https://www.reddit.com/`. Closing the stray
tabs to force the picker shifted indices under `closetab <index>` (indices renumber after each
close) and destroyed the target tab, and an unrelated `x.com/home` tab appeared.

Two concrete implications for the fix:
1. **The wrong tab is chosen INSIDE the recipe**, after and despite an external `selecttab`. So a
   fix that only sets "active" is not enough — the recipe/eval must be given, and must honor, an
   explicit `tabId` end to end (the caller pins the tab and every `GOTO`/`EVAL`/`SCREENSHOT` the
   recipe issues carries it). External activation cannot survive the recipe re-resolving internally.
2. **`closetab`/`selecttab` are index-based and unstable** — indices renumber on every close, so a
   caller cannot reliably address a tab across mutations. The stable `tabId` (#1 in the decision
   above) must be the address for close/select too, not the positional index.

Net: the workaround is not viable; the per-`tabId` threading in the Decision is the actual
unblock. Until it lands, multi-step recipes cannot be steered to the right tab from outside.

### Follow-up (2026-07-28, second session): it is ALSO a navigate-then-eval race, not only contention
Isolated it further. Paused the competing crypto browser crons (`crypto-social-pulse` every 25 min,
`crypto-research-loop`, `crypto-social-sync`) so NO other lane was driving the fork, and reduced the
browser to a SINGLE tab already loaded on the target post (verified `active`). `linkedin:comment`
**still** returned `url: about:blank`, "no comment editor found". So with contention and tab
ambiguity both removed, the recipe's own `GOTO`→`EVAL` sequence still evaluates on `about:blank` —
the eval fires before the navigation it just issued has committed a document (the injectors
re-resolve last-active and/or the eval races the load). Two consequences for the fix:
- **The `EVAL` must run against the *navigated* document**, not fire-and-forget after `GOTO`. Either
  make `GOTO:<tabId>|<url>` block/ack on load-commit (write a `results/` entry when the target tab
  reaches a ready state) so the client can sequence `EVAL` after it, or add a `WAITNAV:<tabId>` /
  have `EVAL` implicitly wait for the pinned tab's pending navigation to commit.
- This is why **proactive posting works but reactive commenting does not**: `linkedin:post` / `x:post`
  are internal-API replay (`post.js`, voyager `normShares` / DraftJS) that do not depend on a
  navigated third-party DOM being present, so they are immune to both the tab-targeting and the
  navigate-then-eval race. Only the DOM-driven recipes (`linkedin:comment`, `*:comment`, replies)
  hit this. 75 proactive posts have succeeded; reactive comments via this recipe have never landed.

## Consequences
- **Unblocks revenue:** the 5 staged outreach touches (and the whole social posting rail) can post.
- Per-agent-tab isolation becomes real; the fleet can safely run multiple browser lanes.
- `render-card` can return to the fork and retire the `playwright-cli` detour once SCREENSHOT works.
- **Cost:** a back-compatible protocol extension (optional `tabId`), a tab registry + resolver in
  `sendkeys_watcher.cc`, and the screenshot file-write/ack path. No change required of existing
  last-active callers.

## References (verified file:line, this fork)
- `chrome/browser/sendkeys_watcher.cc:636-640` — per-line last-active resolution
- `chrome/browser/sendkeys_watcher.cc:731,782-787` — `DispatchLineOnUIThread` prefix dispatch
- `chrome/browser/sendkeys_watcher.cc:869-956` — injectors re-resolving last-active
- `chrome/browser/sendkeys_watcher.cc:977-990` — `InjectScreenshot` (copy-fail, no result ack)
- `chrome/browser/sendkeys_watcher.cc:974,1014` — `WriteResultFile` (EVAL has it, SCREENSHOT lacks it)
- `CHROMIUM_SENDKEYS_SPEC.md` §"Target resolution", §"Two-way protocol: the results/ directory"
- `chromesendkeys.cjs` — `eval`/`getdom`/`screenshot` subcommands (poll `results/<id>.json`)
