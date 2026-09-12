---
verbs:
  timeline: chrome-agent recipe x:timeline
  timeline-twitter: chrome-agent recipe x:timeline-twitter
  eval: chrome-agent evalwithcsp '<js>'
  page: https://x.com/
---

# x.com — read

- `x:timeline` — Scrape visible tweets on the current X timeline / profile (auto-scrolls).
- `x:timeline-twitter` — Same as x:timeline but for the legacy twitter.com host.

A read is a **sample**, not a set — these surfaces are personalised and paginated.
Say what you actually saw; never imply completeness.

Confirm which page you are on before believing a read: `goto` then `status`.
