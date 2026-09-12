---
verbs:
  item: chrome-agent hackernews item
  top: chrome-agent hackernews top
  eval: chrome-agent evalwithcsp '<js>'
  page: https://news.ycombinator.com/
---

# news.ycombinator.com — read

- `hackernews:item` — read a thread back by url or id — comments with depth, and the [flagged]/[dead] a 200 hides
- `hackernews:top` — read the HN front page: title, points, comments, item link

A read is a **sample**, not a set — these surfaces are personalised and paginated.
Say what you actually saw; never imply completeness.

Confirm which page you are on before believing a read: `goto` then `status`.
