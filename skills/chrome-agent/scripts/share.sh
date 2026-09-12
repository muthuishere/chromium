#!/usr/bin/env bash
# share.sh — chrome-agent's time-boxed remote login window (ADR 0003 §3).
#
# This is NOT a remote-control feature. It is a login window with a lock on it:
# you hand someone a key, you watch them use it, and it is taken back whether or
# not anyone remembers to take it back. Every rule below is load-bearing and
# comes from the 2026-07-06 incident (a stale, unauthenticated browser endpoint
# left on a public tunnel for days, as root).
#
#   start [--ttl 15m] [--port N]   bring the stack up, print ONE json line
#   status                          json: running?, url, seconds left, listeners
#   stop                            tear down; idempotent; always exit 0
#   reconcile                       watchdog/boot: expired -> tear down
#   selftest [--ttl 60s]            macOS/CI: tunnel half only, dummy http.server
#
# Invariants:
#   * Nothing ever binds 0.0.0.0. Asserted after start; a public listener is a
#     hard failure that tears the whole stack down (fail closed).
#   * The TTL is enforced by a DETACHED timer, not by the caller remembering.
#     Killing the shell that ran `start` does not extend the share by a second.
#   * State is a file, so `stop` works from another shell, another ssh session,
#     a phone.
#   * The token is printed exactly once, in the URL. It is never logged, never
#     echoed, never put in the ledger, and `status` prints the URL without it.
set -euo pipefail

STATE_DIR="${CHROME_AGENT_SHARE_DIR:-$HOME/.config/chrome-agent/share}"
STATE="$STATE_DIR/state.json"
TOKEN_FILE="$STATE_DIR/token"          # 0600, VNC passwd material; never printed
VNCPASS_FILE="$STATE_DIR/vncpasswd"    # 0600, x11vnc -rfbauth file
LOG_DIR="$STATE_DIR/logs"
LEDGER="${CHROME_AGENT_LEDGER:-$HOME/.config/chrome-agent/actions.ndjson}"
PROFILE="${CHROME_AGENT_PROFILE:-$HOME/chrome-agent-profile}"; PROFILE="${PROFILE%/}"
SELF="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
CLOUDFLARED="${CLOUDFLARED_BIN:-$(command -v cloudflared || echo /opt/homebrew/bin/cloudflared)}"
OS="$(uname -s)"

mkdir -p "$STATE_DIR" "$LOG_DIR" "$(dirname "$LEDGER")"
chmod 700 "$STATE_DIR" 2>/dev/null || true

die(){ echo "share: $*" >&2; exit 1; }

# ---- ledger -------------------------------------------------------------------------------
# Same shape as the `log()` in ../chrome-agent: ts, profile, agent, action, target, result.
# `target` carries the ttl, per the ADR ("every start and stop is logged with the profile and
# the reason"). NEVER pass token material through here.
_agent_id(){ echo "${CHROME_AGENT_ID:-${DEEMWAR_TAB_ID:-share}}"; }
log(){ printf '{"ts":"%s","profile":"%s","agent":"%s","action":"%s","target":"%s","result":%s}\n' \
  "$(date -u +%FT%TZ)" "$PROFILE" "$(_agent_id)" "$1" "$2" \
  "$(python3 -c 'import json,sys;print(json.dumps(sys.argv[1]))' "$3")" >> "$LEDGER"; }

# ---- helpers ------------------------------------------------------------------------------
now(){ date -u +%s; }
iso(){ date -u -r "$1" +%FT%TZ 2>/dev/null || date -u -d "@$1" +%FT%TZ; }

# "15m" / "60s" / "2h" / bare seconds -> seconds
parse_ttl(){ local t="$1" n u
  n="${t%[smh]}"; u="${t#"$n"}"
  case "$n" in ''|*[!0-9]*) die "bad --ttl '$t' (use 15m, 60s, 2h)";; esac
  case "$u" in s|'') echo "$n";; m) echo $((n*60));; h) echo $((n*3600));;
    *) die "bad --ttl unit '$u'";; esac; }

free_port(){ python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }

jget(){ [ -f "$STATE" ] || return 1
  python3 -c 'import json,sys
try: d=json.load(open(sys.argv[1]))
except Exception: sys.exit(1)
v=d.get(sys.argv[2],"")
print(v if not isinstance(v,(dict,list)) else json.dumps(v))' "$STATE" "$1" 2>/dev/null; }

alive(){ [ -n "${1:-}" ] && kill -0 "$1" 2>/dev/null; }

# Every listener owned by our pids must be on loopback. Anything on 0.0.0.0/::
# is the 2026-07-06 failure, so we refuse to continue and tear down.
assert_loopback_only(){ local pids="$1" bad
  command -v lsof >/dev/null 2>&1 || { echo "share: lsof missing, cannot assert bind addresses" >&2; return 1; }
  # shellcheck disable=SC2086  # deliberate word-splitting: pid list
  bad=$(lsof -nP -a -p "$(echo $pids | tr ' ' ',')" -iTCP -sTCP:LISTEN 2>/dev/null \
        | awk 'NR>1{print $9}' | grep -Ev '^(127\.0\.0\.1|\[::1\]|localhost):' || true)
  [ -z "$bad" ] || { echo "share: PUBLIC LISTENER DETECTED: $bad" >&2; return 1; }
  return 0; }

# Wait for cloudflared to publish its quick-tunnel hostname.
wait_for_url(){ local logf="$1" url
  for _ in $(seq 1 60); do
    url=$(grep -Eo 'https://[a-z0-9-]+\.trycloudflare\.com' "$logf" 2>/dev/null | head -1 || true)
    [ -n "$url" ] && { echo "$url"; return 0; }
    sleep 1
  done
  return 1; }

# Detached TTL timer. setsid where available, else nohup+disown; either way it
# leaves the caller's process group, so Ctrl-C / a dying ssh session cannot
# orphan a live tunnel. It calls back into THIS script's `stop`.
#
# TIMER_MARK is a human label in the timer's command line so `ps`/`pgrep` can
# show it. It is NOT how the timer is killed — see do_stop; killing by pattern
# once took out an innocent shell that merely mentioned the string.
TIMER_MARK="chrome-agent-share-timer"
arm_timer(){ local secs="$1"
  # Two defences, both MEASURED on macOS 2026-09-12, neither guessed:
  #   1. start_new_session=True IS setsid (macOS ships no setsid binary), so the
  #      timer leaves the caller's process group and session.
  #   2. the timer ignores HUP/INT/TERM. A nohup timer AND a bare new-session
  #      timer both died when the parent shell was torn down by a harness that
  #      signals descendants — the TTL silently stopped being enforced in exactly
  #      the situation the TTL exists for.
  # Ignoring TERM would make the timer un-cancellable, so `stop` escalates to
  # SIGKILL on $TIMER_MARK, which nothing can trap: cancel still works, a dying
  # caller no longer extends a share.
  python3 - "$secs" "$SELF" "$TIMER_MARK" <<'PY'
import shlex, subprocess, sys
secs, me, mark = sys.argv[1], sys.argv[2], sys.argv[3]
cmd = "trap '' HUP INT TERM; sleep %s; %s stop --reason ttl # %s" % (secs, shlex.quote(me), mark)
p = subprocess.Popen(["bash", "-c", cmd], start_new_session=True,
                     stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                     stderr=subprocess.DEVNULL)
print(p.pid)
PY
}

write_state(){ python3 - "$STATE" "$@" <<'PY'
import json,sys
path=sys.argv[1]; d={}
for kv in sys.argv[2:]:
    k,_,v=kv.partition('=')
    d[k]=v
json.dump(d,open(path,'w'),indent=2)
PY
chmod 600 "$STATE"; }

# ---- teardown -----------------------------------------------------------------------------
kill_pid(){ local p="${1:-}"; [ -n "$p" ] || return 0
  kill -TERM "$p" 2>/dev/null || true
  for _ in 1 2 3 4 5; do alive "$p" || return 0; sleep 0.3; done
  kill -KILL "$p" 2>/dev/null || true; return 0; }

do_stop(){ local reason="${1:-manual}" ttl url tp mypgid
  if [ ! -f "$STATE" ]; then echo '{"ok":true,"stopped":false,"note":"nothing running"}'; exit 0; fi
  ttl="$(jget ttl || echo '')"; url="$(jget url || echo '')"; tp="$(jget timer_pid || echo '')"
  # reverse order of bring-up: the public thing first, the private thing last.
  kill_pid "$(jget cloudflared_pid || true)"
  kill_pid "$(jget client_pid || true)"
  kill_pid "$(jget vnc_pid || true)"
  kill_pid "$(jget xvfb_pid || true)"
  kill_pid "$(jget http_pid || true)"
  # State and ledger BEFORE the timer, deliberately: when the TIMER is the caller,
  # the next step can end this very process, and a teardown that dies one line
  # before `rm` leaves a state file claiming a share that no longer exists.
  rm -f "$STATE" "$TOKEN_FILE" "$VNCPASS_FILE"
  log "share-stop" "ttl=${ttl:-?} reason=$reason" "torn down: ${url:-no-url}"
  printf '{"ok":true,"stopped":true,"reason":"%s"}\n' "$reason"
  # Cancel the timer by PROCESS GROUP, never by pattern: it is its own session
  # leader, so pgid == recorded pid and `kill -- -PID` takes the wrapper and the
  # `sleep` it is blocked on together. Two scars here, both from 2026-09-12:
  #   * `pkill -f <marker>` killed an unrelated shell that merely MENTIONED the
  #     marker on its command line. A teardown that can kill bystanders is not one.
  #   * when the timer itself calls `stop`, that stop is IN the timer's group, so
  #     the group kill was suicide — the share stayed up and nothing was logged.
  #     Hence the pgid comparison below.
  mypgid="$(ps -o pgid= -p $$ 2>/dev/null | tr -d ' ')"
  if [ -n "$tp" ] && [ "$mypgid" != "$tp" ]; then
    kill -TERM -- "-$tp" 2>/dev/null || true
    sleep 0.3
    kill -KILL -- "-$tp" 2>/dev/null || true
  fi
  exit 0; }

do_reconcile(){ local exp
  if [ ! -f "$STATE" ]; then echo '{"ok":true,"action":"none","note":"no share recorded"}'; exit 0; fi
  exp="$(jget expires_at_epoch || echo 0)"
  if [ "$(now)" -ge "${exp:-0}" ]; then do_stop "reconcile-expired"; fi
  # not expired, but the stack may have died under us; if cloudflared is gone the
  # share is a lie — clean it so the next start is honest.
  if ! alive "$(jget cloudflared_pid || true)"; then do_stop "reconcile-dead-tunnel"; fi
  printf '{"ok":true,"action":"none","seconds_remaining":%d}\n' "$((exp-$(now)))"
  exit 0; }

do_status(){ local exp left url listeners pids
  if [ ! -f "$STATE" ]; then echo '{"running":false}'; exit 0; fi
  exp="$(jget expires_at_epoch || echo 0)"; left=$((exp-$(now)))
  url="$(jget url || echo '')"
  pids="$(jget cloudflared_pid || true) $(jget client_pid || true) $(jget vnc_pid || true) $(jget xvfb_pid || true) $(jget http_pid || true)"
  pids="$(echo "$pids" | tr -s ' ')"
  listeners=$(lsof -nP -a -p "$(echo "$pids" | tr ' ' ',')" -iTCP -sTCP:LISTEN 2>/dev/null \
              | awk 'NR>1{printf "%s %s;",$1,$9}' || true)
  python3 -c 'import json,sys
print(json.dumps({"running":True,"mode":sys.argv[1],"url":sys.argv[2],
"seconds_remaining":int(sys.argv[3]),"expires_at":sys.argv[4],
"listening":sys.argv[5],"pids":sys.argv[6],
"note":"url shown WITHOUT its token; the token was printed once at start"}))' \
    "$(jget mode || echo '?')" "$url" "$left" "$(jget expires_at || echo '?')" "${listeners:-none}" "$pids"
  exit 0; }

# ---- start --------------------------------------------------------------------------------
# Linux only. The Mac refuses (see docs/share.md): a Mac has a screen, so a
# remote hand on a logged-in browser buys nothing and costs everything.
do_start(){ local ttl="15m" port="" secs token url tunlog display xvfb_pid vnc_pid client_pid cf_pid timer_pid exp
  while [ $# -gt 0 ]; do case "$1" in
    --ttl) ttl="${2:-}"; shift 2;; --port) port="${2:-}"; shift 2;;
    *) die "unknown flag $1";; esac; done
  secs="$(parse_ttl "$ttl")"
  [ "$secs" -gt 0 ] || die "--ttl must be > 0"

  if [ "$OS" != "Linux" ]; then
    cat >&2 <<'MSG'
share: refusing to start on this platform (not Linux).

A share exists because a SERVER has no screen. This machine has one: open the
browser window and log in directly (`chrome-agent up`, then log in by hand).
Exposing a logged-in browser over a tunnel to save a glance at your own display
is all of the risk and none of the benefit.

To exercise the tunnel half here without a browser:  share.sh selftest --ttl 60s
MSG
    # 1, not 2: chrome-agent's exit contract reserves 2 for "not signed in", and this script is
    # reachable through `chrome-agent share`. Wrong platform is a usage error — nothing attempted.
    exit 1
  fi

  [ -f "$STATE" ] && die "a share is already recorded ($(jget url)); run 'share.sh stop' first"
  for b in Xvfb x11vnc websockify "$CLOUDFLARED"; do
    command -v "$b" >/dev/null 2>&1 || die "missing dependency: $b"
  done
  [ -n "$port" ] || port="$(free_port)"

  # --- the secret. openssl -> a VNC password file (0600) AND the ?password= the
  # noVNC client auto-submits. It is never echoed, never logged; it appears once,
  # inside the URL this function prints.
  token="$(openssl rand -hex 16)"
  umask 077
  printf '%s' "$token" > "$TOKEN_FILE"
  x11vnc -storepasswd "${token:0:8}" "$VNCPASS_FILE" >/dev/null 2>&1 \
    || die "could not write vnc passwd file"

  display=":$(( 90 + RANDOM % 9 ))"
  Xvfb "$display" -screen 0 1440x900x24 -nolisten tcp >"$LOG_DIR/xvfb.log" 2>&1 &
  xvfb_pid=$!
  sleep 1

  # x11vnc on LOOPBACK ONLY. -localhost is the security property here.
  x11vnc -display "$display" -localhost -rfbport 5900 -rfbauth "$VNCPASS_FILE" \
         -forever -shared -noxdamage >"$LOG_DIR/x11vnc.log" 2>&1 &
  vnc_pid=$!
  sleep 1

  # noVNC/websockify, also loopback only.
  websockify --web=/usr/share/novnc/ "127.0.0.1:$port" 127.0.0.1:5900 \
    >"$LOG_DIR/websockify.log" 2>&1 &
  client_pid=$!
  sleep 1

  if ! assert_loopback_only "$xvfb_pid $vnc_pid $client_pid"; then
    kill_pid "$client_pid"; kill_pid "$vnc_pid"; kill_pid "$xvfb_pid"
    rm -f "$TOKEN_FILE" "$VNCPASS_FILE"
    die "FAIL CLOSED: something bound a public interface; stack torn down"
  fi

  tunlog="$LOG_DIR/cloudflared.log"; : > "$tunlog"
  "$CLOUDFLARED" tunnel --url "http://127.0.0.1:$port" >"$tunlog" 2>&1 &
  cf_pid=$!
  url="$(wait_for_url "$tunlog")" || {
    kill_pid "$cf_pid"; kill_pid "$client_pid"; kill_pid "$vnc_pid"; kill_pid "$xvfb_pid"
    rm -f "$TOKEN_FILE" "$VNCPASS_FILE"; die "cloudflared never published a hostname"; }

  exp=$(( $(now) + secs ))
  timer_pid="$(arm_timer "$secs")"
  write_state "mode=login" "display=$display" "port=$port" "url=$url" \
    "expires_at=$(iso "$exp")" "expires_at_epoch=$exp" "ttl=$ttl" \
    "xvfb_pid=$xvfb_pid" "vnc_pid=$vnc_pid" "client_pid=$client_pid" \
    "cloudflared_pid=$cf_pid" "timer_pid=$timer_pid"
  log "share-start" "ttl=$ttl" "share up until $(iso "$exp")"

  # The ONLY time the token is ever emitted.
  python3 -c 'import json,sys
print(json.dumps({"url":sys.argv[1]+"/vnc.html?autoconnect=1&password="+sys.argv[2],
"expires_at":sys.argv[3],"pid_file":sys.argv[4]}))' \
    "$url" "${token:0:8}" "$(iso "$exp")" "$STATE"
}

# ---- selftest ------------------------------------------------------------------------------
# Proves the half that does not need X: dummy http.server on loopback + quick
# tunnel + TTL timer + state file + teardown. No browser is launched, ever.
do_selftest(){ local ttl="60s" secs port dir tunlog url http_pid cf_pid timer_pid exp
  while [ $# -gt 0 ]; do case "$1" in --ttl) ttl="${2:-}"; shift 2;; *) die "unknown flag $1";; esac; done
  secs="$(parse_ttl "$ttl")"
  [ -f "$STATE" ] && die "a share is already recorded; run 'share.sh stop' first"
  dir="$(mktemp -d)"; echo "chrome-agent share selftest $(date -u +%FT%TZ)" > "$dir/probe.txt"
  port="$(free_port)"
  ( cd "$dir" && python3 -m http.server "$port" --bind 127.0.0.1 ) >"$LOG_DIR/http.log" 2>&1 &
  http_pid=$!
  sleep 1
  assert_loopback_only "$http_pid" || { kill_pid "$http_pid"; die "FAIL CLOSED: public listener"; }
  tunlog="$LOG_DIR/cloudflared.log"; : > "$tunlog"
  "$CLOUDFLARED" tunnel --url "http://127.0.0.1:$port" >"$tunlog" 2>&1 &
  cf_pid=$!
  url="$(wait_for_url "$tunlog")" || { kill_pid "$cf_pid"; kill_pid "$http_pid"; die "no hostname"; }
  exp=$(( $(now) + secs ))
  timer_pid="$(arm_timer "$secs")"
  write_state "mode=selftest" "port=$port" "url=$url" "dir=$dir" \
    "expires_at=$(iso "$exp")" "expires_at_epoch=$exp" "ttl=$ttl" \
    "http_pid=$http_pid" "cloudflared_pid=$cf_pid" "timer_pid=$timer_pid"
  log "share-start" "ttl=$ttl mode=selftest" "selftest share up until $(iso "$exp")"
  python3 -c 'import json,sys
print(json.dumps({"url":sys.argv[1]+"/probe.txt","expires_at":sys.argv[2],"pid_file":sys.argv[3]}))' \
    "$url" "$(iso "$exp")" "$STATE"
}

cmd="${1:-}"; shift || true
case "$cmd" in
  start)     do_start "$@";;
  selftest)  do_selftest "$@";;
  status)    do_status;;
  stop)      r="manual"; [ "${1:-}" = "--reason" ] && r="${2:-manual}"; do_stop "$r";;
  reconcile) do_reconcile;;
  *) cat >&2 <<'USAGE'
usage: share.sh start [--ttl 15m] [--port N] | status | stop | reconcile
       share.sh selftest [--ttl 60s]     # tunnel half only, no browser
USAGE
     exit 1;;   # usage, per chrome-agent's exit contract (exit-codes --json)
esac
