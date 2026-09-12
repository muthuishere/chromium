# discord.com — traps

What this site lies about. Each entry cost someone real time.

- https://discord.com/ is the MARKETING site and renders a 'Login' button whether or not a session exists. Probing the bare root reports every live session as logged out. Probe /channels/@me — that is why probe_url overrides home's usual role here.
- Discord's REST API authenticates on an Authorization header, NOT on cookies. A credentialed same-origin fetch of /api/v9/users/@me answers 401 on a perfectly live session — a false negative by construction. Do not add that fetch to the probe.
- The web client is a slow-booting SPA. Probing within a second or two of navigation finds neither the user area nor the server rail and looks exactly like signed-out. The probe says so in its 'why' rather than pretending certainty; re-probe after a few seconds.
- Class names in the DOM are hashed and change with client builds, so the selectors here are substring matches and WILL rot. When this probe goes wrong it goes wrong by reporting logged-out — which is the safe direction, but it is still a page change, not an auth change.
- **One shared browser, no mutex.** Two lanes attach to a nondeterministic tab.
  `chrome-agent goto <url>` then `chrome-agent status` before believing any read.

**Verification rule:** read the artifact back from the live page. A 2xx proves nothing
here, and on some of these sites neither does a 5xx.

<!-- keep: hand-written below — the generator never touches this -->

