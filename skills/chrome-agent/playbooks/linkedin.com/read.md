---
verbs:
  notifications: chrome-agent recipe linkedin:notifications
  my-posts: chrome-agent recipe linkedin:my-posts
  feed: chrome-agent recipe linkedin:feed
  eval: chrome-agent evalwithcsp '<js>'
---

# linkedin.com — read

- `linkedin:notifications` — Inbound notifications via credentialed voyager API (numUnseen + cards). Run it from /feed/ — the /notifications/ page itself wedges every recipe eval.
- `linkedin:my-posts` — Own posts from your recent-activity page (DOM scrape, CSP-immune).
- `linkedin:feed` — Home feed via credentialed voyager API.

A read is a **sample**, not a set — these surfaces are personalised and paginated.
Say what you actually saw; never imply completeness.

Confirm which page you are on before believing a read: `goto` then `status`.
