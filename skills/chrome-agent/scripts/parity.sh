#!/usr/bin/env bash
# parity — the Go client must agree with the bash CLI, verb for verb, until bash retires (ADR 0010).
#
# This is the other half of the port's safety net. selftest.sh proves the Go client obeys the
# CONTRACT; parity proves it gives the SAME ANSWERS as the implementation being replaced, on the
# same machine, against the same profile. A rewrite that passes one and fails the other has quietly
# changed behaviour.
#
#   bash scripts/parity.sh [path-to-go-binary]
set -uo pipefail
SKILL="$(cd "$(dirname "$0")/.." && pwd)"
BASH_CLI="$SKILL/chrome-agent"
GO_CLI="${1:-$SKILL/client/chrome-agent}"
[ -x "$GO_CLI" ] || { echo "no go binary at $GO_CLI — build it: (cd client && go build -o chrome-agent ./cmd/chrome-agent)"; exit 1; }

PASS=0; FAIL=0
same(){ # same <name> <cmd...>  — run the SAME argv through both, compare stdout
  local name="$1"; shift
  local a b
  a=$("$BASH_CLI" "$@" 2>/dev/null); b=$("$GO_CLI" "$@" 2>/dev/null)
  if [ "$a" = "$b" ]; then PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m %s\n' "$name"
  else FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m %s\n    bash: %s\n    go:   %s\n' "$name" "${a:0:120}" "${b:0:120}"; fi
}
same_json(){ # compare as JSON, key order irrelevant
  local name="$1"; shift
  local a b
  a=$("$BASH_CLI" "$@" 2>/dev/null | python3 -c 'import json,sys;print(json.dumps(json.load(sys.stdin),sort_keys=True))' 2>/dev/null)
  b=$("$GO_CLI" "$@" 2>/dev/null | python3 -c 'import json,sys;print(json.dumps(json.load(sys.stdin),sort_keys=True))' 2>/dev/null)
  if [ -n "$a" ] && [ "$a" = "$b" ]; then PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m %s\n' "$name"
  else FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m %s\n    bash: %s\n    go:   %s\n' "$name" "${a:0:120}" "${b:0:120}"; fi
}
rc_same(){ # exit codes must match — apl maps them, so a difference is a behaviour change
  local name="$1"; shift
  "$BASH_CLI" "$@" >/dev/null 2>&1; local a=$?
  "$GO_CLI" "$@" >/dev/null 2>&1; local b=$?
  if [ "$a" = "$b" ]; then PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m %s (rc %d)\n' "$name" "$a"
  else FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m %s — bash rc %d, go rc %d\n' "$name" "$a" "$b"; fi
}

echo "parity: bash ($BASH_CLI) vs go ($GO_CLI)"
echo "-- slice 1: contract + paths"
same_json "exit-codes --json" exit-codes --json
same      "profile"           profile
same      "spool"             spool

echo "-- spool keying must be identical, both historic bugs"
for p in "$HOME/chrome-agent-profile" "$HOME/chrome-agent-profile/" "$HOME/work/profile" "$HOME/personal/profile"; do
  a=$(CHROME_AGENT_PROFILE="$p" "$BASH_CLI" spool)
  b=$(CHROME_AGENT_PROFILE="$p" "$GO_CLI"  spool)
  if [ "$a" = "$b" ]; then PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m spool for %s\n' "$p"
  else FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m spool for %s — bash %s, go %s\n' "$p" "$a" "$b"; fi
done

echo "-- exit codes on failure paths"
rc_same "no args is a friendly 0" 
rc_same "unknown verb is a usage failure" definitelyNotAVerb
( export CHROME_AGENT_FORK=/nope
  "$BASH_CLI" status >/dev/null 2>&1; a=$?
  "$GO_CLI"  status >/dev/null 2>&1; b=$?
  if [ "$a" = "$b" ] && [ "$a" = 3 ]; then printf '  \033[32mPASS\033[0m missing fork exits 3 in both\n'
  else printf '  \033[31mFAIL\033[0m missing fork: bash %d, go %d\n' "$a" "$b"; fi )

echo "-- slice 2: sites"
same      "sites list"        sites list
rc_same   "sites validate"    sites validate

echo "-- slice 2: identity, against a SYNTHETIC site (never a real session)"
S="$(mktemp -d)"
cat > "$S/example.com.json" <<'JSON'
{"domain":"example.com","home":"https://example.com/","aliases":[],
 "login":{"url":"https://example.com/","note":"synthetic parity fixture","twofa":false},
 "logout":{"method":"cookies","note":"synthetic"},
 "auth":{"probe_js":"return {signed_in:false, why:'synthetic parity probe'};"},
 "read":{"verb":"eval","fixture_url":"https://example.com/"},"write":[],
 "traps":[],"status":"unverified","source":"hand","notes":"parity fixture"}
JSON
if "$BASH_CLI" status >/dev/null 2>&1; then
  export CHROME_AGENT_SITES="$S" CHROME_AGENT_ID="parity-$$"
  rc_same "auth on the fixture (signed out)" auth example.com
  rc_same "logout on the fixture"            logout example.com
  # A live, REAL session: both must name the same identity. Read-only.
  a=$("$BASH_CLI" auth github.com 2>/dev/null | python3 -c 'import json,sys;d=json.load(sys.stdin);print(d.get("signed_in"),d.get("as"))' 2>/dev/null)
  b=$("$GO_CLI"  auth github.com 2>/dev/null | python3 -c 'import json,sys;d=json.load(sys.stdin);print(d.get("signed_in"),d.get("as"))' 2>/dev/null)
  if [ -n "$a" ] && [ "$a" = "$b" ]; then PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m auth github.com agrees (%s)\n' "$a"
  else FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m auth github.com — bash %s, go %s\n' "$a" "$b"; fi
  unset CHROME_AGENT_SITES CHROME_AGENT_ID
else
  printf '  \033[33mSKIP\033[0m identity checks — no browser is servicing the spool\n'
fi
rm -rf "$S"

echo
printf 'pass %d  fail %d\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
