# reddit.com — traps

What this site lies about. Each entry cost someone real time.

- One shared browser, no mutex. Two lanes attach to a nondeterministic tab. Run `chrome-agent goto <url>` then `chrome-agent status` before believing any read.
- The .json listing API answers HTTP 200 when signed OUT, with a body of {"features":{...}} and no `name` — the status code is not the verdict, the presence of a field is. (promoted 2026-09-12, from note)
- A listing ROOT needs `/.json`, not `.json`: stripping the trailing slash off https://www.reddit.com leaves the bare origin, so appending .json builds the HOST www.reddit.com.json and fetch rejects with `TypeError: Failed to fetch` — a DNS failure that never mentions the URL. (promoted 2026-09-12, from note)
- /api/submit is captcha-gated; reddit:submit drives the /submit page UI (shadow-DOM aware) to sidestep that, and needs r/<sub>/submit/?type=TEXT open first.
- Verification rule: read the artifact back from the live page. A 2xx proves nothing here, and on some of these sites neither does a 5xx.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->
- The .json listing API answers HTTP 200 when signed OUT, with a body of {"features":{...}} and no `name` — the status code is not the verdict, the presence of a field is.  _(promoted 2026-09-12, from note)_
- A listing ROOT needs `/.json`, not `.json`: stripping the trailing slash off https://www.reddit.com leaves the bare origin, so appending .json builds the HOST www.reddit.com.json and fetch rejects with `TypeError: Failed to fetch` — a DNS failure that never mentions the URL.  _(promoted 2026-09-12, from note)_
