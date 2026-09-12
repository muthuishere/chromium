# news.ycombinator.com — login

**Manual, by a human, in the profile. Always** (ADR-0008).

Nothing here stores, types or automates a password, and nothing touches 2FA.
Automated sign-in is the most reliable way to get an account restricted, and a stored
credential would put a secret inside an agent's context.

**Expired session:** reads land on a login wall instead of content. That is the signal —
not an error, not an empty page.

**What to do:** stop and name the profile that needs a human.

```
apl accounts --check
```

Do not retry in a loop meanwhile.
