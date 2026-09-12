# threads.net — traps

What this site lies about. Each entry cost someone real time.

- threads.net IS NOT THE ORIGIN ANY MORE. Meta moved the app to threads.com in April 2025 and reversed the redirect: https://threads.net/ answers 301 -> https://www.threads.com/ (verified 2026-09-12). Cookies, localStorage and any probe are origin-scoped, so a probe pinned to threads.net is either measuring an empty origin or measuring whatever it landed on after a cross-origin redirect. This file keeps the .net name because that is what people still type; every URL in it points at threads.com.
- The CurrentUserInitialData blob is present on the SIGNED-OUT page, with ACCOUNT_ID and USER_ID as the string "0" and NAME as the empty string (verified on a signed-out www.threads.com document, 2026-09-12). Presence is not the verdict; USER_ID != "0" is. Note they are strings, not numbers.
- The session cookie (sessionid) is HttpOnly and invisible to document.cookie. ds_user_id is the readable half of the same session — the same split as instagram.com — so it can corroborate a verdict but can never carry one on its own, and it can outlive the session it names.
- The feed is GraphQL-hydrated after load (POST /api/graphql with a doc_id, fb_dtsg and an LSD token minted in the page). The HTML that arrives carries chrome and config, not posts, so a DOM read taken too early returns an empty feed that is indistinguishable from an account with nothing to show.
- Meta serves a logged-out-looking shell to clients it does not trust, and answers rate limiting with a normal-looking 200 page and no content rather than a 429. An empty result here means 'ask again later, with the real profile', not 'there is nothing'.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->

