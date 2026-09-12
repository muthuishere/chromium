---
verbs:
  feed: chrome-agent recipe facebook:feed
  eval: chrome-agent evalwithcsp '<js>'
  page: https://www.facebook.com/
---

# facebook.com — read

- `facebook:feed` — Scrape visible posts in the current Facebook feed/profile (auto-scrolls, expands 'See more').

A read is a **sample**, not a set — these surfaces are personalised and paginated.
Say what you actually saw; never imply completeness.

Confirm which page you are on before believing a read: `goto` then `status`.
