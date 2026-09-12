# x.com — traps

What this site lies about. Each entry cost someone real time.

- **Strict CSP** — `evalAsync` fails silently; use `evalwithcsp`.
- **Rate limiting appears as an empty timeline**, not an error. Empty never means "nothing
  there"; it means try later.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->
- `auth_token` is HttpOnly; `ct0` (the csrf cookie) is not and only exists for a signed-in session. Corroborate with the profile link — it is also the only source of the handle.  _(promoted 2026-09-12, from note)_
