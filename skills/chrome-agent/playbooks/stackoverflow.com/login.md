# stackoverflow.com — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Where:** `https://stackoverflow.com/users/login`

One Stack Exchange account signs in across the network, but sign-in happens per site: the page offers Google/GitHub/Facebook SSO plus email+password, and an SSO path opens a provider window that the human must finish. A new IP or a new profile commonly adds an emailed verification link or a CAPTCHA.


**Sign out:** `chrome-agent logout stackoverflow.com` — method `url` (`https://stackoverflow.com/users/logout`).

**Is this profile signed in?** `chrome-agent auth stackoverflow.com` — exit 0 yes, 2 no.
**Sign in:** `chrome-agent login stackoverflow.com` opens the page and types nothing.
On a server the window lives on a virtual display: `chrome-agent share start` (ADR 0003).

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.
