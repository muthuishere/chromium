# instagram.com — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Where:** `https://www.instagram.com/accounts/login/`

Manual, by a human, in this profile — always (ADR-0008). Username/password, or 'continue with Facebook'; a new browser usually draws a 'save your login info' and often a code. Automated sign-in is the fastest way to get the account restricted.

**A second factor is likely — keep the window open until it is done.**


**Sign out:** `chrome-agent logout instagram.com` — method `cookies` (``).

⚠️ `cookies` is LOCAL ONLY: the site is never told, so its session stays alive until it expires on its own.

**Is this profile signed in?** `chrome-agent auth instagram.com` — exit 0 yes, 2 no.
**Sign in:** `chrome-agent login instagram.com` opens the page and types nothing.
On a server the window lives on a virtual display: `chrome-agent share start` (ADR 0003).

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.
