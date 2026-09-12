# github.com — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Where:** `https://github.com/login`

Password then a second factor — GitHub has required 2FA for contributors since 2024, so expect a TOTP/passkey/device prompt every fresh profile. SSO orgs add a second, per-org 'Authorize' step AFTER sign-in: the account is live but org-owned repos 404 until that SAML session is granted. A human types everything.

**A second factor is likely — keep the window open until it is done.**


**Sign out:** `chrome-agent logout github.com` — method `dom` (`https://github.com/logout`).

**Is this profile signed in?** `chrome-agent auth github.com` — exit 0 yes, 2 no.
**Sign in:** `chrome-agent login github.com` opens the page and types nothing.
On a server the window lives on a virtual display: `chrome-agent share start` (ADR 0003).

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.
