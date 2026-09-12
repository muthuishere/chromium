---
verbs:
  listing: chrome-agent recipe reddit:listing
  eval: chrome-agent evalwithcsp '<js>'
---

# reddit.com — read

- `reddit:listing` — Fetch the current subreddit/profile/post as JSON (append .json), credentialed.

A read is a **sample**, not a set — these surfaces are personalised and paginated.
Say what you actually saw; never imply completeness.

Confirm which page you are on before believing a read: `goto` then `status`.
