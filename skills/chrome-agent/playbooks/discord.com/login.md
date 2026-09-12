# discord.com — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Where:** `https://discord.com/login`

Email + password, or the QR code scanned from a logged-in phone. Discord challenges new devices and new IPs hard: expect an emailed verification code, a hCaptcha, and on some accounts a mandatory authenticator prompt. Keep the share open — this is one of the sites most likely to need several minutes of a human's hands.

**A second factor is likely — keep the window open until it is done.**


**Sign out:** `chrome-agent logout discord.com` — method `dom` (`[aria-label="Log Out"], [class*="logout"]`).

**Is this profile signed in?** `chrome-agent auth discord.com` — exit 0 yes, 2 no.
**Sign in:** `chrome-agent login discord.com` opens the page and types nothing.
On a server the window lives on a virtual display: `chrome-agent share start` (ADR 0003).

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.
