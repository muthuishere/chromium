# bsky.app — traps

What this site lies about. Each entry cost someone real time.

- Signing out does NOT remove the account from BSKY_STORAGE — social-app leaves the entry in session.accounts[] with did and handle intact and only strips accessJwt/refreshJwt (both are optional in its zod schema, exactly so an expired session can persist). A probe that gates on 'a handle is in localStorage' reports a signed-out profile as live.
- The persisted blob is validated as ONE zod schema, and social-app's own schema.ts warns that a single failing field makes tryParse discard the ENTIRE persisted state — every account and every preference — so the app boots logged out with defaults. A version skew can therefore turn a working session into 'never signed in' with no error anywhere.
- BSKY_STORAGE is written asynchronously and fanned out across tabs over a BroadcastChannel ('BSKY_BROADCAST_CHANNEL'); the project's own issue reports the write landing 15-20s later in a throttled/background tab. Probing immediately after a hand login or logout can read the PREVIOUS state. Wait, or re-probe.
- No cookie on bsky.app means anything. The app is a pure client: reads and writes are XRPC calls to the account's PDS / the AppView carrying a Bearer token from localStorage. Cookie-shaped reasoning (and any credentialed same-origin fetch) is noise here.
- bsky.app is one client of many for the same account. A session here is not a session on the PDS host (bsky.social) or on any other atproto client, and logging out here does not revoke anything server-side.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->

