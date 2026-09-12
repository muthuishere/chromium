# producthunt.com — traps

What this site lies about. Each entry cost someone real time.

- The session cookie is HttpOnly. document.cookie sees nothing on a live session, so any probe that gates on a cookie name reports logged-out while the account is perfectly signed in.
- The signed-out home page renders a full feed of the day's launches. A read that 'got posts back' is NOT evidence of a session — the products load either way; only the header controls differ.
- Upvote and comment state is client-rendered after hydration. Reading the page too early returns a feed with no interaction state at all, which looks like a signed-out page.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->

