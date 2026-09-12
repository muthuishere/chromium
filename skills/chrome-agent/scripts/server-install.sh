#!/usr/bin/env bash
# server-install.sh — provision an Ubuntu/Debian box to RUN the chrome-agent stack.
#
# SPIKE STATUS: written and statically checked on macOS; the apt path has never been
# executed on a real Ubuntu box. Every claim marked "UNVERIFIED" in
# ../docs/server-ubuntu.md applies here too.
#
# What this DOES:
#   (a) runtime shared libraries the Chromium fork binary links against
#   (b) node + python3 (the chrome-agent CLI shells out to both, constantly)
#   (c) the login/share stack from ADR 0003: Xvfb, x11vnc, websockify/noVNC
#   (d) cloudflared (the time-boxed tunnel that fronts the share)
#   (e) a preflight that tells you, as a table, what is actually present
#
# What this deliberately DOES NOT do:
#   * build Chromium. That is hours of CPU and ~100GB of disk. See --help and
#     ../docs/server-ubuntu.md for the two ways to get a binary onto the box.
#   * start anything, log into anything, or open a port.
#   * print a token, a password, or the contents of a profile.
#
# Idempotent: safe to run twice. Every group is skippable.
#
# Usage: bash server-install.sh [flags]

set -euo pipefail

# ---- knobs -----------------------------------------------------------------------------------
# CHROME_AGENT_FORK must match what the chrome-agent CLI resolves, or the preflight lies.
# The CLI's default is the owner's checkout; we keep the identical default on purpose.
FORK="${CHROME_AGENT_FORK:-$HOME/muthu/gitworkspace/chromium}"; FORK="${FORK%/}"
OUT_DIR="${CHROMIUM_SENDKEYS_OUT:-$FORK/out/Default}"
BINARY="$OUT_DIR/chrome"   # Linux layout; see chromium-agent-launch.cjs resolveBinary()

DO_CHROMIUM_DEPS=1
DO_LANG=1
DO_SHARE=1
DO_CLOUDFLARED=1
DO_INSTALL=1            # --preflight-only turns this off wholesale
APT_YES="-y"

# ---- output ----------------------------------------------------------------------------------
say()  { printf '\033[1m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[33m warn\033[0m %s\n' "$*" >&2; }
fail() { printf '\033[31m FAIL\033[0m %s\n' "$*" >&2; }

usage() {
  cat <<'EOF'
server-install.sh — prepare an Ubuntu/Debian box for the chrome-agent stack.

Flags:
  --skip-chromium-deps   do not install the Chromium runtime shared libraries
  --skip-node            do not install node / python3
  --skip-share           do not install Xvfb / x11vnc / websockify / noVNC
  --skip-cloudflared     do not install cloudflared
  --preflight-only       install nothing; just report the table
  --no-confirm           pass -y to apt (default; kept for symmetry)
  --confirm              let apt prompt interactively
  -h, --help             this

Environment:
  CHROME_AGENT_FORK      checkout of the chromium fork  (default: ~/muthu/gitworkspace/chromium)
  CHROMIUM_SENDKEYS_OUT  build output dir               (default: $CHROME_AGENT_FORK/out/Default)

This script never builds Chromium and never starts a browser. If the preflight says the
fork is missing or unbuilt it prints the two ways to fix that and exits non-zero.
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --skip-chromium-deps) DO_CHROMIUM_DEPS=0 ;;
    --skip-node)          DO_LANG=0 ;;
    --skip-share)         DO_SHARE=0 ;;
    --skip-cloudflared)   DO_CLOUDFLARED=0 ;;
    --preflight-only)     DO_INSTALL=0 ;;
    --no-confirm)         APT_YES="-y" ;;
    --confirm)            APT_YES="" ;;
    -h|--help)            usage; exit 0 ;;
    *) fail "unknown flag: $1"; echo; usage; exit 1 ;;
  esac
  shift
done

# ---- platform gate ---------------------------------------------------------------------------
# Refuse anything that is not Ubuntu/Debian, loudly and early. Half-installing a Chromium
# runtime on the wrong distro is worse than not starting.
if [ "$(uname -s)" != "Linux" ]; then
  fail "this installer is for Ubuntu/Debian Linux; this machine is $(uname -s)."
  fail "on macOS the fork already runs natively — nothing here applies."
  exit 1
fi
if [ ! -r /etc/os-release ]; then
  fail "no /etc/os-release — cannot identify this distribution. Refusing to run apt blind."
  exit 1
fi
# shellcheck disable=SC1091
. /etc/os-release
DISTRO_ID="${ID:-unknown}"
DISTRO_LIKE="${ID_LIKE:-}"
DISTRO_CODENAME="${VERSION_CODENAME:-${UBUNTU_CODENAME:-}}"
case "$DISTRO_ID $DISTRO_LIKE" in
  *ubuntu*|*debian*) ;;
  *) fail "unsupported distribution: ${PRETTY_NAME:-$DISTRO_ID}."
     fail "this script installs via apt and only claims Ubuntu/Debian. Port it or use --preflight-only."
     exit 1 ;;
esac
if ! command -v apt-get >/dev/null 2>&1; then
  fail "apt-get not found even though this looks like ${PRETTY_NAME:-$DISTRO_ID}. Refusing to guess."
  exit 1
fi

# root or sudo, resolved once
SUDO=""
if [ "$(id -u)" -ne 0 ]; then
  if command -v sudo >/dev/null 2>&1; then
    SUDO="sudo"
  elif [ "$DO_INSTALL" -eq 1 ]; then
    fail "not root and no sudo. Re-run as root, or use --preflight-only."
    exit 1
  fi
fi

APT_UPDATED=0
apt_update_once() {
  [ "$APT_UPDATED" -eq 1 ] && return 0
  say "apt-get update"
  $SUDO apt-get update -qq
  APT_UPDATED=1
}

# Does apt know this package name at all? (Ubuntu 24.04 renamed a pile of libs with the
# 64-bit-time_t transition: libasound2 -> libasound2t64 etc. Hardcoding one name breaks
# on the other release, so every ambiguous dep is expressed as a candidate list.)
apt_has() {
  apt-cache show "$1" >/dev/null 2>&1
}

# Install every package that exists; report the ones that do not rather than aborting.
apt_install() {
  local want=() missing=() p
  for p in "$@"; do
    if apt_has "$p"; then want+=("$p"); else missing+=("$p"); fi
  done
  if [ "${#missing[@]}" -gt 0 ]; then
    warn "not in this release's archive, skipped: ${missing[*]}"
  fi
  if [ "${#want[@]}" -eq 0 ]; then
    warn "nothing to install from this group"
    return 0
  fi
  # shellcheck disable=SC2086
  $SUDO apt-get install $APT_YES --no-install-recommends "${want[@]}"
}

# Install the FIRST candidate that exists (for renamed-across-release packages).
apt_install_first() {
  local p
  for p in "$@"; do
    if apt_has "$p"; then
      # shellcheck disable=SC2086
      $SUDO apt-get install $APT_YES --no-install-recommends "$p"
      return 0
    fi
  done
  warn "none of these exist in this release: $*"
  return 0
}

# ===============================================================================================
# (a) runtime deps for the fork binary
# ===============================================================================================
install_chromium_deps() {
  say "group (a): Chromium runtime shared libraries"
  apt_update_once
  # The standard Chromium/Chrome runtime set. Matches what google-chrome-stable's .deb
  # declares as Depends, plus the bits headful-on-Xvfb needs.
  apt_install \
    ca-certificates fonts-liberation xdg-utils \
    libnss3 libnspr4 \
    libatk1.0-0 libatk-bridge2.0-0 libatspi2.0-0 \
    libcairo2 libpango-1.0-0 libpangocairo-1.0-0 \
    libdbus-1-3 libdrm2 libgbm1 libglib2.0-0 \
    libx11-6 libxcb1 libxcomposite1 libxdamage1 libxext6 libxfixes3 \
    libxkbcommon0 libxrandr2 libxrender1 libxi6 libxtst6 \
    libexpat1 libuuid1 libvulkan1 libgl1 libegl1
  # Renamed across releases (t64 transition on 24.04+).
  apt_install_first libasound2t64 libasound2
  apt_install_first libcups2t64 libcups2
  apt_install_first libgdk-pixbuf-2.0-0 libgdk-pixbuf2.0-0
  # GTK: 22.04 has gtk-3-0, 24.04 has gtk-3-0t64. Needed for headful; harmless headless.
  apt_install_first libgtk-3-0t64 libgtk-3-0
  # Fonts. Without these, headful renders tofu and every screenshot is useless.
  apt_install fonts-dejavu-core fonts-noto-color-emoji
}

# ===============================================================================================
# (b) node + python3 — the chrome-agent CLI is a bash script that shells out to both
# ===============================================================================================
install_lang() {
  say "group (b): node + python3"
  apt_update_once
  # python3 is used for every JSON encode/decode in skills/chrome-agent/chrome-agent.
  apt_install python3
  # sha256sum (coreutils) is the fallback for macOS's shasum in _spool_key(). Present on any
  # Ubuntu, asserted here rather than assumed because a wrong spool key means a wrong profile.
  apt_install coreutils
  if command -v node >/dev/null 2>&1; then
    say "node already present: $(node --version)"
  else
    # Ubuntu's packaged nodejs is old but chromesendkeys.cjs is dependency-free and
    # undemanding. If you want a modern node, install nvm/nodesource yourself BEFORE
    # running this and the branch above will leave it alone.
    apt_install nodejs
  fi
}

# ===============================================================================================
# (c) the login/share stack (ADR 0003 §2, §3) — virtual display + VNC + web client
# ===============================================================================================
install_share() {
  say "group (c): login/share stack — Xvfb, x11vnc, websockify, noVNC"
  apt_update_once
  # xvfb      : the virtual display so the fork can run HEADFUL for a login page
  # x11vnc    : exports that display over VNC, bound to 127.0.0.1 only
  # websockify: WebSocket shim noVNC needs
  # novnc     : the browser-side client, so "open this on your phone" works (ADR 0003 open q)
  # x11-utils : xdpyinfo, the only honest way to check a display is actually up
  apt_install xvfb x11vnc websockify novnc x11-utils
}

# ===============================================================================================
# (d) cloudflared — not in the Ubuntu archive; use Cloudflare's own apt repo
# ===============================================================================================
install_cloudflared() {
  say "group (d): cloudflared"
  if command -v cloudflared >/dev/null 2>&1; then
    say "cloudflared already present"
    return 0
  fi
  apt_update_once
  apt_install curl gpg
  local keyring="/usr/share/keyrings/cloudflare-main.gpg"
  local listfile="/etc/apt/sources.list.d/cloudflared.list"
  local arch; arch="$(dpkg --print-architecture)"
  if [ ! -s "$keyring" ]; then
    say "adding Cloudflare package signing key"
    $SUDO mkdir -p /usr/share/keyrings
    curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg \
      | $SUDO tee "$keyring" >/dev/null
  fi
  # Cloudflare publishes per-Ubuntu-codename components; 'any' is the safe generic.
  if [ ! -s "$listfile" ]; then
    say "adding Cloudflare apt source"
    printf 'deb [signed-by=%s arch=%s] https://pkg.cloudflare.com/cloudflared any main\n' \
      "$keyring" "$arch" | $SUDO tee "$listfile" >/dev/null
    APT_UPDATED=0
  fi
  apt_update_once
  apt_install cloudflared
}

# ===============================================================================================
# the fork itself — verified, never built
# ===============================================================================================
fork_advice() {
  cat <<EOF

  ---------------------------------------------------------------------------------------
  HOW TO GET A FORK ONTO THIS BOX (this script will not do it — it takes hours)

  The launcher resolves the Linux binary as:   \$CHROMIUM_SENDKEYS_OUT/chrome
  currently:                                   $BINARY

  PATH 1 — build here. Needs ~100GB free disk, >=16GB RAM (32GB to be comfortable),
           and hours of CPU. Depot_tools + the fork's own install-build-deps:

    git clone <the fork> "$FORK"
    cd "$FORK" && ./build/install-build-deps.sh --no-prompt
    gn gen out/Default --args='is_debug=false dcheck_always_on=false is_component_build=true'
    autoninja -C out/Default chrome

  PATH 2 — copy a build in. Build on ANOTHER LINUX x86_64 box or in CI, then:

    tar -C <that box>/out -czf default.tgz Default
    scp default.tgz this-box:  &&  mkdir -p "$FORK/out" && tar -C "$FORK/out" -xzf default.tgz

    A macOS build CANNOT be copied here. out/Default/Chromium.app is a Mach-O bundle;
    Linux needs an ELF. The copy path requires a LINUX build. See ../docs/server-ubuntu.md.

  Either way the CLI needs chromesendkeys.cjs and chromium-agent-launch.cjs from the
  SAME checkout — clone the repo even if you only copy in the binary.
  ---------------------------------------------------------------------------------------
EOF
}

# ===============================================================================================
# preflight
# ===============================================================================================
PREFLIGHT_FAILURES=0

# row <name> <status: ok|missing|warn> <detail>
row() {
  local mark
  case "$2" in
    ok)      mark=$'\033[32m  OK   \033[0m' ;;
    warn)    mark=$'\033[33m WARN  \033[0m' ;;
    *)       mark=$'\033[31mMISSING\033[0m'; PREFLIGHT_FAILURES=$((PREFLIGHT_FAILURES + 1)) ;;
  esac
  printf '  %-26s %b  %s\n' "$1" "$mark" "$3"
}

# A soft row: reports but never fails the run (optional groups the operator skipped).
row_soft() {
  local mark
  case "$2" in
    ok) mark=$'\033[32m  OK   \033[0m' ;;
    *)  mark=$'\033[33m WARN  \033[0m' ;;
  esac
  printf '  %-26s %b  %s\n' "$1" "$mark" "$3"
}

have() { command -v "$1" >/dev/null 2>&1; }

preflight() {
  echo
  say "preflight — chrome-agent on ${PRETTY_NAME:-$DISTRO_ID} (${DISTRO_CODENAME:-unknown})"
  echo
  printf '  %-26s %-7s  %s\n' "CHECK" "STATUS" "DETAIL"
  printf '  %-26s %-7s  %s\n' "--------------------------" "-------" "----------------------------------"

  # --- the fork ---
  if [ -d "$FORK" ] && [ -f "$FORK/chromesendkeys.cjs" ] && [ -f "$FORK/chromium-agent-launch.cjs" ]; then
    row "fork checkout" ok "$FORK"
  elif [ -d "$FORK" ]; then
    row "fork checkout" missing "$FORK exists but has no chromesendkeys.cjs / chromium-agent-launch.cjs"
  else
    row "fork checkout" missing "$FORK does not exist (set CHROME_AGENT_FORK)"
  fi

  if [ -x "$BINARY" ]; then
    row "fork binary executable" ok "$BINARY"
  elif [ -e "$BINARY" ]; then
    row "fork binary executable" missing "$BINARY exists but is not executable (chmod +x)"
  else
    row "fork binary executable" missing "$BINARY not built"
  fi

  # An ELF check catches the single most likely operator mistake: a macOS build copied over.
  if [ -e "$BINARY" ]; then
    if head -c 4 "$BINARY" 2>/dev/null | grep -q $'\x7fELF'; then
      row "fork binary is Linux ELF" ok "$(uname -m)"
    else
      row "fork binary is Linux ELF" missing "not an ELF — a macOS/Windows build cannot run here"
    fi
  fi

  # --- languages ---
  if have node; then row "node" ok "$(node --version 2>/dev/null)"
  else row "node" missing "install: apt-get install nodejs"; fi

  if have python3; then row "python3" ok "$(python3 --version 2>&1)"
  else row "python3" missing "install: apt-get install python3"; fi

  if have sha256sum; then row "sha256sum" ok "spool-key hashing (shasum fallback)"
  elif have shasum;   then row "sha256sum" ok "shasum present instead"
  else row "sha256sum" missing "coreutils"; fi

  # --- share stack (soft: a work-only headless box legitimately has none of this) ---
  if have Xvfb; then row_soft "Xvfb" ok "virtual display for headful login"
  else row_soft "Xvfb" warn "no headful login possible (ADR 0003 §2)"; fi

  if have x11vnc; then row_soft "x11vnc" ok "$(x11vnc -version 2>&1 | head -1)"
  else row_soft "x11vnc" warn "no share possible (ADR 0003 §3)"; fi

  if have websockify; then row_soft "websockify" ok "noVNC WebSocket shim"
  else row_soft "websockify" warn "noVNC needs it"; fi

  if [ -d /usr/share/novnc ]; then row_soft "noVNC client" ok "/usr/share/novnc"
  else row_soft "noVNC client" warn "apt-get install novnc"; fi

  if have cloudflared; then row_soft "cloudflared" ok "$(cloudflared --version 2>&1 | head -1)"
  else row_soft "cloudflared" warn "no tunnel — share would have to bind publicly, which ADR 0003 forbids"; fi

  # --- runtime libs: ask the loader, not the package manager ---
  if [ -x "$BINARY" ]; then
    if have ldd; then
      local missing_libs
      missing_libs="$(ldd "$BINARY" 2>/dev/null | grep -c 'not found' || true)"
      if [ "${missing_libs:-0}" -eq 0 ]; then
        row "shared libraries resolve" ok "ldd reports no missing objects"
      else
        row "shared libraries resolve" missing "$missing_libs unresolved — run: ldd $BINARY | grep 'not found'"
      fi
    else
      row_soft "shared libraries resolve" warn "no ldd; cannot check"
    fi
  else
    row_soft "shared libraries resolve" warn "skipped — no binary to inspect"
  fi

  # --- systemd --user lingering, for the long-running headless browser ---
  if have loginctl; then
    if loginctl show-user "$(id -un)" -p Linger 2>/dev/null | grep -q 'Linger=yes'; then
      row_soft "systemd --user lingering" ok "survives logout"
    else
      row_soft "systemd --user lingering" warn "enable: sudo loginctl enable-linger $(id -un)"
    fi
  else
    row_soft "systemd --user lingering" warn "no loginctl (container?) — use a supervisor instead"
  fi

  echo
  if [ "$PREFLIGHT_FAILURES" -eq 0 ]; then
    say "preflight: all REQUIRED checks passed."
    say "next: CHROME_AGENT_FORK=$FORK chrome-agent up   (headless — see ../docs/server-ubuntu.md)"
  else
    fail "preflight: $PREFLIGHT_FAILURES required check(s) failed."
    if [ ! -x "$BINARY" ]; then fork_advice; fi
  fi
}

# ===============================================================================================
main() {
  say "chrome-agent server install — ${PRETTY_NAME:-$DISTRO_ID}"
  if [ "$DO_INSTALL" -eq 1 ]; then
    if [ "$DO_CHROMIUM_DEPS" -eq 1 ]; then install_chromium_deps; else say "skipping group (a)"; fi
    if [ "$DO_LANG"          -eq 1 ]; then install_lang;          else say "skipping group (b)"; fi
    if [ "$DO_SHARE"         -eq 1 ]; then install_share;         else say "skipping group (c)"; fi
    if [ "$DO_CLOUDFLARED"   -eq 1 ]; then install_cloudflared;   else say "skipping group (d)"; fi
  else
    say "--preflight-only: installing nothing"
  fi
  preflight
  [ "$PREFLIGHT_FAILURES" -eq 0 ] || exit 1
}

main
