# substack.com — traps

What this site lies about. Each entry cost someone real time.

- GET /api/v1/subscriptions answers HTTP 401 with a JSON BODY when signed out — {"errors":[{"msg":"Please sign in", "msgHTML":"Please <a ...>sign in</a>."}]} (verified 2026-09-12). It is valid JSON and it parses cleanly, so any check of the shape 'the fetch resolved and the body parsed' calls a signed-out session live. Read the status, then read the field.
- window._preloads.userSettings exists on the signed-out page with user_id: null (verified 2026-09-12 on both substack.com/ and a publication subdomain). The key's presence is not the answer, its value is.
- substack.com/home 302-redirects to / when signed out (verified 2026-09-12) — not a 401, not a 404. A fetch that follows redirects lands on a 200 marketing page, which is exactly what 'it worked' looks like.
- Every publication is its own origin: <pub>.substack.com serves its own _preloads and its own /api/v1/*. A relative API fetch made from a publication page hits the publication host, not substack.com, and the two do not return the same user-scoped data. Probe on substack.com.
- In the HTML, _preloads is assigned as window._preloads = JSON.parse("...") — a JSON string inside a JSON string, so anything scraping the markup must unescape twice (this is what yt-dlp's extractor does). In a live page it is already an object; the probe handles both.
- Neither /api/v1/user nor /api/v1/profile exists on substack.com: both answer 404 with an HTML page, not a JSON error (verified 2026-09-12). Guessing a REST-looking who-am-I path here gets an HTML body and a confident wrong answer.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->

