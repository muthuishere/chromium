# instagram.com — traps

What this site lies about. Each entry cost someone real time.

- One shared browser, no mutex. Two lanes attach to a nondeterministic tab. Run `chrome-agent goto <url>` then `chrome-agent status` before believing any read.
- instagram:profile scrapes the CURRENT page and wants an open profile grid; instagram:post wants an open post, not a profile root. Pointed at the wrong page a read can still 'succeed' — on 2026-09-12 `verify instagram.com` ran against whatever the tab held, scraped REDDIT, found images and stamped instagram verified. Navigate first, then read.
- The probe reads `ds_user_id`, the readable half of the session (sessionid is HttpOnly). It is an account ID, never a handle, and it can outlive the session cookie — so a true verdict here is weaker evidence than LinkedIn's or Reddit's API probes.
- No write verb exists for this site. Do not improvise one by driving the DOM.
- Verification rule: read the artifact back from the live page. A 2xx proves nothing here, and on some of these sites neither does a 5xx.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->

