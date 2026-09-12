---
verbs:
  delete-post: chrome-agent recipe x:delete-post
  like: chrome-agent x like
  post: chrome-agent recipe x:post
  reply: chrome-agent recipe x:reply
  repost: chrome-agent x repost
---

# x.com — write

**Staged by default.** Every write is prepared and held until `--confirm`.
That is what keeps publishing owner-gated by construction; do not route around it.

Confirm the identity first — apl names the handle that owns this site
(`apl identity get --site x.com`). Posting from the wrong one is the failure this
design exists to prevent.

- `x:delete-post` — WRITE: delete a tweet/reply by driving the native UI (⋯ caret -> Delete -> confirm sheet), no API replay. Open the tweet first; target opts.tweetId or the first caret. Staged unless opts.confirm=true. opts:{tweetId,confirm}.
- `x:like` — like the tweet at <url> — verified by data-testid flipping like -> unlike  _(chrome-agent verb, not a registry recipe)_
- `x:post` — WRITE: create a tweet by driving the native composer — fills it directly (DraftJS paste) whether the inline composer is already open or opened on demand, optional image via the file input, then clicks Post (bypasses 226). Staged unless opts.confirm=true. opts:{text,imagePath|imageB64/imageMime/imageName,confirm}.
- `x:reply` — WRITE: reply to a tweet by driving the composer (fills DraftJS via paste, optional image via the composer file input, clicks Reply; bypasses 226 — X's JS computes x-client-transaction-id). Open the tweet's permalink first. Staged unless opts.confirm=true. Returns mediaRequested/mediaAttached so an ignored image is visible. NOTE: chrome-agent does NOT expand imagePath — pass imageB64. opts:{text,imageB64/imageMime/imageName,confirm}.
- `x:repost` — repost the tweet at <url> — verified by data-testid flipping retweet -> unretweet  _(chrome-agent verb, not a registry recipe)_

Verify by reading the artifact back from the live page. See `traps.md`.
