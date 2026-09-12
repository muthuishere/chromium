# linkedin.com — traps

What this site lies about. Each entry cost someone real time.

- **Strict CSP kills `evalAsync`** — `script-src` without `unsafe-eval`. A plain eval fails
  silently: no error, no result. Use `evalwithcsp`.
- **The comment API returns HTTP 500 while succeeding.** The comment IS created. Retrying
  double-posts. Verify the artifact, never the status code.
- **Logged out looks like a page, not an error** — it redirects to
  `/login/?session_redirect=…`. See `login.md`.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->
- `li_at` is HttpOnly, so `document.cookie` can never see it — gating a signed-in check on it calls a perfectly live session logged out. Ask `/voyager/api/me` with the JSESSIONID csrf token and let the browser attach the cookie.  _(promoted 2026-09-12, from note)_
