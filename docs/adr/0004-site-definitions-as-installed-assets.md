# ADR 0004 — Site definitions are data, shipped with the skill and installed where they can be edited

- **Status:** **BUILT AND PROVEN, 2026-09-12.** 19 site definitions ship; `scripts/sites.py` owns
  the resolution order; `auth`/`login`/`logout`/`verify`/`read` all read them. All four proofs below
  were executed, not asserted: an override dir changes the verdict (the stub probe is what runs),
  `sites sync` leaves a locally edited file alone and names it, `--force` replaces it and the real
  probe returns, and 12 of the definitions were written by agents that never touched the CLI or the
  browser — 7 of those then ran live on the first try with no CLI change in between.
- **Date:** 2026-09-12
- **Owner:** Muthu (fork maintainer)
- **Author:** Claude Code (chrome-agent session)
- **Severity:** MEDIUM — no new capability; it decides where site knowledge lives, and therefore
  who can fix a broken site and how fast.
- **Related:** ADR 0003 (server), ADR 0005 (lifecycle), apl ADR-0009 (chrome-agent owns site
  knowledge).

---

## Context

Site knowledge is currently scattered across four places with four different lifecycles:

| What | Where it lives today | Who can change it |
|---|---|---|
| signed-in probe per site | a bash `case` inside the `chrome-agent` script | whoever edits the CLI |
| read/write verbs | the browser-research recipe registry, another repo | whoever edits that repo |
| which page a verb needs | a second bash `case` (`_ensure_origin`, `_verify_fixture`) | the CLI again |
| traps, login notes | generated markdown playbooks | the generator's Python tables |

Every one of these is **code**, and all of it changes for the same reason: a site re-skinned. When
LinkedIn moves a button, the fix is a one-line data change, and today it means editing a shell
script inside a 1.4 GB Chromium checkout — and then getting that edit onto a server.

This is also the difference between a skill that can be installed and one that can only be cloned.
A site definition is the smallest useful unit of "how this site behaves", and it should travel with
the skill, not with the fork.

## Decision

**One JSON file per domain — `sites/<domain>.json` — is the single source of page-level truth**, and
it is an *asset* of the skill, not part of its code.

It carries: `home`, `login` (url + what a human should expect + whether 2FA is likely), `logout`
(method/url/selector), `auth.probe_js` (a read-only JS body returning `{signed_in, as?}`),
`read.verb` + `read.fixture_url`, `write[]`, `traps[]`, and an honest `status` of
`verified | unverified`. Full shape and rules: `skills/chrome-agent/sites/SCHEMA.md`.

### Resolution order — user copy wins

```
$CHROME_AGENT_SITES            (explicit override, for tests and CI)
~/.config/chrome-agent/sites/  (INSTALLED copy — editable, survives reinstall)
<skill>/sites/                 (SHIPPED copy — the embedded asset)
```

`chrome-agent sites sync` installs shipped → installed. It **never silently overwrites** a file the
operator changed; `--force` does that, explicitly, and says which files it replaced.

That order is the whole point of the ADR. When a page changes at 2am on a server, the fix is
editing one JSON file in `~/.config`, not redeploying a skill. When the fix is right, it flows back
into the repo as a normal commit and `sites sync --force` retires the local patch.

### The CLI stops knowing about sites

`auth`, `login`, `logout`, `verify` and the origin/fixture rules all become *readers* of these
files. The CLI keeps only mechanism: how to run JS in a tab, how to navigate, how to decide a
verdict. That is what lets a site be added by writing a file — including by an agent that has never
touched the CLI.

### Unverified is a first-class state

A definition written from documentation is a **hypothesis**. It ships with `status: "unverified"`,
and the CLI must say so in its output rather than implying the same confidence as a site somebody
has actually driven. `verify` and a live `auth` are what promote it. This matters most for the
sites nobody has logged into yet, which will be most of them.

## Alternatives rejected

| Option | Why not |
|---|---|
| **Keep the bash `case` statements** | Editing a shell script to fix a CSS selector, on a server, inside a Chromium checkout. Also unparseable by anything that isn't bash. |
| **Put it all in the browser-research recipe registry** | Cross-repo. apl ADR-0009 already says chrome-agent owns site knowledge; the registry owns *how to call an API*, which is a different and smaller thing. |
| **A single big `sites.json`** | Every edit is a merge conflict; agents writing sites in parallel collide; you cannot ship one site without shipping all of them. |
| **JS/TS modules per site** | Executable config is a supply-chain hole the moment a site file is fetched or hand-edited on a server. JSON with one JS *string* that runs in the page is already the maximum sharp edge worth having. |
| **Generate definitions from the playbooks** | Backwards — the playbooks are the generated artifact. |

## Consequences

- A new site is a pull request containing one file. An agent can write it; a human reviews it.
- A broken site is a one-file hotfix, on the machine where it broke, with no deploy.
- Two copies of truth now exist (shipped vs installed) and they can diverge. `sites sync` must be
  able to *show* the diff, not just apply it, or this becomes its own drift problem.
- `probe_js` is a string of JavaScript inside a JSON file. It runs in the page. The schema's rules
  (read-only, no clicks, no navigation, must not throw) are load-bearing, and a reviewer who skips
  them is the vulnerability.
- Playbooks and site files overlap on traps. The playbook stays generated *from* the site file plus
  the verb list, so there is still exactly one place to write a trap.

## What would prove this

1. Deleting a domain's `case` branch from the CLI and adding its JSON leaves `auth <domain>`
   behaving identically — proven per domain, not asserted.
2. Editing `~/.config/chrome-agent/sites/<domain>.json` changes behaviour with no repo change and
   no restart.
3. `sites sync` leaves a locally edited file alone, reports it, and `--force` replaces it and says
   what it replaced.
4. A site definition written by an agent that never ran the browser can be `verify`'d green by a
   human later, with no CLI change in between. That is the test of whether the schema is really
   sufficient.
