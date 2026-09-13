# WIRING — `internal/profile`

Dispatcher calls (ADR 0005, `chrome-agent profile …`). Nothing here touches the browser.

| CLI | call | prints |
|---|---|---|
| `profile create <dir>` | `profile.Create(dir)` | `*CreateResult` |
| `profile list` | `profile.List()` | `*ListResult` (no error path) |
| `profile delete <dir> [--yes] [--force]` | `profile.Delete(dir, profile.DeleteOpts{Yes, Force})` | `*DeleteResult` |

- Every returned error is `*profile.Error{Code, Slug, Detail}` — pass it straight to
  `exit.Die(e.Code, e.Slug, e.Detail)`. All refusals use `exit.Usage` (1), matching the bash CLI.
- `profile.JSON(v)` is the encoder these results are shaped for (indented, stable).
- **`--force` does NOT imply `--yes`.** Force only permits the DEFAULT profile as a target; the
  staging step still applies. The bash CLI accepted `--force` as consent to destroy — do not
  re-introduce that when wiring the flags.
- A staged delete (`Yes:false`) returns `Staged:true` and a **non-error** — exit 0. Print it and stop.
- Also exported, for `doctor` / `up` / `instances` if they want it: `LockState(dir)`,
  `Running(dir)`, `SignedInDomains(dir)`, `Candidates()`.
