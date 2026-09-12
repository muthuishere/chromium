# x.com — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Where:** `https://x.com/login`

Manual, by a human, in this profile — always (ADR-0008). Handle/email plus password; X very often inserts an extra 'confirm your username/phone' step and an authenticator or emailed code on a new device. `chrome-agent login x.com` opens the page and types nothing.

**A second factor is likely — keep the window open until it is done.**


**Sign out:** `chrome-agent logout x.com` — method `cookies` (``).

⚠️ `cookies` is LOCAL ONLY: the site is never told, so its session stays alive until it expires on its own.

**Is this profile signed in?** `chrome-agent auth x.com` — exit 0 yes, 2 no.
**Sign in:** `chrome-agent login x.com` opens the page and types nothing.
On a server the window lives on a virtual display: `chrome-agent share start` (ADR 0003).

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.
