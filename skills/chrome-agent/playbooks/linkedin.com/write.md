---
verbs:
  post: chrome-agent recipe linkedin:post
  post-image: chrome-agent recipe linkedin:post-image
  delete-post: chrome-agent recipe linkedin:delete-post
  comment: chrome-agent recipe linkedin:comment
  comment-delete: chrome-agent recipe linkedin:comment-delete
---

# linkedin.com — write

**Staged by default.** Every write is prepared and held until `--confirm`.
That is what keeps publishing owner-gated by construction; do not route around it.

Confirm the identity first — `browser-for linkedin.com` names the handle that owns this
site. Posting from the wrong one is the failure this design exists to prevent.

- `linkedin:post` — WRITE: create a LinkedIn post via voyager normShares (verified 201). Staged unless confirm. opts:{text,confirm,connectionsOnly,allowedCommentersScope}.
- `linkedin:post-image` — no description in the registry
- `linkedin:delete-post` — WRITE: delete a LinkedIn post by urn. Staged unless opts.confirm=true. opts:{urn,confirm}.
- `linkedin:comment` — WRITE: comment/reply on a LinkedIn post by driving the comment box UI (fills Quill editor-aware, clicks the box's submit). Open the post's permalink first. Staged unless opts.confirm=true. opts:{text,confirm,selector?}.
- `linkedin:comment-delete` — WRITE: delete a comment WE posted (the undo for linkedin:comment) via the comment's ⋯ options menu -> Delete -> confirm. Open the post's permalink first; target precisely with opts.matchText (a substring of our comment). Staged unless opts.confirm=true. opts:{matchText,confirm}.

Verify by reading the artifact back from the live page. See `traps.md`.
