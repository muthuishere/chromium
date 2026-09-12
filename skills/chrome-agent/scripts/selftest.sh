#!/usr/bin/env bash
# selftest — exercise every verb that cannot change anything, and say what broke.
#
# NON-DESTRUCTIVE BY CONSTRUCTION. It never posts, never likes, never logs out of a real site,
# never deletes a profile. The logout path is exercised against a SYNTHETIC site definition in a
# temp dir (example.com), which is why a temp $CHROME_AGENT_SITES exists at all — a real logout
# test would destroy the owner's session to prove a code path works.
#
#   bash scripts/selftest.sh            # everything that needs no browser + browser checks if up
#   bash scripts/selftest.sh --offline  # skip everything that needs a running browser
set -uo pipefail
CA="$(cd "$(dirname "$0")/.." && pwd)/chrome-agent"
OFFLINE=0; [ "${1:-}" = "--offline" ] && OFFLINE=1
PASS=0; FAIL=0; SKIP=0
ok(){   PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m %s\n' "$1"; }
bad(){  FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m %s — %s\n' "$1" "$2"; }
skip(){ SKIP=$((SKIP+1)); printf '  \033[33mSKIP\033[0m %s — %s\n' "$1" "$2"; }

# check <name> <expected-exit> <command...>
check(){ local name="$1" want="$2"; shift 2
  local out rc; out=$("$@" 2>/dev/null); rc=$?
  if [ "$rc" = "$want" ]; then ok "$name"; else bad "$name" "exit $rc, wanted $want"; fi
  printf '%s' "$out" >/dev/null
}
json(){ python3 -c 'import json,sys;json.load(sys.stdin)' >/dev/null 2>&1; }

echo "chrome-agent selftest"
echo "-- contract (no browser needed)"
check "exit-codes --json parses" 0 bash -c "$CA exit-codes --json | python3 -m json.tool"
check "usage exits 0"            0 bash -c "$CA >/dev/null"
check "bad verb is usage"        1 bash -c "$CA auth"
check "unknown site is usage"    1 bash -c "$CA auth nosuchsite.example"
check "recipes --json parses"    0 bash -c "$CA recipes --json | python3 -m json.tool >/dev/null"
check "sites validate"           0 bash -c "$CA sites validate >/dev/null"
check "sites list"               0 bash -c "$CA sites list >/dev/null"
check "profile resolves"         0 bash -c "$CA profile >/dev/null"
check "spool resolves"           0 bash -c "$CA spool >/dev/null"
check "ledger status parses"     0 bash -c "$CA ledger status | python3 -m json.tool >/dev/null"
check "promote reviews"          0 bash -c "$CA promote --notes-only >/dev/null"

echo "-- the fork guard"
check "missing fork exits 3"     3 bash -c "CHROME_AGENT_FORK=/nope $CA status"
check "missing fork is JSON"     0 bash -c "CHROME_AGENT_FORK=/nope $CA status 2>/dev/null | python3 -m json.tool >/dev/null"

echo "-- spool keying (both historic bugs)"
a=$(CHROME_AGENT_PROFILE="$HOME/chrome-agent-profile"  "$CA" spool)
b=$(CHROME_AGENT_PROFILE="$HOME/chrome-agent-profile/" "$CA" spool)
[ "$a" = "$b" ] && ok "trailing slash keeps one spool" || bad "trailing slash" "$a != $b"
c=$(CHROME_AGENT_PROFILE="$HOME/work/profile"     "$CA" spool)
d=$(CHROME_AGENT_PROFILE="$HOME/personal/profile" "$CA" spool)
[ "$c" != "$d" ] && ok "basename collision stays two spools" || bad "basename collision" "$c == $d"

echo "-- profile lifecycle (staged, nothing destroyed)"
T="$(mktemp -d)"
check "profile create"           0 bash -c "$CA profile create $T/p >/dev/null"
out=$("$CA" profile delete "$T/p" 2>/dev/null)
printf '%s' "$out" | grep -q '"staged":true' && ok "profile delete stages without --yes" || bad "profile delete staging" "no staged flag"
[ -d "$T/p" ] && ok "staged delete removed nothing" || bad "staged delete" "it deleted the profile"
check "profile delete --yes"     0 bash -c "$CA profile delete $T/p --yes >/dev/null"
[ ! -d "$T/p" ] && ok "confirmed delete removed it" || bad "confirmed delete" "still there"
check "default profile guarded"  1 bash -c "$CA profile delete $HOME/chrome-agent-profile --yes"

echo "-- site resolution order (ADR 0004)"
S="$(mktemp -d)"
cat > "$S/example.com.json" <<'JSON'
{"domain":"example.com","home":"https://example.com/","aliases":[],
 "login":{"url":"https://example.com/","note":"synthetic selftest fixture","twofa":false},
 "logout":{"method":"cookies","note":"synthetic"},
 "auth":{"probe_js":"return {signed_in:false, why:'synthetic selftest probe'};"},
 "read":{"verb":"eval","fixture_url":"https://example.com/"},"write":[],
 "traps":[],"status":"unverified","source":"hand","notes":"selftest fixture"}
JSON
check "override dir validates"   0 bash -c "CHROME_AGENT_SITES=$S $CA sites validate $S/example.com.json >/dev/null"
CHROME_AGENT_SITES="$S" "$CA" sites path example.com | grep -q "^$S" \
  && ok "override dir wins" || bad "override dir" "resolution order is wrong"

if [ "$OFFLINE" = 1 ]; then
  skip "browser checks" "--offline"
else
  echo "-- the engine (needs a browser: chrome-agent up)"
  if "$CA" status >/dev/null 2>&1; then
    check "doctor"                 0 bash -c "$CA doctor >/dev/null"
    check "status parses"          0 bash -c "$CA status >/dev/null"
    # example.com has no session by definition, so a signed-out verdict (exit 2) is the PASS.
    check "auth on the fixture"    2 bash -c "CHROME_AGENT_SITES=$S CHROME_AGENT_ID=selftest $CA auth example.com >/dev/null"
    check "logout on the fixture"  0 bash -c "CHROME_AGENT_SITES=$S CHROME_AGENT_ID=selftest $CA logout example.com >/dev/null"
    check "read the fixture"       0 bash -c "CHROME_AGENT_SITES=$S CHROME_AGENT_ID=selftest $CA read example.com >/dev/null"
  else
    skip "engine checks" "no browser is servicing the spool (chrome-agent up)"
  fi
fi
rm -rf "$T" "$S"

echo
printf 'pass %d  fail %d  skip %d\n' "$PASS" "$FAIL" "$SKIP"
[ "$FAIL" -eq 0 ]
