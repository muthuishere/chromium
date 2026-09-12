---
verbs:
  post: chrome-agent recipe instagram:post
  profile: chrome-agent recipe instagram:profile
  eval: chrome-agent evalwithcsp '<js>'
  page: https://www.instagram.com/instagram/
---

# instagram.com — read

- `instagram:post` — Caption + media URLs from a single open Instagram post.
- `instagram:profile` — Scrape post/reel links + thumbnails + alt-captions from the current IG profile grid.

A read is a **sample**, not a set — these surfaces are personalised and paginated.
Say what you actually saw; never imply completeness.

Confirm which page you are on before believing a read: `goto` then `status`.
