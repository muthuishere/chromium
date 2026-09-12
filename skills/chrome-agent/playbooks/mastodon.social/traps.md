# mastodon.social — traps

What this site lies about. Each entry cost someone real time.

- The <script id="initial-state"> element is present on the SIGNED-OUT page too — verified on mastodon.social 4.8.0-alpha on 2026-09-12, where it carried "access_token": null and no "me". Gating on 'the initial-state element exists' reports every anonymous visitor as signed in.
- Almost everything worth reading is readable with NO session: /api/v1/timelines/public, /api/v1/accounts/lookup, an account's statuses, the whole /explore surface. A feed that loads is therefore zero evidence of a session — only a 'who am I' endpoint or meta.me is.
- /api/v1/accounts/verify_credentials answers 401 {"error":"The access token is invalid"} with no session (verified 2026-09-12) — it does NOT redirect and it does not 403. Any other endpoint's 200 must not be read as authentication.
- The instance is the boundary. Cookies, the OAuth token in initial-state, the handle and the whole session are scoped to this host; being live on mastodon.social says nothing about any other instance, and the same person is a different account on each.
- Mastodon rate-limits the API per token (documented default: a few hundred requests per 5 minutes, tighter for writes) and answers 429 with X-RateLimit-* headers. A throttled read comes back as a short or empty timeline rather than an obvious error, so a recipe must check the status and those headers before believing an empty feed.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->

