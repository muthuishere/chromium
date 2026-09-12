---
verbs:
  channel-videos: chrome-agent recipe youtube:channel-videos
  eval: chrome-agent evalwithcsp '<js>'
  page: https://www.youtube.com/results?search_query=chromium
---

# youtube.com — read

- `youtube:channel-videos` — Scrape video titles + links + metadata from the current channel / results page (auto-scrolls).

A read is a **sample**, not a set — these surfaces are personalised and paginated.
Say what you actually saw; never imply completeness.

Confirm which page you are on before believing a read: `goto` then `status`.
