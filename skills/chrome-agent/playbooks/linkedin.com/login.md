# linkedin.com — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Where:** `https://www.linkedin.com/login`

Manual, by a human, in this profile — always (ADR-0008). `chrome-agent login linkedin.com` opens the page and types nothing. Email/password plus, on a new device, an emailed or app code; LinkedIn also sometimes shows a puzzle challenge. Logged out does NOT look like an error: the site redirects to /login/?session_redirect=... and renders a perfectly normal page.

**A second factor is likely — keep the window open until it is done.**


**Sign out:** `chrome-agent logout linkedin.com` — method `url` (`https://www.linkedin.com/m/logout/`).

**Is this profile signed in?** `chrome-agent auth linkedin.com` — exit 0 yes, 2 no.
**Sign in:** `chrome-agent login linkedin.com` opens the page and types nothing.
On a server the window lives on a virtual display: `chrome-agent share start` (ADR 0003).

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.
