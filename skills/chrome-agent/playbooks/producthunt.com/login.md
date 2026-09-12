# producthunt.com — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Where:** `https://www.producthunt.com/`

There is no password form worth aiming at: the header 'Sign in' opens a provider chooser (Google / X / LinkedIn / email magic-link). Every path leaves the origin, so the operator must stay on the share until the browser lands back on producthunt.com. The email path sends a link — the human clicks it in their mail client, and the session materialises in whichever browser opens that link, which must be THIS one.

**A second factor is likely — keep the window open until it is done.**


**Sign out:** `chrome-agent logout producthunt.com` — method `dom` (`a[href="/sign_out"], a[href*="sign_out"], [data-test="logout"]`).

**Is this profile signed in?** `chrome-agent auth producthunt.com` — exit 0 yes, 2 no.
**Sign in:** `chrome-agent login producthunt.com` opens the page and types nothing.
On a server the window lives on a virtual display: `chrome-agent share start` (ADR 0003).

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.
