# medium.com — traps

What this site lies about. Each entry cost someone real time.

- `sid` is HttpOnly so document.cookie cannot see the live session, AND `uid` is left behind after sign-out so document.cookie can claim a session that no longer exists. Cookies are wrong in both directions here — this is the worst site in this set to cookie-check.
- Cloudflare fronts medium.com and answers a plain non-browser request with HTTP 403 (observed here 2026-09-12). Reads must run inside the real browser; curl-style fetching is not a fallback.
- The internal /_/api/* endpoints prefix their JSON with an XSSI guard (`])}while(1);</x>`), so response.json() throws on a completely healthy reply. Read text, cut to the first '{'.
- Publication custom domains (and *.medium.com publications) render a different app shell. The Apollo state keys the probe looks for may simply not exist there, so always probe medium.com itself, never the article's vanity domain.
- Medium's inline state global has changed name over the years (__APOLLO_STATE__, __PRELOADED_STATE__, __NEXT_DATA__). The probe tries the first two and falls through to the API rather than trusting any single one.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->

