# Wiring `internal/recipes` into the dispatcher

This package is finished and tested but nothing calls it yet — `cmd/chrome-agent/main.go` is owned
by another session. Here is exactly what to call.

Import: `github.com/deemwarhq/chrome-agent/internal/recipes`

## `chrome-agent recipes [--json]`

```go
list, err := recipes.List()          // []recipes.Recipe, sorted by Key
```

`err` means no registry was resolvable at all — the Ubuntu failure mode. For `--json`, marshal
`list` directly: the struct tags are already byte-identical to `node recipes-list.mjs --json`
(48 entries, 20 writes, pinned by `TestListMatchesTheNodeBaseline`). For the human list, print
`"  <key> — <describe>"`, appending `"  [chrome-agent verb]"` when `r.Source == "chrome-agent"`.

## `chrome-agent recipes vendor [--force]`

```go
res, err := recipes.Sync(force, dryRun)   // *recipes.SyncResult
```

Writes the embedded registry into `~/.config/chrome-agent/recipes`, and **keeps** a file an operator
edited unless `--force`. This replaces the bash `recipes_vendor`, which needed a browser-research
checkout to exist; the binary now carries the source. `install` should call `recipes.Sync(false,
false)` next to `sites.Sync`.

## `chrome-agent recipe <site:name> [opts-json] [--csp|--plain]`

```go
b := browser.New()
res, err := recipes.Run(b, key, optsJSON, recipes.Options{})
```

- `Run` calls `EnsureOrigin` itself. Do not navigate first.
- `Options{}` = the CSP-safe path, 20s. `Options{Plain: true}` reproduces the bash default
  (`eval(atob(...))`) — offer it as `--plain`, do not make it the default; on a strict-CSP page it
  fails **silently**, no error and no result. There is no `--csp` flag to add: CSP-safe is now the
  default, so the bash third argument becomes a no-op you can accept and ignore.
- `res` is the decoded recipe result (usually `map[string]any`); marshal it straight to stdout.
- Exit codes: a resolution failure (`no recipe "x"`) is `exit.Usage`; a browser/eval failure is
  `exit.Browser`; a result carrying its own `error` key is the site refusing — `exit.Site`.
- **Writes are still staged by the recipe itself** (`opts.confirm`). `Run` does not add a
  confirmation gate and must not be given one here; the recipe owns that contract.

## `chrome-agent read <domain> [generic-recipe]`

```go
out, err := recipes.Read(b, domain, genericKey)   // genericKey "" => generic:page-text
```

`out` marshals to the bash shape exactly: `{domain, read_with, page, definition, generic, result}`
plus `note` when `generic` is true. Check `b.Alive()` first and die `exit.Browser` with
`"no browser is servicing <spool> — run: chrome-agent up"`, as bash does.

## `chrome-agent verify <domain>`

```go
out, err := recipes.Verify(b, domain, func(d string) error {
        v, err := identity.Auth(b, d)
        if err != nil { return err }
        if !v.SignedIn { return fmt.Errorf("%s", v.Why) }
        return nil
})
```

The `probe` callback is how a domain with **no read recipe** still gets verified; pass the
`identity.Auth` closure above — this package deliberately does not import `identity`, to keep the
dependency one-way. Exit `exit.Site` (4) when `out.Verified` is false, `exit.OK` otherwise.

**Not ported deliberately:** bash's `verify` also rewrites `last_verified:` in
`skills/chrome-agent/playbooks/<domain>/meta.md`. That path is outside `client/` and there is no
`paths.Playbooks()` yet. `VerifyResult.LastVerified` carries the stamp; the dispatcher (or a later
`paths` addition) should write it.

## Also exported, if a verb wants it

- `recipes.Resolve(key) (*Recipe, error)` — metadata + `Fn` source without running anything.
- `recipes.Payload(r, optsJSON) string` — the exact JS `recipe-run.mjs` used to print. Useful for a
  `recipe --dry-run` that shows what would be evaluated.
- `recipes.PlanFor(domain) Plan` — what `read`/`verify` would run, and on what page.
- `recipes.EnsureOrigin(b, key)` — if a caller runs a payload by hand.

## One asymmetry inherited on purpose

`List()` reports the registry's built-ins plus chrome-agent's own verbs, and **not** the per-machine
drop-ins in `~/.config/chrome-agent/recipes/user/`. That is what bash did — `recipes-list.mjs` read
`RECIPES` while `recipe-run.mjs` resolved through `getRecipes()` — so a user recipe has always been
invisible to `recipes` and runnable by `recipe <key>`. `Resolve`/`Run` keep resolving them. If that
should change, change it in `List()`, not in `Resolve()`.

## The four chrome-agent verbs that are listed but not runnable here

`linkedin:like`, `x:like`, `x:repost`, `reddit:upvote` are DOM write actions that still live in the
bash CLI. They appear in `List()` (dropping them would tell a generator that reacting is impossible
— that regression already shipped once) and `Run` refuses them with an error naming the bash verb.
`hackernews:top` and `hackernews:item` ARE implemented here. When the like/upvote verbs are ported,
fill in `builtins[].js` and `Run` starts serving them with no other change.
