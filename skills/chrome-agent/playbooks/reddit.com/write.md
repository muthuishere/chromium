---
verbs:
  comment: chrome-agent recipe reddit:comment
  delete-post: chrome-agent recipe reddit:delete-post
  post: chrome-agent recipe reddit:post
  post-oauth: chrome-agent recipe reddit:post-oauth
  submit: chrome-agent recipe reddit:submit
  upvote: chrome-agent reddit upvote
---

# reddit.com — write

**Staged by default.** Every write is prepared and held until `--confirm`.
That is what keeps publishing owner-gated by construction; do not route around it.

Confirm the identity first — apl names the handle that owns this site
(`apl identity get --site reddit.com`). Posting from the wrong one is the failure this
design exists to prevent.

- `reddit:comment` — WRITE: comment/reply on a Reddit post (t3_) or comment (t1_) via /api/comment. Staged unless opts.confirm=true. Returns t1_ id (delete via reddit:delete-post). opts:{thingId,text,confirm}.
- `reddit:delete-post` — WRITE: delete a Reddit post OR comment by fullname (t3_/t1_). Staged unless opts.confirm=true. opts:{fullname,confirm}.
- `reddit:post` — WRITE: submit a Reddit self-post via API. Staged unless opts.confirm=true. opts:{subreddit,title,text,confirm}.
- `reddit:post-oauth` — WRITE: submit via the OFFICIAL oauth.reddit.com/api/submit using the session's own bearer token (no app registration; the Postiz endpoint). Token read+used in-page, never returned. Staged unless opts.confirm=true. opts:{subreddit,title,text,url,confirm}.
- `reddit:submit` — WRITE: create a Reddit text post by driving the /submit page UI (shadow-DOM aware; sidesteps the captcha-gated /api/submit). Open r/<sub>/submit/?type=TEXT first. Staged unless opts.confirm=true. opts:{title,text,confirm,probe}.
- `reddit:upvote` — upvote the post at <url> — verified by aria-pressed becoming true  _(chrome-agent verb, not a registry recipe)_

Verify by reading the artifact back from the live page. See `traps.md`.
