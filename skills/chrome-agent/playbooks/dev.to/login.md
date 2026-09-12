# dev.to — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Where:** `https://dev.to/enter`

Mostly third-party SSO — GitHub, Google, Apple, Forem Passport — plus email+password. The SSO buttons open the provider's own page, so the human finishes there and lands back on dev.to. No second factor of its own; whatever the provider enforces applies.


**Sign out:** `chrome-agent logout dev.to` — method `url` (`https://dev.to/signout_confirm`).

**Is this profile signed in?** `chrome-agent auth dev.to` — exit 0 yes, 2 no.
**Sign in:** `chrome-agent login dev.to` opens the page and types nothing.
On a server the window lives on a virtual display: `chrome-agent share start` (ADR 0003).

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.
