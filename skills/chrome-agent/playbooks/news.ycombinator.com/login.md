# news.ycombinator.com — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Where:** `https://news.ycombinator.com/login`

Manual, by a human, in this profile — always (ADR-0008). Plain username/password form, no SSO and no second factor; the session is a single `user` cookie, so it is long-lived but dies outright when it dies.


**Sign out:** `chrome-agent logout news.ycombinator.com` — method `dom` (`a[href^="logout"]`).

**Is this profile signed in?** `chrome-agent auth news.ycombinator.com` — exit 0 yes, 2 no.
**Sign in:** `chrome-agent login news.ycombinator.com` opens the page and types nothing.
On a server the window lives on a virtual display: `chrome-agent share start` (ADR 0003).

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.
