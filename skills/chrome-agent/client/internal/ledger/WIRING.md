# WIRING — `internal/ledger`

| CLI | call |
|---|---|
| (every write verb, after it acts) | `ledger.Append(agent, action, target, result)` |
| `ledger status` | `ledger.StatusOf()` |
| `ledger rotate` | `ledger.Rotate("manual")`, then `ledger.StatusOf()` |
| `ledger tail [n]` (new, optional) | `ledger.Tail(n)` |

- `agent` is the session key that owns the tab (bash used `_tab_id`); pass whatever the tab registry
  resolves to, `""` is acceptable but makes the line less auditable. `profile` and `ts` are filled in
  for you — do not pass them.
- `Append` rotates on size by itself. No caller should ever call `RotateIfBig`.
- Env: `CHROME_AGENT_LEDGER` (path — set it in any test), `CHROME_AGENT_LEDGER_MAX` (default 8 MB),
  `CHROME_AGENT_LEDGER_KEEP` (default 5).
- An `Append` error must NOT fail the verb that just succeeded. Log it to stderr; a missing audit
  line is bad, a caller that retries a completed `--confirm` write because logging failed is worse.
