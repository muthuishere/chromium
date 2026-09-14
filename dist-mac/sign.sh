#!/usr/bin/env bash
# sign.sh — inside-out Developer ID signing of the fork's Chromium.app, on a STAGED copy.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ID="Developer ID Application: Suguna Paulraj (6PQ6534W2R)"
ENT="$ROOT/dist-mac/ent"
SRC="$ROOT/out/Release/Chromium.app"
APP="$ROOT/dist-mac/Chromium.app"

rm -rf "$APP"; cp -R "$SRC" "$APP"
FW="$APP/Contents/Frameworks/Chromium Framework.framework"
V="$FW/Versions/152.0.7948.0"

sign(){ codesign --force --timestamp --options runtime --sign "$ID" "$@"; }

echo "== 1. dylibs (no entitlements) =="
find "$V/Libraries" -name "*.dylib" -print0 2>/dev/null | while IFS= read -r -d '' f; do sign "$f"; done

echo "== 2. helper apps (each with its entitlements) =="
H="$FW/Helpers"
sign --entitlements "$ENT/helper-renderer.entitlements" "$H/Chromium Helper (Renderer).app"
sign --entitlements "$ENT/helper-renderer.entitlements" "$H/Chromium Helper (GPU).app"
sign --entitlements "$ENT/helper.entitlements"          "$H/Chromium Helper.app"
sign --entitlements "$ENT/helper.entitlements"          "$H/Chromium Helper (Alerts).app"

echo "== 2b. loose Mach-O executables in Helpers (crashpad, app_mode_loader, shortcut copier) =="
# Notary rejected the first mac submission for exactly these: ad-hoc linker signatures.
find "$V/Helpers" -maxdepth 1 -type f -perm +111 -print0 | while IFS= read -r -d '' f; do sign "$f"; done

echo "== 3. any XPC services / nested bundles =="
find "$V" \( -name "*.xpc" -o -name "*.framework" \) -print0 2>/dev/null | while IFS= read -r -d '' f; do sign "$f"; done

echo "== 4. the framework =="
sign "$FW"

echo "== 5. the main app (hardened runtime + device entitlements) =="
sign --entitlements "$ENT/app.entitlements" "$APP"

echo "== verify =="
codesign --verify --deep --strict --verbose=2 "$APP" 2>&1 | tail -3
echo "-- spctl (Gatekeeper assessment, pre-notarization) --"
spctl -a -vvv -t exec "$APP" 2>&1 | head -3 || true
echo "SIGN_DONE $APP"
