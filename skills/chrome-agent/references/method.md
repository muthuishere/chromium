# chrome-agent — method & the fork's eval contract (PROVEN 2026-07-15, CEO hands-on)

## Architecture: reuse browser-research recipes, run them through the undetectable fork

Don't reinvent per-site driving. **browser-research** already has verified, general recipes
(reads via internal-API replay + `post.js` staged-safe writes). chrome-agent RUNS THOSE
RECIPES UNCHANGED through the fork. The client is **thin** (~405 lines): async eval and per-tab
targeting are native in the fork now, not client-side scaffolding. Two primitives make it work:

- `recipe <site:name> [opts-json]` → `recipe-run.mjs` resolves the key in browser-research's
  `RECIPES` registry and hands the recipe `fn` to the fork's native async eval (`EVALASYNC`),
  which awaits it in-browser and returns the JSON result — pinned to the session's own tab via
  `TAB:<tabId>|`. Verified: `linkedin:feed` returned real posts; `linkedin:post` (no confirm)
  returned `staged:true` (session+csrf OK, nothing published).
- `evalAsync <js>` → the general async escape hatch for anything without a recipe yet (the CLI
  verb is `evalAsync`; there is no bare `eval` verb).

Available write recipes (all staged unless `opts.confirm=true`): `linkedin:post`,
`linkedin:post-image`, `linkedin:comment`, `linkedin:delete-post`, `x:post`, `x:reply`,
`reddit:post`, `hackernews:post`, … Run `chrome-agent recipes` for the live catalog.

## THE fork eval contract — now native async (updated 2026-07-28)

**The fork now runs async natively via `EVALASYNC:<id>|<body>`.** The client hands the fork an
async function body (it may `await` and `return`), and the fork wraps it in an async IIFE, stashes
the settled result on a page global, polls that global in-browser, and acks
`{"ok":true,"value":...}` / `{"ok":false,"error":...}`. So the client no longer needs the old
base64 `eval(atob(...))` + window-token stash+poll dance — that whole trick moved into the fork.
This is what makes the client thin, and it is how `evalAsync` and `recipe` work today.

Background on why the hack existed (the fork's *sync* `EVAL` still behaves this way, so it matters
if you ever drop to it): sync `EVAL` **evaluates a SINGLE EXPRESSION and does NOT await promises.**
Proven:
- `(function(){var a=1; return a+2;})()` → `3`  ✓ (single-line expression)
- the same with an internal **newline** → `null`  ✗ (ANY newline breaks sync EVAL)
- `Promise.resolve(42)` → `{}`  ✗ (returns the promise, un-awaited)

For anything multiline or async, use `evalAsync` / `EVALASYNC` (native await) instead of sync
`EVAL`. Net effect: **posting needs no composer, no clicks, no screenshots, no OS focus** — it's
the same internal API the web client fires (`post.js` notes the DOM composer "needs real OS focus
to mount Quill" — the flaky-click wall; API-replay sidesteps it entirely).

## FAST-LEARNING (how any agent learns a new site quickly)

1. **`chrome-agent capture arm`** → instruments the page's fetch/XHR (browser-research
   `capture:arm`). Do the action **by hand** in the window (post, like, whatever). Then
   **`chrome-agent capture dump`** → the exact method/URL/body the site actually fired. That
   is the API to replay. Promote it into `browser-research/.../recipes/<site>.js` (see that
   skill's `workflow.md` step 4) so every agent inherits it — then it's a `recipe`.
2. **`chrome-agent learn <name> [url]`** → the visual side: screenshot (Read it — ground
   truth) + full DOM dump (grep for stable text/href/aria). Use for UI-only actions where no
   clean API exists.

## Fallback: real input (only for genuinely UI-only actions)

`cj type` (real keyboard into the focused field) is reliable; `click <x> <y>` maps 1:1 to
screenshot pixels but is INTERMITTENTLY flaky (fork was mid bug-fix) — prefer keyboard, and
always screenshot-verify a click landed. Abort a draft with `key escape` → Discard. A saved
LinkedIn draft never self-posts, so it's safe to leave. Almost never needed now that posting =
API replay.
