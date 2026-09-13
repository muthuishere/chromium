# WIRING — `internal/learned`

| CLI | call |
|---|---|
| `note <domain> "<text>"` | `learned.Note(domain, text)` → `*NoteResult` |
| `promote [<domain>] [--apply] [--include-drift]` | `learned.Promote(learned.Opts{Domain, Apply, IncludeDrift})` → `*Report` |
| (a review of one source) | `learned.Pending(domain, learned.SourceNote\|SourceDrift\|SourceAll)` |

- `Note` returns a plain error on bad input — map it to `exit.Usage` with slug `usage`.
- `Promote` is a report, never an error, for per-domain failures: a missing `traps.md` lands in
  `Report.Domains[d].Error`. Exit 0 and print it.
- **Default behaviour: drift is listed, not written.** `IncludeDrift` is the opt-in. Wire
  `--include-drift`; the bash flags were `--source=` / `--notes-only`, and its DEFAULT promoted drift
  into canon (see the report on promote.py).
- Playbooks are resolved by `CHROME_AGENT_PLAYBOOKS`, else `CHROME_AGENT_SKILL/playbooks`, else
  `paths.Fork()/skills/chrome-agent/playbooks`. If the dispatcher learns the skill dir some other
  way, set `CHROME_AGENT_SKILL` rather than adding a fourth rule here.
