# substack.com — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Where:** `https://substack.com/sign-in`

Substack prefers a passwordless emailed magic link ('Sign in with email'); there is also a password form behind 'Sign in with password', plus Google/Apple/Twitter buttons. The magic-link flow means the human needs the mailbox open in another window, and the link opens a NEW tab which is where the session actually lands.

**A second factor is likely — keep the window open until it is done.**


**Sign out:** `chrome-agent logout substack.com` — method `url` (`https://substack.com/sign-out`).

**Is this profile signed in?** `chrome-agent auth substack.com` — exit 0 yes, 2 no.
**Sign in:** `chrome-agent login substack.com` opens the page and types nothing.
On a server the window lives on a virtual display: `chrome-agent share start` (ADR 0003).

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.
