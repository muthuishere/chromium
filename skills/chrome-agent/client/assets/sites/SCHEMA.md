# site definition schema

One JSON file per domain, named `<domain>.json`. This is the **only** place a site's page-level
facts live: the login page, the signed-in probe, the logout route, which verb reads it, and the
traps. When a site re-skins, you edit one file — not the CLI, not a recipe, not a playbook.

`chrome-agent sites sync` installs these into `~/.config/chrome-agent/sites/`, which **wins** over
the shipped copy. So a page change can be fixed on a running server without touching the install,
and `sites sync --force` pulls the shipped version back when you want it.

```jsonc
{
  "domain": "example.com",              // REQUIRED, bare host, no scheme, no www
  "aliases": ["ex", "example"],         // what a human might type
  "home": "https://www.example.com/",   // where `login` and `auth` land by default

  "login": {
    "url": "https://www.example.com/login",
    "note": "what a human should expect — SSO? a QR? an emailed code?",
    "twofa": true                       // is a second factor likely? tells the operator to keep the share open
  },

  "logout": {
    "method": "url",                    // "url" | "dom" | "cookies"
    "url": "https://www.example.com/logout",
    "selector": "button[data-testid=logout]",  // when method = dom
    "note": "anything that bites"
  },

  "auth": {
    "probe_url": "https://www.example.com/",   // optional; defaults to home
    // A JS FUNCTION BODY. It may await. It MUST return {signed_in: bool, as?: string, why?: string}.
    // Ask the site's own API or its own config object. NEVER gate on a cookie you cannot read:
    // the session cookies that matter are usually HttpOnly, so document.cookie reports a live
    // session as logged out (this is the single most common bug in these files).
    "probe_js": "const r = await fetch('/api/me', {credentials:'include'}); ..."
  },

  "read":  { "verb": "recipe:example:feed", "fixture_url": "https://www.example.com/explore" },
  "write": ["recipe:example:post"],      // verb keys, or [] when the site is read-only here

  "traps": ["What this site lies about. Each entry should have cost someone real time."],

  "status": "unverified",               // "verified" only after a human or `verify` ran it live
  "source": "hand" | "agent-research",
  "notes": "anything that does not fit above"
}
```

## Rules

- **No identity, ever.** A site is reachable as more than one person; which `browser:<label>` acts
  is apl's business. A default identity in here silently makes the multi-identity design
  single-identity.
- **No credentials, no password hints, no 2FA handling.** `login` opens the page; a human types.
- **`probe_js` runs in the page.** Keep it read-only: no clicks, no navigation, no writes. It must
  not throw — wrap `document.cookie` (it raises SecurityError on opaque origins) and every fetch.
- **`status: "unverified"` is the honest default.** A definition written from documentation is a
  hypothesis. `chrome-agent verify <domain>` and a live `auth` are what make it "verified".
