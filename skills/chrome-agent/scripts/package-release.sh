#!/usr/bin/env bash
# package-release.sh — turn a NON-COMPONENT build tree into a shippable engine artifact.
#
# ADR 0007 is the mandate. Two rules from it are enforced here by construction, because both have
# already shipped as bugs once:
#
#   1. NEVER package a component build. out/Default here is is_component_build=true: its
#      Chromium.app is 109 MB of launcher stub whose rpath includes @loader_path/../../.., and the
#      real browser is 524 loose dylibs (699 MB) sitting beside it in the build dir. Copied
#      anywhere else it dies with `dyld: Library not loaded: @rpath/libc++_chrome.dylib`. That
#      artifact works perfectly on the machine that built it and nowhere else, which is the worst
#      possible failure mode, so this script reads args.gn and REFUSES.
#   2. NEVER ship the whole out dir. out/Release on Linux measured 9.5 GB of test binaries and
#      object files; the artifact is 531 MB. Only the documented per-OS set is staged.
#
# It does not verify anything. Packaging and verification are separate on purpose: verify-release.sh
# runs ADR 0007's five gates against the finished artifact on a machine that did not build it.
#
#   bash scripts/package-release.sh                          # host OS, out/Release, fork.1
#   bash scripts/package-release.sh --build-dir out/Release --fork-n 2
#   bash scripts/package-release.sh --all-locales            # +120 MB, all 123 MB of locales
#   bash scripts/package-release.sh --zip                    # darwin: also a ditto zip (notarization)
#   bash scripts/package-release.sh --stage-only             # stage + manifest, no archive
#
set -euo pipefail

SELF="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SKILL="$(cd "$SELF/.." && pwd)"
FORK="$(cd "$SKILL/../.." && pwd)"

BUILD_DIR="out/Release"
DIST="$FORK/dist"
FORK_N="1"
ALL_LOCALES=0
MAKE_ZIP=0
STAGE_ONLY=0
OS_OVERRIDE=""

die(){ printf '\033[31mpackage-release: %s\033[0m\n' "$*" >&2; exit 1; }
say(){ printf '\033[36m==\033[0m %s\n' "$*"; }
note(){ printf '   %s\n' "$*"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --build-dir)   BUILD_DIR="${2:?}"; shift 2 ;;
    --dist|--out-dir) DIST="${2:?}"; shift 2 ;;
    --fork-n)      FORK_N="${2:?}"; shift 2 ;;
    --os)          OS_OVERRIDE="${2:?}"; shift 2 ;;
    --all-locales) ALL_LOCALES=1; shift ;;
    --zip)         MAKE_ZIP=1; shift ;;
    --stage-only)  STAGE_ONLY=1; shift ;;
    -h|--help)     sed -n '2,30p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *)             die "unknown flag $1 (try --help)" ;;
  esac
done

case "$BUILD_DIR" in /*) BUILD="$BUILD_DIR" ;; *) BUILD="$FORK/$BUILD_DIR" ;; esac
[ -d "$BUILD" ] || die "no build dir at $BUILD"

# --- sha256, on either OS, without node or python3 ------------------------------------------------
sha256(){
  if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  else die "neither shasum nor sha256sum is available — cannot checksum the artifact"; fi
}
bytes(){ if stat -f%z "$1" >/dev/null 2>&1; then stat -f%z "$1"; else stat -c%s "$1"; fi; }
human(){ du -sh "$1" 2>/dev/null | awk '{print $1}'; }

# --- gate: this MUST be a non-component release build ---------------------------------------------
ARGS_GN="$BUILD/args.gn"
[ -f "$ARGS_GN" ] || die "$ARGS_GN is missing — this is not a gn output dir"
ARGS_ONELINE="$(tr '\n' ' ' < "$ARGS_GN" | tr -s ' ' | sed 's/ *$//')"

if grep -Eq '^[[:space:]]*is_component_build[[:space:]]*=[[:space:]]*true' "$ARGS_GN"; then
  die "$BUILD is a COMPONENT build (is_component_build=true).
   A component build is not relocatable: the .app/binary is a stub that loads hundreds of loose
   dylibs out of the build tree, and it dies on any other machine (ADR 0007 §1).
   Build a release tree instead:
     gn gen out/Release --args='is_debug=false is_component_build=false dcheck_always_on=false \\
       symbol_level=0 blink_symbol_level=0 is_official_build=false proprietary_codecs=true ffmpeg_branding=\"Chrome\"'
     autoninja -C out/Release chrome"
fi
grep -Eq '^[[:space:]]*is_component_build[[:space:]]*=[[:space:]]*false' "$ARGS_GN" \
  || die "$ARGS_GN does not say is_component_build=false. Refusing to guess (ADR 0007 §1)."

# --- identity: version, os, arch, name ------------------------------------------------------------
V="$FORK/chrome/VERSION"
[ -f "$V" ] || die "no chrome/VERSION at $V — is $FORK really the fork?"
CHROMIUM_VERSION="$(awk -F= '/^MAJOR/{a=$2}/^MINOR/{b=$2}/^BUILD/{c=$2}/^PATCH/{d=$2}END{print a"."b"."c"."d}' "$V")"

OS="${OS_OVERRIDE:-$(uname -s | tr '[:upper:]' '[:lower:]')}"
case "$(uname -m)" in
  arm64|aarch64) ARCH="arm64" ;;
  x86_64|amd64)  ARCH="x64" ;;
  *) die "unknown arch $(uname -m)" ;;
esac
case "$OS" in
  darwin|linux) : ;;
  *) die "no shippable set is defined for $OS (ADR 0007 §5 defers win-x64)" ;;
esac

NAME="chrome-agent-engine-${CHROMIUM_VERSION}+fork.${FORK_N}-${OS}-${ARCH}"
STAGE="$DIST/$NAME"

say "packaging $NAME"
note "build tree : $BUILD"
note "staging to : $STAGE"

mkdir -p "$DIST"
rm -rf "$STAGE"
mkdir -p "$STAGE"

# --- the per-OS shippable set ---------------------------------------------------------------------
#
# darwin: everything is inside Chromium.app. Non-component, the whole browser is
#   Contents/Frameworks/Chromium Framework.framework — the helpers, the paks, icudtl.dat and the v8
#   snapshots all live in the bundle, which is why the bundle alone is the artifact.
# linux: `chrome` is one ELF binary, but it is useless without its resources beside it. This list is
#   exactly what dist/README.md documents for the shipped linux-x64 artifact.
stage_darwin(){
  local app="$BUILD/Chromium.app"
  [ -d "$app" ] || die "no Chromium.app in $BUILD — run: autoninja -C $BUILD_DIR chrome"
  say "copying Chromium.app"
  # ditto preserves the bundle exactly: symlinks inside the framework, permissions, and any
  # signature already applied. cp -R has historically flattened framework version symlinks.
  if command -v ditto >/dev/null 2>&1; then ditto "$app" "$STAGE/Chromium.app"
  else cp -Rp "$app" "$STAGE/Chromium.app"; fi

  # The stub check, belt and braces: a component-build launcher is ~35 KB. A non-component one is
  # still small (the code is in the framework), so the real tell is the framework's own binary.
  local fw
  fw="$(find "$STAGE/Chromium.app/Contents/Frameworks" -maxdepth 4 -name 'Chromium Framework' -type f 2>/dev/null | head -1)"
  [ -n "$fw" ] || die "no Chromium Framework binary inside the bundle — this build is not self-contained"
  local fwb; fwb="$(bytes "$fw")"
  [ "$fwb" -gt 50000000 ] || die "Chromium Framework is only $fwb bytes — that is a component-build stub, not the browser"
  note "framework  : $fwb bytes"
}

stage_linux(){
  local b="$BUILD"
  [ -x "$b/chrome" ] || die "no chrome binary in $b — run: autoninja -C $BUILD_DIR chrome"
  say "copying the linux shippable set"
  local f
  for f in chrome chrome_crashpad_handler \
           chrome_100_percent.pak chrome_200_percent.pak resources.pak \
           icudtl.dat snapshot_blob.bin v8_context_snapshot.bin \
           libEGL.so libGLESv2.so libvk_swiftshader.so vk_swiftshader_icd.json; do
    if [ -e "$b/$f" ]; then cp -p "$b/$f" "$STAGE/$f"
    else note "absent (skipped): $f"; fi
  done
  mkdir -p "$STAGE/locales"
  if [ "$ALL_LOCALES" = 1 ]; then
    cp -p "$b"/locales/*.pak "$STAGE/locales/" 2>/dev/null || true
  else
    # en-US only. The full set is 123 MB of the 531 MB tree; nothing in the agent path reads them.
    for f in en-US.pak en.pak; do
      [ -e "$b/locales/$f" ] && cp -p "$b/locales/$f" "$STAGE/locales/$f"
    done
  fi
}

trim_darwin_locales(){
  [ "$ALL_LOCALES" = 1 ] && { note "keeping all locales (--all-locales)"; return 0; }
  local res
  res="$(find "$STAGE/Chromium.app/Contents/Frameworks" -maxdepth 5 -type d -name Resources 2>/dev/null | head -1)"
  [ -n "$res" ] || { note "no framework Resources dir — no locales to trim"; return 0; }
  local before after n=0
  before="$(human "$res")"
  local d
  while IFS= read -r d; do
    case "$(basename "$d")" in
      en.lproj|en_US.lproj|en-US.lproj) : ;;
      *) rm -rf "$d"; n=$((n+1)) ;;
    esac
  done < <(find "$res" -maxdepth 1 -type d -name '*.lproj')
  after="$(human "$res")"
  note "locales    : removed $n .lproj dirs ($before -> $after; --all-locales keeps them)"
}

case "$OS" in
  darwin) stage_darwin; trim_darwin_locales ;;
  linux)  stage_linux ;;
esac

# --- the two files that make the artifact self-sufficient -----------------------------------------
#
# The Go client resolves the fork (CHROME_AGENT_FORK) to find chromesendkeys.cjs and
# chromium-agent-launch.cjs, and the binary to CHROMIUM_SENDKEYS_OUT (or <fork>/out/Default).
# Staging those two files (17 KB) plus an out/Default symlink back to the artifact root means ONE
# env var makes an unpacked artifact fully resolvable on a box with no checkout — which is exactly
# what ADR 0007's "doctor on a clean box, no repo present" asks for. The binaries stay at the
# artifact root, so the CHROMIUM_SENDKEYS_OUT form documented in dist/README.md keeps working too.
for f in chromesendkeys.cjs chromium-agent-launch.cjs; do
  [ -f "$FORK/$f" ] && cp -p "$FORK/$f" "$STAGE/$f"
done
mkdir -p "$STAGE/out"
( cd "$STAGE/out" && ln -sfn ../ Default )

# --- the fork patch: the three fields that make a build reproducible -------------------------------
UPSTREAM_BASE="${UPSTREAM_BASE:-}"
PATCH_SHA=""; PATCH_FILES=""; PATCH_LINES=""
if command -v git >/dev/null 2>&1 && git -C "$FORK" rev-parse --git-dir >/dev/null 2>&1; then
  [ -n "$UPSTREAM_BASE" ] || UPSTREAM_BASE="$(git -C "$FORK" rev-list --max-parents=0 HEAD 2>/dev/null | tail -1)"
  if [ -n "$UPSTREAM_BASE" ]; then
    P="$(mktemp)"
    if git -C "$FORK" diff "$UPSTREAM_BASE" HEAD > "$P" 2>/dev/null && [ -s "$P" ]; then
      PATCH_SHA="$(sha256 "$P")"
      PATCH_FILES="$(git -C "$FORK" diff --name-only "$UPSTREAM_BASE" HEAD 2>/dev/null | wc -l | tr -d ' ')"
      PATCH_LINES="$(git -C "$FORK" diff --shortstat "$UPSTREAM_BASE" HEAD 2>/dev/null | sed 's/^ *//')"
    fi
    rm -f "$P"
  fi
fi

STAGE_HUMAN="$(human "$STAGE")"
say "staged: $STAGE_HUMAN"

# --- archive ---------------------------------------------------------------------------------------
TARBALL=""; ZIPFILE=""
if [ "$STAGE_ONLY" = 0 ]; then
  say "archiving (this takes a few minutes at this size)"
  TARBALL="$DIST/$NAME.tar.gz"
  rm -f "$TARBALL"
  ( cd "$DIST" && tar czf "$NAME.tar.gz" "$NAME" )
  note "tar.gz     : $(human "$TARBALL") ($(bytes "$TARBALL") bytes)"

  if [ "$MAKE_ZIP" = 1 ] && [ "$OS" = darwin ]; then
    # notarytool takes a zip, and ONLY ditto's zip preserves an .app's symlinks and signature.
    # `zip -r` produces an archive that notarization rejects.
    ZIPFILE="$DIST/$NAME.zip"
    rm -f "$ZIPFILE"
    command -v ditto >/dev/null 2>&1 || die "--zip needs ditto (macOS)"
    ditto -c -k --keepParent --sequesterRsrc "$STAGE/Chromium.app" "$ZIPFILE"
    note "zip        : $(human "$ZIPFILE") — for notarytool submission only"
  fi
fi

# --- manifest + SHA256SUMS ------------------------------------------------------------------------
# Shape matches dist/manifest.json (ADR 0007 §4). Gates are recorded as UNVERIFIED: this script does
# not run them, verify-release.sh does, and a manifest that claims a pass nobody ran is worse than
# no manifest at all.
MAN="$DIST/$NAME.manifest.json"
ART_NAME=""; ART_BYTES="null"; ART_HUMAN=""; ART_SHA=""
if [ -n "$TARBALL" ]; then
  ART_NAME="$(basename "$TARBALL")"; ART_BYTES="$(bytes "$TARBALL")"
  ART_HUMAN="$(human "$TARBALL")";  ART_SHA="$(sha256 "$TARBALL")"
fi

if [ "$OS" = darwin ]; then
  MAIN_BIN="$(find "$STAGE/Chromium.app/Contents/Frameworks" -maxdepth 4 -name 'Chromium Framework' -type f | head -1)"
  BIN_LABEL="Chromium Framework (inside Chromium.app)"
else
  MAIN_BIN="$STAGE/chrome"; BIN_LABEL="chrome"
fi
MAIN_BYTES="$(bytes "$MAIN_BIN" 2>/dev/null || echo 0)"

cat > "$MAN" <<JSON
{
  "artifact": "${ART_NAME}",
  "chromium_version": "${CHROMIUM_VERSION}",
  "upstream_base": "${UPSTREAM_BASE}",
  "fork_patch_sha256": "${PATCH_SHA}",
  "fork_patch_files": "${PATCH_FILES}",
  "fork_patch_lines": "${PATCH_LINES}",
  "args_gn": "$(printf '%s' "$ARGS_ONELINE" | sed 's/"/\\"/g')",
  "built_on": "$(uname -s) $(uname -r) / $(uname -m) / $( (sysctl -n hw.ncpu 2>/dev/null || nproc) ) cores",
  "built_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "os": "${OS}",
  "arch": "${ARCH}",
  "sizes": {
    "main_binary": "${BIN_LABEL}",
    "main_binary_bytes": ${MAIN_BYTES},
    "staged_tree": "${STAGE_HUMAN}$( [ "$ALL_LOCALES" = 1 ] && echo ' (all locales)' || echo ' (en-US locale only)')",
    "tarball_bytes": ${ART_BYTES},
    "tarball_human": "${ART_HUMAN}"
  },
  "sha256": "${ART_SHA}",
  "signed": false,
  "notarized": false,
  "gates": {
    "relocation": "UNVERIFIED — run scripts/verify-release.sh",
    "webdriver": "UNVERIFIED — run scripts/verify-release.sh",
    "spool_protocol": "UNVERIFIED — run scripts/verify-release.sh",
    "doctor": "UNVERIFIED — run scripts/verify-release.sh",
    "read": "UNVERIFIED — run scripts/verify-release.sh"
  },
  "notes": [
    "Packaged by scripts/package-release.sh. Gates are UNVERIFIED until verify-release.sh writes them.",
    "$( [ "$OS" = darwin ] && echo 'macOS: unsigned and un-notarized. Gatekeeper will quarantine this on any machine that DOWNLOADS it — see docs/release-macos.md.' || echo 'Nothing signs a Linux artifact; the sha256 is the integrity story.' )",
    "proprietary_codecs carries an H.264/AAC patent-licensing obligation on DISTRIBUTED binaries (ADR 0007)."
  ]
}
JSON
say "manifest: $MAN"

if [ -n "$TARBALL" ]; then
  SUMS="$DIST/SHA256SUMS"
  NEW="$DIST/.SHA256SUMS.new"
  : > "$NEW"
  # Keep every OTHER artifact's line; replace this one's, so repackaging never leaves a stale sum.
  if [ -f "$SUMS" ]; then grep -v -F -e "$ART_NAME" -e "$NAME.zip" "$SUMS" >> "$NEW" || true; fi
  printf '%s  %s\n' "$ART_SHA" "$ART_NAME" >> "$NEW"
  if [ -n "$ZIPFILE" ]; then printf '%s  %s\n' "$(sha256 "$ZIPFILE")" "$(basename "$ZIPFILE")" >> "$NEW"; fi
  mv "$NEW" "$SUMS"
  say "SHA256SUMS updated"
fi

echo
say "done"
note "stage   : $STAGE"
[ -n "$TARBALL" ] && note "tarball : $TARBALL"
[ -n "$ZIPFILE" ] && note "zip     : $ZIPFILE"
note "manifest: $MAN"
echo
echo "Next — the artifact is NOT a release until the five gates pass on a machine that did not build it:"
echo "  bash $SKILL/scripts/verify-release.sh ${TARBALL:-$STAGE}"
[ "$OS" = darwin ] && echo "  and, before anyone downloads it: $SKILL/docs/release-macos.md (codesign + notarize)"
exit 0
