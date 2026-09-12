# bsky.app — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Where:** `https://bsky.app/`

The landing page IS the login form (handle or email + password, or 'Sign in with a different account' for a self-hosted PDS). No SSO, no QR. An app password from Settings works here too and is the safer thing to type into a shared browser profile. On success the client writes the session into localStorage under BSKY_STORAGE for this origin only.

**A second factor is likely — keep the window open until it is done.**


**Sign out:** `chrome-agent logout bsky.app` — method `dom` (`https://bsky.app/settings/account`).

**Is this profile signed in?** `chrome-agent auth bsky.app` — exit 0 yes, 2 no.
**Sign in:** `chrome-agent login bsky.app` opens the page and types nothing.
On a server the window lives on a virtual display: `chrome-agent share start` (ADR 0003).

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.
