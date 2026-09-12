# news.ycombinator.com — traps

What this site lies about. Each entry cost someone real time.

- **HN serves HTTP 200 on a dead or flagged post.** The status code is not evidence. Open the
  item and read it back as a logged-out visitor would see it.
- HN serves HTTP 200 on a dead or flagged post. The status code is not evidence. Open the item and read it back as a logged-out visitor would see it.
- One shared browser, no mutex. Two lanes attach to a nondeterministic tab. Run `chrome-agent goto <url>` then `chrome-agent status` before believing any read.
- A dead or flagged item still answers 200 and renders — the only evidence is the page text: `[flagged]` / `[dead]`. `chrome-agent hackernews item <id>` reports both, which is what the verification rule here always demanded. (promoted 2026-09-12, from note)
- hackernews:post submits a STORY to /newest — it is NOT the verb for commenting. Use hackernews:comment.
- Both write verbs replay a per-page token (fnid for submit, hmac for comment), so the page must be freshly loaded; a stale token 200s and does nothing.
- Verification rule: read the artifact back from the live page. A 2xx proves nothing here, and on some of these sites neither does a 5xx.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->
- A dead or flagged item still answers 200 and renders — the only evidence is the page text: `[flagged]` / `[dead]`. `chrome-agent hackernews item <id>` reports both, which is what the verification rule here always demanded.  _(promoted 2026-09-12, from note)_
