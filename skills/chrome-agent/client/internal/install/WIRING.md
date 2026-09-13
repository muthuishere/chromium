# WIRING — `internal/install`

| CLI | call |
|---|---|
| `install [<bindir>]` | `install.Install(bindir)` — empty bindir means `~/.local/bin` |

- Returns `(*Result, error)`; the error is only for "could not create the config tree / sync failed".
  Everything survivable (a non-symlink already on PATH, a bindir not on `$PATH`, kept local site
  edits) comes back in `Result.Warnings` with exit 0.
- `Install` uses `os.Executable()` for the symlink target, so the dispatcher passes nothing.
- **Deliberately NOT called: recipes.** Bash's `install_` ran `recipes_vendor`, because without the
  browser-research recipes a fresh box has 6 verbs instead of 48. `internal/recipes` (landed in
  parallel with this package) now exposes `Sync(force, dry)` with the same shape as `sites.Sync`, so
  the port is one call plus a `recipes`/`recipes_kept` field on `Result` — left out here only
  because that package was being edited while this one was written, and install's mandate was
  dirs + sites + symlink. Add it when recipes settles; the warning bash printed when vendoring
  failed is worth keeping.
