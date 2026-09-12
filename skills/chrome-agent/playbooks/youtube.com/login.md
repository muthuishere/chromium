# youtube.com — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Where:** `https://accounts.google.com/ServiceLogin?service=youtube`

Manual, by a human, in this profile — always (ADR-0008). This is a GOOGLE sign-in, not a YouTube one: signing in here signs the profile into every Google property, and Google is the most likely of these sites to challenge an unfamiliar browser (device prompt, passkey, or a code).

**A second factor is likely — keep the window open until it is done.**


**Sign out:** `chrome-agent logout youtube.com` — method `url` (`https://accounts.google.com/Logout`).

**Is this profile signed in?** `chrome-agent auth youtube.com` — exit 0 yes, 2 no.
**Sign in:** `chrome-agent login youtube.com` opens the page and types nothing.
On a server the window lives on a virtual display: `chrome-agent share start` (ADR 0003).

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.
