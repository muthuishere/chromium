# github.com — traps

What this site lies about. Each entry cost someone real time.

- The session cookie (user_session) is HttpOnly — document.cookie cannot see it. Reading cookies here reports a live session as logged out. The meta tag is the evidence.
- api.github.com does NOT accept the browser session cookie: a credentialed fetch from the page to the API host answers 401 even while github.com itself is signed in. In-page reads must go to github.com paths (HTML, or /_graphql with the page's CSRF token); token-auth API work belongs to `gh`, not to this browser.
- Signed-out pages still render a full-looking header, and many repo pages look identical signed in or out. Only meta[name=user-login] distinguishes them.
- Under SAML SSO the account can be signed in and still 404 on org repos until the org session is authorised — that is not a logged-out state and the probe will (correctly) say signed_in:true.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->

