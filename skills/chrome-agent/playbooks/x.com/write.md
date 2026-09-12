---
verbs:
  post: chrome-agent recipe x:post
  reply: chrome-agent recipe x:reply
  delete-post: chrome-agent recipe x:delete-post
---

# x.com — write

**Staged by default.** Every write is prepared and held until `--confirm`.
That is what keeps publishing owner-gated by construction; do not route around it.

Confirm the identity first — `browser-for x.com` names the handle that owns this
site. Posting from the wrong one is the failure this design exists to prevent.

- `x:post` — WRITE: create a tweet by driving the native composer — fills it directly (DraftJS paste) whether the inline composer is already open or opened on demand, optional image via the file input, then clicks Post (bypasses 226). Staged unless opts.confirm=true. opts:{text,imagePath|imageB64/imageMime/imageName,confirm}.
- `x:reply` — WRITE: reply to a tweet by driving the composer (fills DraftJS via paste, optional image via the composer file input, clicks Reply; bypasses 226 — X's JS computes x-client-transaction-id). Open the tweet's permalink first. Staged unless opts.confirm=true. Returns mediaRequested/mediaAttached so an ignored image is visible. NOTE: chrome-agent does NOT expand imagePath — pass imageB64. opts:{text,imageB64/imageMime/imageName,confirm}.
- `x:delete-post` — WRITE: delete a tweet/reply by driving the native UI (⋯ caret -> Delete -> confirm sheet), no API replay. Open the tweet first; target opts.tweetId or the first caret. Staged unless opts.confirm=true. opts:{tweetId,confirm}.

Verify by reading the artifact back from the live page. See `traps.md`.
