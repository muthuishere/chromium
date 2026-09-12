# indiehackers.com — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Where:** `https://www.indiehackers.com/sign-in`

Email + password, with Google and X as alternatives. Historically an emailed sign-in link is offered too, and that link opens a session in whichever browser handles it — which must be this one. No SSO redirect chain for the email path, so the share can usually be closed as soon as the feed renders.


**Sign out:** `chrome-agent logout indiehackers.com` — method `dom` (`a[href*="sign-out"], a[href*="signout"], button[data-action*="signOut"]`).

**Is this profile signed in?** `chrome-agent auth indiehackers.com` — exit 0 yes, 2 no.
**Sign in:** `chrome-agent login indiehackers.com` opens the page and types nothing.
On a server the window lives on a virtual display: `chrome-agent share start` (ADR 0003).

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.
