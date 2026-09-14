#!/usr/bin/env bash
# build-release.sh — build the chrome-agent engine from the CANONICAL committed args.
#
# The build recipe lives in the repo (build-config/agent-release.gn), not in whatever command a
# person typed, so a resync or a resume can never silently change what gets built — the fork.1
# codec regression (a /tmp heredoc that re-ran gn gen without the codec flag) cannot recur.
#
#   scripts/build-release.sh [out-dir]      # default out/Release
#
# It refuses out/Default: a running agent browser is served from there, and overwriting its
# libraries mid-session kills the owner's logged-in tabs.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${1:-out/Release}"
ARGS="$ROOT/build-config/agent-release.gn"

[ "$OUT" = "out/Default" ] && { echo "refusing out/Default — a running browser uses it; pick out/Release" >&2; exit 1; }
[ -f "$ARGS" ] || { echo "missing $ARGS" >&2; exit 1; }
command -v gn >/dev/null || { echo "gn not on PATH (add depot_tools)" >&2; exit 1; }

mkdir -p "$ROOT/$OUT"
cp "$ARGS" "$ROOT/$OUT/args.gn"
echo "== gn gen $OUT from build-config/agent-release.gn =="
( cd "$ROOT" && gn gen "$OUT" )
echo "== verifying the codec flag actually took =="
( cd "$ROOT" && gn args "$OUT" --list=proprietary_codecs --short 2>/dev/null | grep -q "proprietary_codecs = true" ) \
  && echo "  proprietary_codecs = true (confirmed by gn, not just the file)" \
  || { echo "  proprietary_codecs is NOT true after gn gen — aborting" >&2; exit 1; }
echo "== build (nice) =="
( cd "$ROOT" && nice -n 19 ionice -c3 autoninja -C "$OUT" chrome )
echo "== result =="
( cd "$ROOT" && file "$OUT/chrome" 2>/dev/null || file "$OUT/Chromium.app/Contents/MacOS/Chromium" 2>/dev/null )
