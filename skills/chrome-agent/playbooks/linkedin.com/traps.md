# linkedin.com — traps

What this site lies about. Each entry cost someone real time.

- **Strict CSP kills `evalAsync`** — `script-src` without `unsafe-eval`. A plain eval fails
  silently: no error, no result. Use `evalwithcsp`.
- **The comment API returns HTTP 500 while succeeding.** The comment IS created. Retrying
  double-posts. Verify the artifact, never the status code.
- **Logged out looks like a page, not an error** — it redirects to
  `/login/?session_redirect=…`. See `login.md`.
- Strict CSP kills `evalAsync` — `script-src` without `unsafe-eval`. A plain eval fails silently: no error, no result. Use `evalwithcsp`.
- The comment API returns HTTP 500 while succeeding. The comment IS created. Retrying double-posts. Verify the artifact, never the status code.
- Logged out looks like a page, not an error — it redirects to `/login/?session_redirect=...`.
- One shared browser, no mutex. Two lanes attach to a nondeterministic tab. Run `chrome-agent goto <url>` then `chrome-agent status` before believing any read.
- `li_at` is HttpOnly, so `document.cookie` can never see it — gating a signed-in check on it calls a perfectly live session logged out. Ask `/voyager/api/me` with the JSESSIONID csrf token and let the browser attach the cookie. (promoted 2026-09-12, from note)
- Origin is not enough for the voyager read recipes — they need /feed/ specifically. Proven 2026-07-21: from /in/me/recent-activity (host matches) linkedin:feed TIMED OUT; the same call from /feed/ returned 20 posts. linkedin:my-posts is the mirror image — run from /feed/ it returns postCount:0, which is indistinguishable from 'we have no posts'.
- linkedin:post-image is KNOWN BROKEN (2026-07-10): register + upload succeed and normShares returns 201, but the published post renders text-only. Check the live post for the image before calling it done.
- Verification rule: read the artifact back from the live page. A 2xx proves nothing here, and on some of these sites neither does a 5xx.
- The signed-in probe must FAIL CLOSED. A `catch` that returns signed_in:true turns a network blip into a green light on the one site where a false green already cost 44 hours of silent non-posting.
- JSESSIONID is the CSRF cookie, not a session: it is set for anonymous visitors too. Its absence is evidence; its presence is not. Only /voyager/api/me decides.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->
- `li_at` is HttpOnly, so `document.cookie` can never see it — gating a signed-in check on it calls a perfectly live session logged out. Ask `/voyager/api/me` with the JSESSIONID csrf token and let the browser attach the cookie.  _(promoted 2026-09-12, from note)_
