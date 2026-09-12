# indiehackers.com — traps

What this site lies about. Each entry cost someone real time.

- The whole site reads fine while signed out — feed, posts, comments, milestones. There is no content difference to detect a session by; only the header controls and the composer change.
- The session cookie is HttpOnly, so document.cookie is blind to it on a live session.
- '/api/v3/me' in this probe is an UNVERIFIED guess at the who-am-I endpoint. It is written to fall through silently on 404/HTML rather than to conclude anything, so a wrong path costs nothing — but do not read a green verdict from that layer until a live run has confirmed the path exists.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->

