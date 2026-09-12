# youtube.com — traps

What this site lies about. Each entry cost someone real time.

- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

No site-specific trap has been recorded yet. That means nobody has
been bitten and written it down — not that this site is honest.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->
- The readable cookies (PREF, __Secure-3PAPISID) are present when signed OUT; the meaningful ones (LOGIN_INFO, SAPISID) are HttpOnly. `ytcfg.data_.LOGGED_IN` is YouTube stating it itself — use that.  _(promoted 2026-09-12, from note)_
- A read recipe here scrapes THE CURRENT PAGE, so the homepage yields 0 items and looks like drift. Verify from a results or channel page (`chrome-agent verify` uses /results?search_query=chromium).  _(promoted 2026-09-12, from note)_
