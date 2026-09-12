# `share` — the time-boxed remote login window

Spike implementation of ADR 0003 §3 ("The share is a verb, and it is off by default").
Script: `skills/chrome-agent/scripts/share.sh`.

A share is **not** remote control. It is a login window with a lock on it: you hand
someone a key, you watch them use it, and the key is taken back whether or not anyone
remembers to take it back. Every rule below traces to the 2026-07-06 incident — a stale,
unauthenticated browser endpoint on a public tunnel, running as root for days.

```
share.sh start [--ttl 15m] [--port N]   # Linux only. one json line: {url, expires_at, pid_file}
share.sh status                          # json: running?, url (no token), seconds left, listeners
share.sh stop                            # idempotent, exit 0 even with nothing running
share.sh reconcile                       # watchdog/boot: expired or dead -> tear down
share.sh selftest [--ttl 60s]            # tunnel half only, dummy http.server, NO browser
```

## The stack (Linux)

```
Xvfb :9x            virtual display, -nolisten tcp
  -> x11vnc         -localhost  (127.0.0.1:5900 ONLY)
  -> websockify     127.0.0.1:<port>  serving /usr/share/novnc
  -> cloudflared tunnel --url http://127.0.0.1:<port>
```

Nothing binds `0.0.0.0`. After bring-up, `assert_loopback_only` runs `lsof` over the pids
we started and greps every LISTEN address; anything that is not `127.0.0.1` / `[::1]` /
`localhost` **tears the whole stack down and exits non-zero** before cloudflared is ever
started. Fail closed, in that order, deliberately: the tunnel is the last thing up and the
first thing down.

## State, TTL and teardown

* State is a file — `~/.config/chrome-agent/share/state.json`, mode 600 (override the
  directory with `$CHROME_AGENT_SHARE_DIR`). It holds port, url, expiry (ISO + epoch), ttl
  and every pid. So `stop` works from another shell, another ssh session, a phone.
* **The TTL is mandatory**, default 15m, and it is enforced by a *detached* timer process,
  never by the caller remembering. The timer is started with `start_new_session=True`
  (= `setsid`; macOS ships no `setsid` binary) and ignores HUP/INT/TERM, so a dying caller
  cannot extend a share.
* `stop` cancels that timer **by process group** (`kill -- -PID`), never by pattern, and
  skips the kill when the caller is itself inside that group.
* `reconcile` is the belt to the timer's braces — for cron, a watchdog, or boot. It tears
  down when the recorded expiry has passed, and also when `cloudflared` is gone but the
  state file still claims a live share (state that lies is worse than no state).
* Every start and stop appends to `~/.config/chrome-agent/actions.ndjson` in the existing
  ledger shape (`ts, profile, agent, action, target, result`), with the TTL and the reason
  in `target`: `share-start ttl=15m`, `share-stop ttl=15m reason=ttl`.

## The token

`openssl rand -hex 16` at start, written to a 0600 file and to the x11vnc password file.
It is emitted exactly once, inside the URL `start` prints. It is never echoed, never
logged, never put in the ledger, and `status` prints the URL **without** it.

> The quick-tunnel hostname is unguessable, but it is **not a secret**. It lands in
> `cloudflared`'s log, in the operator's shell history, and in whatever chat app the
> operator pastes it into. The token is the real control; the hostname is only obscurity.

## The macOS decision: refuse, don't degrade

`start` on anything that is not Linux **refuses** with an explanation and exit code 2.

Why refuse rather than build a degraded "share my visible window" mode: the share exists
because a *server has no screen*. A Mac has one. Exposing a logged-in browser over a public
tunnel to save a glance at your own display is all of the risk of this feature and none of
its benefit — and the ADR's own alternatives table already rejects the same trade in its
other forms. A degraded mac mode would also be the mode that gets used casually, i.e. the
one that goes stale on a tunnel. The mac path for login is: open the window and log in.

`selftest` exists so the *tunnel half* can still be exercised and regression-tested on a
Mac (or in CI) with a dummy `python3 -m http.server`, with no browser anywhere near it.

---

# Proven on macOS 2026-09-12

Machine: Darwin 25.4.0 (arm64), `cloudflared 2026.5.2` at `/opt/homebrew/bin/cloudflared`.
`bash -n` clean; `shellcheck` (already installed) clean, no suppressions except one
deliberate `SC2086` for a pid list.

## 1. The tunnel half, end to end, no browser — raw commands

```console
$ D=$(mktemp -d); echo "chrome-agent share proof $(date -u +%FT%TZ)" > "$D/probe.txt"
$ P=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
PORT=62134 DIR=/var/folders/cb/.../T/tmp.7VgpwssByX

$ (cd "$D" && python3 -m http.server "$P" --bind 127.0.0.1 &)
$ lsof -nP -iTCP:$P -sTCP:LISTEN
COMMAND   PID        USER   FD   TYPE   DEVICE SIZE/OFF NODE NAME
Python  20098 muthuishere    3u  IPv4 0x2b59...      0t0  TCP 127.0.0.1:62134 (LISTEN)   <-- loopback only

$ cloudflared tunnel --url "http://127.0.0.1:$P" > /tmp/proof-cf.log 2>&1 &
$ grep -Eo 'https://[a-z0-9-]+\.trycloudflare\.com' /tmp/proof-cf.log | head -1
https://communities-bags-realize-athletes.trycloudflare.com

$ curl -s --max-time 20 "$URL/probe.txt"          # off-tunnel, over the public internet
chrome-agent share proof 2026-09-12T16:58:39Z
curl-rc=0
```

Teardown and proof that it is gone:

```console
$ kill <cloudflared_pid> <http_pid>; sleep 3
$ curl -s -o /dev/null -w "http_code=%{http_code}\n" --max-time 20 "$URL/probe.txt"
http_code=530                      # Cloudflare: origin gone, tunnel dead
$ ps -p <cloudflared_pid> <http_pid>; echo rc=$?
rc=1                               # both gone
$ lsof -nP -iTCP:62134 -sTCP:LISTEN; echo rc=$?
rc=1                               # no listener
```

(Other, unrelated `cloudflared` processes for named tunnels belonging to other projects were
running on this machine before and after; only the pids started here were touched.)

## 2. The same half through `share.sh selftest`, with state + ledger

```console
$ ./share.sh selftest --ttl 60s
{"url": "https://fire-infringement-consent-accordance.trycloudflare.com/probe.txt",
 "expires_at": "2026-09-12T17:00:18Z",
 "pid_file": "/Users/.../.config/chrome-agent/share/state.json"}

$ curl -s --max-time 20 "$URL"
chrome-agent share selftest 2026-09-12T16:59:11Z

$ ./share.sh status
{"running": true, "mode": "selftest", "url": "https://fire-...trycloudflare.com",
 "seconds_remaining": 55, "expires_at": "2026-09-12T17:00:18Z",
 "listening": "Python 127.0.0.1:62218;cloudflar 127.0.0.1:62241;",   <-- both loopback
 "pids": "24646 24636",
 "note": "url shown WITHOUT its token; the token was printed once at start"}

$ tail -1 ~/.config/chrome-agent/actions.ndjson
{"ts":"2026-09-12T16:59:18Z","profile":"/Users/.../chrome-agent-profile","agent":"share",
 "action":"share-start","target":"ttl=60s mode=selftest","result":"selftest share up until 2026-09-12T17:00:18Z"}
```

## 3. The TTL tears the share down with no operator action

Final run, 45s TTL, started from a shell that then exited:

```console
17:13:02  $ ./share.sh selftest --ttl 45s
          {"url": "https://collins-terminology-pos-features.trycloudflare.com/probe.txt",
           "expires_at": "2026-09-12T17:13:47Z", ...}
          # shell ends here. nothing further is typed.

17:14:12  $ ./share.sh status
          {"running":false}
          $ curl -s -o /dev/null -w "%{http_code}" "$URL"   ->  530
          $ ls ~/.config/chrome-agent/share/                ->  logs        (state.json gone)
          $ ps -eo pid,command | grep 'cloudflared tunnel --url'  -> none

$ grep share- ~/.config/chrome-agent/actions.ndjson | tail -2
... "action":"share-start","target":"ttl=45s mode=selftest","result":"selftest share up until 2026-09-12T17:13:47Z"
... "action":"share-stop" ,"target":"ttl=45s reason=ttl"   ,"result":"torn down: https://collins-...trycloudflare.com"
```

Teardown at 17:13:48, one second after the recorded expiry, unattended.

### Two real bugs this test caught (both were in the first draft)

Worth recording, because both are the class of bug that silently disables the security
property while leaving the feature looking fine:

1. **A `nohup ... &` timer died with its parent shell** — so the TTL stopped being enforced
   in exactly the situation the TTL exists for. Fixed with a new session
   (`start_new_session=True`) plus an ignored TERM/INT/HUP in the timer. Measured, twice,
   before and after.
2. **`pkill -f <marker>` to cancel the timer killed the wrong process.** It matched an
   unrelated shell whose command line merely *mentioned* the marker string (it killed the
   operator's own shell, exit 144), and when the timer itself invoked `stop`, the pattern
   kill took out the timer's own group — i.e. the stop committed suicide mid-teardown, left
   the tunnel up and wrote nothing to the ledger. Fixed: cancel by process group
   (`kill -- -PID`, the timer is its own session leader) and skip that kill when the caller
   is inside the group; write state + ledger *before* the timer kill.

## 4. `stop`, `reconcile`, and the refusals

```console
$ ./share.sh stop                        # nothing running
{"ok":true,"stopped":false,"note":"nothing running"}   rc=0
$ ./share.sh reconcile                   # nothing recorded
{"ok":true,"action":"none","note":"no share recorded"} rc=0

$ ./share.sh start --ttl 15m             # on macOS
share: refusing to start on this platform (not Linux). ...                 rc=2

$ ./share.sh stop                        # cancels a live 600s timer, by pgid
{"ok":true,"stopped":true,"reason":"manual"}
timer alive rc=1                          # cancelled

# expired share whose timer never fired (timer group killed by hand, expiry backdated):
$ ./share.sh reconcile
{"ok":true,"stopped":true,"reason":"reconcile-expired"}
cloudflared alive rc=1        off-tunnel http_code=530

# a share that is still in date is left alone:
$ ./share.sh reconcile
{"ok":true,"action":"none","seconds_remaining":600}

# state that lies (tunnel killed underneath it) is cleaned:
$ ./share.sh reconcile
{"ok":true,"stopped":true,"reason":"reconcile-dead-tunnel"}
```

## 5. The public-bind guard actually sees a public bind

The guard's exact pipeline, run against a decoy socket bound to `0.0.0.0` that accepts
nothing:

```console
$ BAD=$(lsof -nP -a -p $BP -iTCP -sTCP:LISTEN | awk 'NR>1{print $9}' \
        | grep -Ev '^(127\.0\.0\.1|\[::1\]|localhost):')
$ echo "[$BAD]"
[*:54017]
GUARD FIRES -> start() would tear the stack down and exit non-zero
```

---

# UNVERIFIED — needs a real Linux box

Nothing below has been run. Do not treat it as working.

* **Xvfb / x11vnc / websockify bring-up.** Flags, ordering, and timing are written from the
  documented behaviour, not from a run. In particular: whether 1s between each stage is
  enough, whether `x11vnc -localhost` plus `-rfbauth` behaves as assumed, and whether
  `websockify --web=/usr/share/novnc/` is the right path on Ubuntu (the Debian package
  installs there; other distros differ).
* **The noVNC URL shape** `/vnc.html?autoconnect=1&password=<token>`. noVNC accepts a
  `password` query parameter, but it is the VNC password, so the token is truncated to the
  8-byte VNC password limit — **the effective secret is 8 hex chars (32 bits), not 128**.
  That is adequate for a 15-minute window against an unguessable hostname and nothing more.
  If this becomes a real feature rather than a spike, put Cloudflare Access in front (the
  ADR's own open question) and stop relying on a VNC password as the control.
* **The fork running headful on the virtual display**, and a human completing a real login
  through it — ADR "What would prove this" items 1 and 2 are both untouched.
* **`chrome-agent auth <domain>` reporting `signed_in: true` on the server** after such a
  login.
* Whether `lsof` is present on the target box (the guard hard-fails without it, which is
  the correct direction, but it means the dependency list is really `lsof` too).
