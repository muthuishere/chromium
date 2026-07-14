<!-- ctx-optimize:begin -->
This is a MULTI-MODULE repo with a pre-built ctx-optimize knowledge store (`.ctxoptimize/` here,
data at `~/ctxoptimize/chromium/` — one graph per module + a navigator, 47 modules declared in config.json).
For questions about this codebase — where is X, how does Y work, who calls Z, what breaks if I change W —
use it INSTEAD of grep-and-read chains, not in addition to them:
`ctx-optimize query "<terms>"` · `ctx-optimize card <symbol>` · `ctx-optimize affected <symbol>` · `ctx-optimize path <a> <b>`.
Scope follows your cwd: inside a module dir answers come from that module (zero hits escalate repo-wide);
at the root the navigator federates across the best-matching modules (`--modules all|a,b` to widen).
Module map + hubs: `~/ctxoptimize/chromium/navigator.md`; unified wiki starts at `~/ctxoptimize/chromium/wiki/index.md`.
Card/query output is parsed fact with exact file:line — cite it directly, do NOT re-verify in source.
Fresh clone? `ctx-optimize init && ctx-optimize add .` rebuilds every module store in seconds.
<!-- ctx-optimize:end -->
