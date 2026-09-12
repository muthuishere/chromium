---
verbs:
  comment: chrome-agent recipe linkedin:comment
  comment-delete: chrome-agent recipe linkedin:comment-delete
  delete-post: chrome-agent recipe linkedin:delete-post
  like: chrome-agent linkedin like
  post: chrome-agent recipe linkedin:post
  post-image: chrome-agent recipe linkedin:post-image
---

# linkedin.com — write

**Staged by default.** Every write is prepared and held until `--confirm`.
That is what keeps publishing owner-gated by construction; do not route around it.

Confirm the identity first — apl names the handle that owns this site
(`apl identity get --site linkedin.com`). Posting from the wrong one is the failure this
design exists to prevent.

- `linkedin:comment` — WRITE: comment/reply on a LinkedIn post by driving the comment box UI (fills Quill editor-aware, clicks the box's submit). Open the post's permalink first. Staged unless opts.confirm=true. opts:{text,confirm,selector?}.
- `linkedin:comment-delete` — WRITE: delete a comment WE posted (the undo for linkedin:comment) via the comment's ⋯ options menu -> Delete -> confirm. Open the post's permalink first; target precisely with opts.matchText (a substring of our comment). Staged unless opts.confirm=true. opts:{matchText,confirm}.
- `linkedin:delete-post` — WRITE: delete a LinkedIn post by urn. Staged unless opts.confirm=true. opts:{urn,confirm}.
- `linkedin:like` — like the post at <url> (or the first feed post) — verified by the reaction button flipping state  _(chrome-agent verb, not a registry recipe)_
- `linkedin:post` — WRITE: create a LinkedIn post via voyager normShares (verified 201). Staged unless confirm. opts:{text,confirm,connectionsOnly,allowedCommentersScope}.
- `linkedin:post-image` — WRITE, KNOWN BROKEN (2026-07-10): create a LinkedIn post with an image via voyager media upload. Register + PUT upload both succeed (asset goes READY) and normShares returns 201, but the published post does NOT render the image in the feed on any payload shape tried (media:[{status,mediaUrn}], content.contentEntities w/ + w/o thumbnails) — verified live 4x, always text-only. DO NOT rely on this for a real visual until someone finds the correct schema; check for an image on the live post before treating this as done. opts:{text,imagePath|imageB64/imageMime/imageName,confirm,connectionsOnly}.

Verify by reading the artifact back from the live page. See `traps.md`.
