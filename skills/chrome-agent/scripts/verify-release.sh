#!/usr/bin/env bash
# verify-release.sh — run ADR 0007's five gates against an engine artifact, on any machine.
#
#   bash scripts/verify-release.sh dist/chrome-agent-engine-…-darwin-arm64.tar.gz
#   bash scripts/verify-release.sh dist/chrome-agent-engine-…-linux-x64           # a staged dir
#   bash scripts/verify-release.sh <artifact> --headful      # macOS headful instead of --headless=new
#   bash scripts/verify-release.sh <artifact> --keep         # leave the scratch dir for inspection
#
# THE RULE THIS SCRIPT EXISTS TO ENFORCE: there is no SKIP. A gate is PASS or it is FAIL. A gate this
# script cannot run — no client, no network, no binary, an unparseable answer — is a FAIL with a
# named reason, and the script exits non-zero. ADR 0007's failure mode is silent and
# machine-specific; a green run that quietly skipped the one gate that mattered is how it ships.
#
# The five gates (ADR 0007 §3):
#   1 relocation      unpack somewhere else entirely, launch it there, get a DOM back
#   2 undetectability navigator.webdriver === false, through the spool
#   3 spool protocol  eval, evalAsync, the tabId registry and the screenshot ack all answer
#   4 doctor          `chrome-agent doctor` reports ready:true against the UNPACKED artifact
#   5 a real read     news.ycombinator.com returns items, end to end
#
# WHAT IT NEVER TOUCHES: the owner's browser. Every run gets a throwaway profile in a scratch dir,
# and the spool is keyed off the profile path, so this cannot steer, restart, or even see the live
# session another agent is driving.
#
# No node and no python3 are used by any gate. The artifact is launched directly and every question
# is asked through the Go client (client/chrome-agent), built here if it is missing.
set -uo pipefail

SELF="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SKILL="$(cd "$SELF/.." && pwd)"
CLIENT="$SKILL/client/chrome-agent"

ART=""; KEEP=0; HEADLESS=1
while [ $# -gt 0 ]; do
  case "$1" in
    --keep)    KEEP=1; shift ;;
    --headful) HEADLESS=0; shift ;;
    -h|--help) sed -n '2,30p' "${BASH_SOURCE[0]}"; exit 0 ;;
    -*)        echo "unknown flag $1" >&2; exit 1 ;;
    *)         ART="$1"; shift ;;
  esac
done
[ -n "$ART" ] || { echo "usage: verify-release.sh <artifact.tar.gz | staged-dir> [--headful] [--keep]" >&2; exit 1; }

PASS=0; FAIL=0
declare -a RESULTS=()
gate_pass(){ PASS=$((PASS+1)); RESULTS+=("PASS|$1|$2"); printf '  \033[32mPASS\033[0m %-16s %s\n' "$1" "$2"; }
gate_fail(){ FAIL=$((FAIL+1)); RESULTS+=("FAIL|$1|$2"); printf '  \033[31mFAIL\033[0m %-16s %s\n' "$1" "$2"; }
say(){ printf '\n\033[36m==\033[0m %s\n' "$*"; }

# --- the scratch dir: SOMEWHERE ELSE ENTIRELY -----------------------------------------------------
# Gate 1 is meaningless if the artifact is exercised next to its build tree, because that is the
# exact path an @rpath stub resolves through. ${TMPDIR}/mktemp is on another part of the filesystem
# and has no relationship to the fork checkout.
SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/chrome-agent-verify.XXXXXX")"
BROWSER_PID=""
# shellcheck disable=SC2329  # invoked by the EXIT trap below
cleanup(){
  if [ -n "$BROWSER_PID" ]; then kill "$BROWSER_PID" 2>/dev/null || true; wait "$BROWSER_PID" 2>/dev/null || true; fi
  if [ "$KEEP" = 1 ]; then echo "scratch kept: $SCRATCH"; else rm -rf "$SCRATCH"; fi
}
trap cleanup EXIT

echo "verify-release — ADR 0007's five gates"
echo "  artifact : $ART"
echo "  scratch  : $SCRATCH"

# --- the client -----------------------------------------------------------------------------------
if [ ! -x "$CLIENT" ]; then
  say "building the Go client (it is missing)"
  if command -v go >/dev/null 2>&1; then
    ( cd "$SKILL/client" && go build -o chrome-agent ./cmd/chrome-agent ) || true
  fi
fi
if [ ! -x "$CLIENT" ]; then
  gate_fail relocation      "no client at $CLIENT, and go could not build one"
  gate_fail webdriver       "no client"
  gate_fail spool_protocol  "no client"
  gate_fail doctor          "no client"
  gate_fail read            "no client"
  echo; echo "5 gates could not run. That is a FAIL, not a skip."; exit 1
fi

# --- unpack ---------------------------------------------------------------------------------------
say "gate 1/5 — relocation"
UNPACK="$SCRATCH/unpacked"
mkdir -p "$UNPACK"
ROOT=""
if [ -d "$ART" ]; then
  # A staged dir still has to be COPIED out of where it was staged; verifying it in place proves
  # nothing about an artifact someone unpacks in ~/Downloads.
  if command -v ditto >/dev/null 2>&1; then ditto "$ART" "$UNPACK/$(basename "$ART")"
  else cp -a "$ART" "$UNPACK/$(basename "$ART")"; fi
  ROOT="$UNPACK/$(basename "$ART")"
elif [ -f "$ART" ]; then
  case "$ART" in
    *.tar.gz|*.tgz) tar xzf "$ART" -C "$UNPACK" ;;
    *.zip)          unzip -q "$ART" -d "$UNPACK" ;;
    *)              gate_fail relocation "not an archive this script knows: $ART"; ROOT="" ;;
  esac
  [ -z "${ROOT:-}" ] && ROOT="$(find "$UNPACK" -maxdepth 1 -mindepth 1 -type d | head -1)"
else
  gate_fail relocation "no such artifact: $ART"
fi

BIN=""
OSN="$(uname -s | tr '[:upper:]' '[:lower:]')"
if [ -n "$ROOT" ] && [ -d "$ROOT" ]; then
  if [ -x "$ROOT/Chromium.app/Contents/MacOS/Chromium" ]; then
    BIN="$ROOT/Chromium.app/Contents/MacOS/Chromium"
  elif [ -x "$ROOT/chrome" ]; then
    BIN="$ROOT/chrome"
  elif [ -d "$ROOT/Chromium.app" ] || [ -x "$UNPACK/Chromium.app/Contents/MacOS/Chromium" ]; then
    BIN="$UNPACK/Chromium.app/Contents/MacOS/Chromium"
    ROOT="$UNPACK"
  fi
fi

if [ -z "$BIN" ] || [ ! -x "$BIN" ]; then
  gate_fail relocation "no launchable binary inside the unpacked artifact (looked for Chromium.app/Contents/MacOS/Chromium and ./chrome)"
else
  # The cheapest guard against the single most likely mistake: shipping the wrong OS's binary.
  FILEOUT="$(file -b "$BIN" 2>/dev/null || echo unknown)"
  FMT_OK=0
  case "$OSN" in
    darwin) case "$FILEOUT" in *Mach-O*) FMT_OK=1 ;; esac ;;
    linux)  case "$FILEOUT" in *ELF*)    FMT_OK=1 ;; esac ;;
    *)      FMT_OK=1 ;;
  esac
  if [ "$FMT_OK" = 0 ]; then
    gate_fail relocation "binary format is wrong for this OS: $FILEOUT"
    BIN=""
  fi
fi

# --- launch it, from the scratch dir, with a throwaway profile -------------------------------------
PROFILE="$SCRATCH/profile"
LOG="$SCRATCH/browser.log"
export CHROME_AGENT_FORK="$ROOT"          # the artifact stages chromesendkeys.cjs + the launcher
export CHROMIUM_SENDKEYS_OUT="$ROOT"      # ...and the binary lives at the artifact root
export CHROME_AGENT_PROFILE="$PROFILE"

SPOOL=""
if [ -n "$BIN" ]; then
  mkdir -p "$PROFILE"
  SPOOL="$("$CLIENT" spool 2>/dev/null || true)"
  if [ -z "$SPOOL" ]; then
    gate_fail relocation "the client could not resolve a spool for $PROFILE"
    BIN=""
  else
    mkdir -p "$SPOOL"
  fi
fi

if [ -n "$BIN" ]; then
  set -- --user-data-dir="$PROFILE" --no-first-run --no-default-browser-check \
         --disable-session-crashed-bubble --no-sandbox
  [ "$HEADLESS" = 1 ] && set -- "$@" --headless=new
  CHROMIUM_SENDKEYS_DIR="$SPOOL" "$BIN" "$@" about:blank >"$LOG" 2>&1 &
  BROWSER_PID=$!
  # Drop it from the jobs table: a relocation failure kills the browser instantly and bash would
  # otherwise print its own "Abort trap: 6" line on top of the gate's own, much clearer, message.
  disown "$BROWSER_PID" 2>/dev/null || true

  # A relocation failure (the dyld @rpath one) surfaces here, within a second, as a dead pid.
  ALIVE=0
  for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do
    if ! kill -0 "$BROWSER_PID" 2>/dev/null; then break; fi
    if "$CLIENT" eval 'return document.readyState' 6 >/dev/null 2>&1; then ALIVE=1; break; fi
    sleep 1
  done

  if [ "$ALIVE" = 1 ]; then
    gate_pass relocation "launched from $SCRATCH, DOM answered ($FILEOUT)"
  else
    WHY="$(grep -m1 -Ei 'library not loaded|dyld|error while loading|no such file' "$LOG" 2>/dev/null | tr '\n' ' ' | cut -c1-180)"
    [ -n "$WHY" ] && WHY="$WHY … (re-run with --keep for the full log)"
    [ -n "$WHY" ] || WHY="the browser never serviced the spool — see $LOG"
    gate_fail relocation "$WHY"
    BROWSER_PID=""
  fi
fi

RUNNING=0
[ -n "$BROWSER_PID" ] && kill -0 "$BROWSER_PID" 2>/dev/null && RUNNING=1

# --- gate 2: undetectability ----------------------------------------------------------------------
say "gate 2/5 — navigator.webdriver === false, through the spool"
if [ "$RUNNING" = 0 ]; then
  gate_fail webdriver "the artifact never came up — the gate could not be run (that is a FAIL)"
else
  ST="$("$CLIENT" status 2>&1 || true)"
  case "$ST" in
    *'"webdriver": false'*|*'"webdriver":false'*) gate_pass webdriver "navigator.webdriver === false" ;;
    *'"webdriver": true'*|*'"webdriver":true'*)
      gate_fail webdriver "navigator.webdriver is TRUE — this build lost the patch. It is not a slower product, it is a different one (ADR 0007 §3.2)." ;;
    *) gate_fail webdriver "no parseable webdriver answer: $(printf '%s' "$ST" | tr '\n' ' ' | cut -c1-160)" ;;
  esac
fi

# --- gate 3: the spool protocol -------------------------------------------------------------------
# ADR 0007 names chrome-agent-selftest.cjs here. That needs node, which a target box may not have,
# and ADR 0008's linux artifact passed this gate on doctor's engine probe instead — the same four
# assertions (eval, evalAsync, the tabId registry, the screenshot ack), each under its own timeout.
# That is what runs here. If node AND the selftest are both present it runs as well, and a failure
# there fails the gate too.
say "gate 3/5 — the spool protocol"
DOC=""
if [ "$RUNNING" = 0 ]; then
  gate_fail spool_protocol "the artifact never came up — the gate could not be run (that is a FAIL)"
else
  DOC="$("$CLIENT" doctor 2>&1 || true)"
  MISSING=""
  for cap in eval evalasync tabid screenshot_ack; do
    case "$DOC" in
      *"\"$cap\": true"*|*"\"$cap\":true"*) : ;;
      *) MISSING="$MISSING $cap" ;;
    esac
  done
  if [ -z "$MISSING" ]; then
    EXTRA=""
    SELFTEST="$ROOT/chrome-agent-selftest.cjs"
    if [ -f "$SELFTEST" ] && command -v node >/dev/null 2>&1; then
      if CHROMIUM_SENDKEYS_DIR="$SPOOL" node "$SELFTEST" >"$SCRATCH/selftest.log" 2>&1; then
        EXTRA=" + chrome-agent-selftest.cjs"
      else
        gate_fail spool_protocol "doctor's probe was green but chrome-agent-selftest.cjs failed — see $SCRATCH/selftest.log"
        MISSING="selftest"
      fi
    fi
    [ -z "$MISSING" ] && gate_pass spool_protocol "eval, evalAsync, tabId registry, screenshot ack all answered$EXTRA"
  else
    gate_fail spool_protocol "engine capabilities absent:$MISSING"
  fi
fi

# --- gate 4: doctor against the UNPACKED artifact --------------------------------------------------
say "gate 4/5 — chrome-agent doctor, against the unpacked artifact"
if [ "$RUNNING" = 0 ]; then
  gate_fail doctor "the artifact never came up — the gate could not be run (that is a FAIL)"
elif [ -z "$DOC" ]; then
  gate_fail doctor "doctor produced no output"
else
  case "$DOC" in
    *'"ready": true'*|*'"ready":true'*)
      gate_pass doctor "ready: true, with CHROME_AGENT_FORK=$ROOT and no repo checkout in play" ;;
    *)
      WANT="$(printf '%s' "$DOC" | tr -d '\n' | sed -n 's/.*"missing"[^[]*\[\([^]]*\)\].*/\1/p')"
      gate_fail doctor "ready is not true — missing: ${WANT:-unknown}" ;;
  esac
fi

# --- gate 5: a real read, end to end ---------------------------------------------------------------
say "gate 5/5 — a real read (news.ycombinator.com)"
if [ "$RUNNING" = 0 ]; then
  gate_fail read "the artifact never came up — the gate could not be run (that is a FAIL)"
else
  if ! "$CLIENT" goto https://news.ycombinator.com 6 >/dev/null 2>&1; then
    gate_fail read "goto failed — no network, or the engine refused to navigate"
  else
    ITEMS="$("$CLIENT" evalcsp \
      'return Array.from(document.querySelectorAll(".titleline > a")).map(a => a.textContent).filter(Boolean)' 25 2>&1 || true)"
    N="$(printf '%s\n' "$ITEMS" | grep -c '^  "' || true)"
    if [ "${N:-0}" -ge 10 ]; then
      gate_pass read "$N Hacker News items with titles, end to end through the artifact"
    else
      gate_fail read "only ${N:-0} items came back (wanted >= 10) — a site change, or the stack is broken. Say which in the release note (ADR 0007 §3)."
    fi
  fi
fi

# --- verdict ----------------------------------------------------------------------------------------
say "verdict"
printf '  %d passed, %d failed\n' "$PASS" "$FAIL"
if [ -n "${ART:-}" ]; then
  {
    echo "{"
    echo "  \"artifact\": \"$(basename "$ART")\","
    echo "  \"verified_on\": \"$(uname -s) $(uname -r) / $(uname -m)\","
    echo "  \"verified_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\","
    echo "  \"gates\": {"
    i=0
    for r in "${RESULTS[@]}"; do
      st="${r%%|*}"; rest="${r#*|}"; nm="${rest%%|*}"; why="${rest#*|}"
      i=$((i+1)); comma=","; [ "$i" = "${#RESULTS[@]}" ] && comma=""
      printf '    "%s": "%s — %s"%s\n' "$nm" "$(printf '%s' "$st" | tr '[:upper:]' '[:lower:]')" \
        "$(printf '%s' "$why" | sed 's/"/\\"/g')" "$comma"
    done
    echo "  }"
    echo "}"
  } > "${VERIFY_JSON:-$SCRATCH/verify.json}"
  echo "  report: ${VERIFY_JSON:-$SCRATCH/verify.json}"
fi

if [ "$FAIL" -gt 0 ]; then
  echo
  echo "  NOT A RELEASE. A build that fails any gate is not published (ADR 0007 §3)."
  exit 1
fi
echo
echo "  All five gates passed. Copy the gate lines into the artifact's manifest.json."
exit 0
