# medium.com — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Where:** `https://medium.com/m/signin`

Medium prefers passwordless: Google/Facebook/Apple/X SSO, or an emailed magic sign-in link with no password at all. The magic-link path means the human needs their mailbox open, and the link must be opened IN THIS PROFILE's browser or the session lands somewhere else.


**Sign out:** `chrome-agent logout medium.com` — method `url` (`https://medium.com/m/signout`).

**Is this profile signed in?** `chrome-agent auth medium.com` — exit 0 yes, 2 no.
**Sign in:** `chrome-agent login medium.com` opens the page and types nothing.
On a server the window lives on a virtual display: `chrome-agent share start` (ADR 0003).

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.
