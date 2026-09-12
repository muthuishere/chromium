# news.ycombinator.com — traps

What this site lies about. Each entry cost someone real time.

- **HN serves HTTP 200 on a dead or flagged post.** The status code is not evidence. Open the
  item and read it back as a logged-out visitor would see it.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.
