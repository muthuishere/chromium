# stackoverflow.com — traps

What this site lies about. Each entry cost someone real time.

- The session cookies (prov, acct, the per-site auth cookie) are HttpOnly — document.cookie cannot see them, and gating on them reports a live session as logged out.
- StackExchange.options.user.fkey EXISTS WHEN SIGNED OUT. It is the request-forgery key, not a session. Gating on fkey is a green light for an anonymous page; gate on userId / isRegistered.
- Being signed in to stackoverflow.com does not mean being signed in to superuser.com, serverfault.com or stackexchange.com. The ACCOUNT is network-wide; the SESSION is per site, and each one has its own first sign-in.
- Stack Overflow fronts everything with bot protection: a plain non-browser request to stackoverflow.com/questions answers HTTP 403 (observed 2026-09-12 from this machine). Every read has to happen inside the real browser, or through api.stackexchange.com.
- Because the probe is written from documentation and never run here, the 'as' it returns may be the display name with reputation glued on — treat the string as a hint until a live verify tidies the selector.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->

