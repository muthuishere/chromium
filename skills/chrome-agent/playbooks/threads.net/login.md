# threads.net — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Where:** `https://www.threads.com/login`

Sign-in is delegated to Instagram: the page offers 'Continue with Instagram' (an instagram.com handshake that lands back on threads.com) or a direct username/password form for a Threads-only account. A human must complete it, and the Instagram hop means the browser leaves this origin and comes back — do not close the window mid-handshake.

**A second factor is likely — keep the window open until it is done.**


**Sign out:** `chrome-agent logout threads.net` — method `dom` (`https://www.threads.com/settings/account`).

**Is this profile signed in?** `chrome-agent auth threads.net` — exit 0 yes, 2 no.
**Sign in:** `chrome-agent login threads.net` opens the page and types nothing.
On a server the window lives on a virtual display: `chrome-agent share start` (ADR 0003).

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.
