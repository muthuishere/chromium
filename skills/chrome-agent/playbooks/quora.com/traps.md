# quora.com — traps

What this site lies about. Each entry cost someone real time.

- A signed-OUT question page is full of a[href^="/profile/"] links — every answer author has one. An unscoped profile-link selector therefore reports every logged-out question page as signed in. The probe only accepts such a link inside the header/nav shell, and this is the single easiest way to get this file wrong.
- Quora shows a partial answer and a 'continue reading' gate to signed-out visitors. Content coming back is not evidence of a session; truncated content is evidence of the opposite.
- The session cookies are HttpOnly; the readable ones are set for anonymous visitors too, so no cookie here separates the two states.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->

