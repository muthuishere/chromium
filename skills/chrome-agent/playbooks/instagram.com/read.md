---
verbs:
  profile: chrome-agent recipe instagram:profile
  post: chrome-agent recipe instagram:post
  eval: chrome-agent evalwithcsp '<js>'
---

# instagram.com — read

- `instagram:profile` — Scrape post/reel links + thumbnails + alt-captions from the current IG profile grid.
- `instagram:post` — Caption + media URLs from a single open Instagram post.

A read is a **sample**, not a set — these surfaces are personalised and paginated.
Say what you actually saw; never imply completeness.

Confirm which page you are on before believing a read: `goto` then `status`.
