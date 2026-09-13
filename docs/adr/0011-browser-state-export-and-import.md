# ADR 0011 — Exporting and importing browser state (cookies, storage) as an explicit credential

- **Status:** **BUILT, NOT YET RUN, 2026-09-13.** The engine has COOKIEEXPORT/COOKIEIMPORT (full
  cookie fidelity, domain-scoped, rejections reported, no value ever logged) and it compiles; the Go
  client owns encryption at rest (scrypt→AES-256-GCM, unit-tested), the ledger, and a non-zero exit
  on any rejected cookie. What is unproven is exactly what needs a running VERSION-era browser: a
  real export on one machine and import on another, and the cross-OS transfer this ADR says lifts
  0003 §4. Two ADR clauses were corrected by contact with the code — see §3 (encryption cannot live
  in the engine) and §5 (partitioned/opaque keys, and deriving scheme from Secure).
- **Date:** 2026-09-13
- **Owner:** Muthu (fork maintainer)
- **Author:** Claude Code (chrome-agent session)
- **Severity:** HIGH — it makes credential theft a first-class, one-command feature. That is exactly
  why it must be designed rather than added.
- **Related:** ADR 0003 §4 (the cross-OS profile-carry finding), ADR 0005 (lifecycle),
  ADR 0008 (Linux build), ADR 0009 (protocol + grants).

---

## Context

Two forces meet here.

**The capability is genuinely needed.** Moving a session between machines, seeding a server profile,
snapshotting a known-good login before a risky test, restoring one after — these are ordinary
operations that today have no answer but "log in again by hand".

**And it solves a problem ADR 0003 declared unsolvable.** That ADR established, from this fork's own
source, that a macOS profile cannot be carried to Linux: both platforms tag cookie ciphertext `v10`
with different keys (`keychain_key_provider.mm:28-66` vs `posix_key_provider.cc:17-23`), so Linux
decrypts the Mac's cookies with the constant `peanuts`, padding fails, and the rows are dropped as
`kDecryptFailed` **with no error surfaced anywhere**. The conclusion was: carry is Linux→Linux only.

Cookie export cuts that knot. A running browser has the cookies **decrypted in memory**. Exporting
from the live engine and importing on the other side is OS-independent, because the ciphertext never
travels. It is the only mechanism that makes cross-OS session transfer work at all.

**The cost is that the exported file is a bearer credential.** Not "sensitive data" — the actual
session. Anyone holding it is the user, on any machine, until the cookies expire. It is worse than a
password in one specific way: it bypasses 2FA, because the second factor was already satisfied when
the session was minted.

## Decision

**Ship export and import as first-class engine verbs, and treat the output as a credential in every
part of the design.**

### 1. It must be an engine verb, not a page eval

The cookies that matter — `li_at`, `auth_token`, `sessionid` — are **HttpOnly**, which is precisely
why `document.cookie` cannot see them and why every naive probe in this project reported live
sessions as logged out. A JS-based export would silently export the worthless half. The engine reads
the cookie store; the page cannot.

### 2. Scoped by default, never "everything"

```
cookie export --domain linkedin.com [--domain x.com] --out <file>
cookie import --in <file> [--domain …]
```

A bare `cookie export` with no domain is a refusal, not a convenience. The common case is one site;
the whole jar is an all-identities-at-once artifact and should be uncomfortable to produce.

### 3. Encrypted at rest, or it does not get written

The output is age/AES-encrypted with a passphrase or key the caller supplies. **No plaintext
default, not even to a file with mode 0600**, because a 0600 file is one `scp` away from a laptop
that gets lost. If the caller wants plaintext they pass a flag whose name says what it is
(`--i-know-this-is-a-credential`), and the file lands 0600 regardless.

### 4. Never through a grant, never over the remote plane

ADR 0009 lets a human be lent a tab. A viewer holding a `control` grant must not be able to walk out
with the identity they were lent. Export is a **local-only capability**: not exposed on the WS
control plane by default, and behind its own capability flag if ever enabled. Renting someone a tab
for ten minutes and renting them the account forever are different transactions.

### 5. Fidelity, or the import is a silent lie

A cookie is not a name/value pair. `Secure`, `HttpOnly`, `SameSite`, `Path`, `Domain`, expiry, and
the partition key all decide whether a session works. Round-trip all of them; on import, report what
was set and what was **rejected** — a browser silently drops cookies it dislikes, and a caller who
sees "imported 47" while 12 were refused has a broken session and a green light. This is the same
class of failure as everything else here: 200 on a dead post, a verified-but-empty read, a probe
that fails open.

### 6. It leaves a trace, and the trace carries no secret

Export and import append to the action ledger (which already carries `profile` and `agent`): *what
domains, when, by whom, to which file* — **never a value**. If an identity walks, the ledger should
say when and from where.

### 7. Storage beyond cookies, deliberately second

`localStorage` / IndexedDB hold sessions for some sites (Bluesky keeps its whole session there, which
is why its probe reads storage and not cookies). Same rules apply, but cookies ship first: they are
the majority case and the model is simpler.

## Alternatives rejected

| Option | Why not |
|---|---|
| **Copy the profile directory** | Broken across OSes by construction (ADR 0003 §4), and it moves 5 GB to carry 4 KB. |
| **Export via `document.cookie`** | Cannot see HttpOnly — i.e. cannot see the session. |
| **Plaintext JSON by default** | The most valuable file on the machine, lying in a downloads folder. |
| **Expose export on the remote plane** | Lending a tab must not lend the account. |
| **Refuse to build it** | The need is real; the alternative is people writing something worse with `sqlite3` on a copied `Cookies` file. |

## Consequences

- Cross-OS session transfer becomes possible for the first time — ADR 0003 §4's hard limit is lifted
  by a different mechanism, not contradicted.
- A new artifact exists whose loss is equivalent to account compromise, with no revocation except
  logging the session out at the site.
- `logout` gains weight: it is the revocation for a leaked export, and its `cookies` method — which
  only clears locally — explicitly is not.
- Any agent that can run this can exfiltrate an identity. The scoping, the encryption and the
  local-only rule are what keep that from being one careless prompt away.

## Corrected by contact with the code (2026-09-13)

- **§3, encrypt at rest, moved to the client.** The engine has no key management and its only output
  is the plaintext spool result file, so it cannot meaningfully encrypt. Encryption and the
  `--i-know-this-is-a-credential` flag live in the Go client, which reads and should delete the
  result promptly. (Done: scrypt→AES-256-GCM, `internal/cookies`.)
- **§5, fidelity has two limits.** Partitioned cookies with opaque/nonced keys cannot be serialized —
  the engine flags them `partition_key_unserializable` rather than dropping them silently. And import
  must derive the source URL scheme from the `Secure` flag, or every Secure session cookie is refused.
- **§4, "never through a grant" has no referent yet** — there is no WS plane to expose it on; the only
  guard today is that the spool is local.
- **§6, the ledger is the client's.** The engine logs only domain and counts.

## What would prove this

1. Export `linkedin.com` on macOS, import on Linux, and `chrome-agent auth linkedin.com` reports
   `signed_in: true` with the right name on the Linux box — the transfer ADR 0003 §4 says is
   impossible by profile copy.
2. The exported file is unreadable without the key.
3. Import reports rejected cookies rather than swallowing them, proven by importing a deliberately
   malformed jar.
4. `cookie export` with no `--domain` refuses.
5. A `control` grant cannot reach export — attempted over the WS plane, denied, logged.
