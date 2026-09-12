---
verbs:
  post: chrome-agent recipe hackernews:post
  comment: chrome-agent recipe hackernews:comment
---

# news.ycombinator.com — write

**Staged by default.** Every write is prepared and held until `--confirm`.
That is what keeps publishing owner-gated by construction; do not route around it.

Confirm the identity first — `browser-for news.ycombinator.com` names the handle that owns this
site. Posting from the wrong one is the failure this design exists to prevent.

- `hackernews:post` — WRITE: submit a Hacker News story via the real submit form (fnid token replay). Needs to be logged in on HN in the bridge profile. Staged unless opts.confirm=true. opts:{title,url,text,confirm}. NOTE: this submits a STORY to /newest — it is NOT the verb for commenting; use hackernews:comment.
- `hackernews:comment` — WRITE: comment on an HN story (or reply to a comment with opts.reply=true) via the real form (per-page hmac replay). Self-verifying: re-reads the live thread and returns liveUrl, since HN 200s on rate-limit/flag pages. Staged unless opts.confirm=true. opts:{id,text,reply,confirm}.

Verify by reading the artifact back from the live page. See `traps.md`.
