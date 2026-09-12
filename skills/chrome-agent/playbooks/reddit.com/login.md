# reddit.com — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Where:** `https://www.reddit.com/login/`

Manual, by a human, in this profile — always (ADR-0008). Username/password, or Google/Apple SSO in a popup. 2FA is opt-in per account, so it may or may not appear. `chrome-agent login reddit.com` opens the page and types nothing.


**Sign out:** `chrome-agent logout reddit.com` — method `url` (`https://www.reddit.com/logout/`).

**Is this profile signed in?** `chrome-agent auth reddit.com` — exit 0 yes, 2 no.
**Sign in:** `chrome-agent login reddit.com` opens the page and types nothing.
On a server the window lives on a virtual display: `chrome-agent share start` (ADR 0003).

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.
