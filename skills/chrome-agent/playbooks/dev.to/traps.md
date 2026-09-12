# dev.to — traps

What this site lies about. Each entry cost someone real time.

- The session cookies (_Forem_Session / remember_user_token) are HttpOnly — invisible to document.cookie, so a cookie check reports a live session as logged out.
- /async_info/base_data answers HTTP 200 WHEN SIGNED OUT. The signed-out body is {broadcast, param, token} with no `user` key at all. The status code is not the verdict; the presence of `user` is. Same failure shape as reddit's me.json.
- In that response `user` is a JSON STRING, not an object (the controller does user_data.to_json inside the JSON), so it needs a second JSON.parse before username exists.
- dev.to is edge-cached and much of the page is hydrated client-side, so a probe that runs a fraction of a second too early can see body.dataset.user unset on a page that is genuinely signed in — that is why the fetch fallback exists and why it, not the dataset, is authoritative.
- Anonymously fetched HTML always carries body[data-user-status="logged-out"] (confirmed here 2026-09-12), which is exactly what a cached page would also show. Never treat a scraped copy of the page as a session verdict.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->

