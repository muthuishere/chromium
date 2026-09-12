# ADR 0005 — Profile and session lifecycle are CLI verbs, and logout is one of them

- **Status:** **BUILT, 2026-09-12.** All six verbs exist. `logout` was tested on all three methods
  (`url` against a real signed-out site; `dom` and `cookies` against a synthetic fixture, so no real
  session was destroyed to prove it) and it verifies with `auth` afterwards. `profile delete`'s
  guards fire — and building them found that BOTH lock checks used `-e` on a symlink whose target
  never exists, so "is a browser running on this profile?" had never once been true.
- **Date:** 2026-09-12
- **Owner:** Muthu (fork maintainer)
- **Author:** Claude Code (chrome-agent session)
- **Severity:** MEDIUM — mostly formalising what exists, with one genuinely missing half (logout).
- **Related:** ADR 0003 (server), ADR 0004 (site definitions), apl ADR-0007 (`login` as a
  capability), apl ADR-0008 (manual login only).

---

## Context

A browser profile is the credential. Not a token, not a key — a directory. Everything that manages
it has therefore to be an explicit, auditable verb, and until today most of it was implicit:
apl could only check that a directory existed, `login` did not exist at all, and nothing could sign
a profile **out**.

The asymmetry is the part that matters. We built `login` and `auth` on 2026-09-12 and did not build
`logout`, which means the system can acquire an identity and cannot put one down. On a shared or
server machine that is backwards: the risky operation is the one that leaves a session lying
around, and that is exactly the one with no command.

## Decision

The lifecycle is six verbs, split by what they are scoped to.

### Profile-scoped — a profile is a container, not an identity

```
chrome-agent profile create <dir>     # make it, do not launch     [BUILT]
chrome-agent profile list             # what exists, and its spool, size, last use
chrome-agent profile delete <dir>     # destroy it, with confirmation
chrome-agent profile                  # which profile this invocation resolves to   [BUILT]
```

`profile delete` deletes a live credential. It therefore: refuses while a browser is running on that
profile; requires `--yes` (or an interactive confirmation) — never a bare invocation; refuses the
default profile unless `--force`; prints what it is about to destroy, including which domains that
profile is currently signed into, **before** asking. Deleting a profile is the only way to revoke a
browser identity we have, so it should read like a revocation, not like `rm`.

`profile list` exists because on a server nobody can see the directories, and a profile whose spool
is not what you expect is the bug class that already cost us two incidents (a trailing slash forked
one profile into two spools; a `basename` collision merged two profiles into one).

### Domain-scoped — a session belongs to a site, not to a profile

```
chrome-agent login  <domain>          # open the page for a HUMAN; types nothing   [BUILT]
chrome-agent auth   <domain>          # read-only {signed_in, as?}                 [BUILT]
chrome-agent logout <domain>          # end THIS site's session, leave the rest
```

**This split is the whole design.** A profile is signed into many sites at once, so a
profile-level "signed in" boolean is a lie the moment one cookie expires while another holds — and
a green light nobody verified is the failure this system exists to prevent.

`logout` inherits the same scoping and gets its method from the site definition (ADR 0004):
- `method: "url"` — navigate the site's own logout route. Correct by construction; the site does its
  own cleanup.
- `method: "dom"` — click the site's own logout control when there is no route.
- `method: "cookies"` — last resort: clear that origin's cookies. It ends the local session without
  telling the site, so the server-side session stays alive until it expires. That is a real
  difference and the output must say which one happened.

**`logout` must verify.** It runs `auth` afterwards and reports `signed_in: false`, or it failed.
Every other verb in this CLI verifies its effect; a logout that says "done" without checking is the
same lie as a 200 on a dead post.

### Exit codes stay the contract

`0 ok · 1 usage · 2 not signed in · 3 browser unreachable · 4 site refused`, machine-readable via
`exit-codes --json`. `logout` on an already-signed-out site is **0, not an error** — it is
idempotent, because the caller's intent ("be signed out") is satisfied.

## Alternatives rejected

| Option | Why not |
|---|---|
| **`logout` = delete the profile** | Destroys every other site's session to end one. The scoping mistake this ADR exists to prevent. |
| **`logout` = clear cookies, always** | Leaves the server-side session alive and the site unaware. Fine as a fallback, wrong as the default, and silently different from what the user asked for. |
| **A single `session` verb with subcommands** | `login`/`logout`/`auth` are three different risk levels. Flattening them makes the dangerous one easy to reach by typo. |
| **Let apl do profile management (mkdir / rm)** | apl creating a directory produces a logged-out profile that *looks* configured, and apl reports it healthy. The thing that owns the browser must own the container. |
| **Interactive prompts inside `login`** | It would put an agent in the credential path. `login` opens the window and gets out of the way; that is the entire safety property. |

## Consequences

- apl's blank `browser:<label>` row in ADR-0007 is filled with no new concept on its side, and
  `apl login browser:deemwar --site linkedin.com` means exactly one thing.
- Revoking a browser identity becomes a real operation instead of "delete a folder and hope".
- On a server (ADR 0003) `logout` is the safety valve: finish the job, put the identity down.
- `logout`'s three methods will behave differently per site, and the honest output ("we cleared
  cookies; the server session may still be live") will sometimes be the only correct answer.

## What would prove this

1. `logout <domain>` followed by `auth <domain>` returns `signed_in: false` — on a site using the
   `url` method and on one using `cookies`, with the output naming which.
2. `logout` on one domain leaves every other domain in that profile signed in. Checked, not assumed.
3. `profile delete` refuses a running profile, refuses the default without `--force`, and names the
   signed-in domains before it asks.
4. `profile list` on a machine with three profiles reports three distinct spools.
