# instagram.com — traps

What this site lies about. Each entry cost someone real time.

- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

No site-specific trap has been recorded yet. That means nobody has
been bitten and written it down — not that this site is honest.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.
