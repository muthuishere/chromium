# mastodon.social — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Where:** `https://mastodon.social/auth/sign_in`

Email + password on THIS instance only. A Mastodon account is instance-local: credentials for another instance will not work here, and the operator must know which instance the identity lives on before the window opens. Some instances put an OAuth/SSO provider on this page instead; mastodon.social does not.

**A second factor is likely — keep the window open until it is done.**


**Sign out:** `chrome-agent logout mastodon.social` — method `dom` (`https://mastodon.social/auth/sign_out`).

**Is this profile signed in?** `chrome-agent auth mastodon.social` — exit 0 yes, 2 no.
**Sign in:** `chrome-agent login mastodon.social` opens the page and types nothing.
On a server the window lives on a virtual display: `chrome-agent share start` (ADR 0003).

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.
