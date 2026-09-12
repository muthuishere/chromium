# quora.com — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Where:** `https://www.quora.com/`

Signed out, the home page IS the login wall — Google / Facebook / email on one card. The provider buttons open a popup rather than navigating, so the operator must keep the share open and must not close what looks like a stray window. Quora is aggressive about new-device and new-IP checks and may hold the session behind an emailed code.

**A second factor is likely — keep the window open until it is done.**


**Sign out:** `chrome-agent logout quora.com` — method `dom` (`a[href*="/logout"]`).

**Is this profile signed in?** `chrome-agent auth quora.com` — exit 0 yes, 2 no.
**Sign in:** `chrome-agent login quora.com` opens the page and types nothing.
On a server the window lives on a virtual display: `chrome-agent share start` (ADR 0003).

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.
