# facebook.com — traps

What this site lies about. Each entry cost someone real time.

- One shared browser, no mutex. Two lanes attach to a nondeterministic tab. Run `chrome-agent goto <url>` then `chrome-agent status` before believing any read.
- A logged-out feed returns postCount:0 with no error — 'the verb ran' is not 'the verb works'. Zero items here means signed out or drifted, never 'nothing to see'.
- The probe reads `c_user`, which is readable but is only an account ID — it can never name a person, and it goes stale in a profile whose session cookie has expired while c_user lingers. Treat a c_user-only verdict as weaker evidence than the API probes the other sites use.
- No write verb exists for this site. Do not improvise one by driving the DOM.
- Verification rule: read the artifact back from the live page. A 2xx proves nothing here, and on some of these sites neither does a 5xx.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->

