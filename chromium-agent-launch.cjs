#!/usr/bin/env node
// Copyright 2026 The Chromium Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.
//
// Cross-platform launcher for the sendkeys-enabled Chromium build. Works on
// macOS, Windows, and Linux: it locates the built browser binary for the
// current OS, ensures the spool directory exists (the in-browser watcher
// refuses to start without it), sets CHROMIUM_SENDKEYS_DIR, and spawns the
// browser with a dedicated profile. See //CHROMIUM_SENDKEYS_SPEC.md.
//
// Usage:
//   node chromium-agent-launch.js [url] [-- <extra chromium flags>]
//
// Env / defaults (override any with an env var):
//   CHROMIUM_SENDKEYS_OUT     build output dir      (default: out/Default)
//   CHROMIUM_SENDKEYS_DIR     command spool dir     (default: ~/chrome-agent-sendkeys)
//   CHROMIUM_AGENT_PROFILE    --user-data-dir       (default: ~/chrome-agent-profile)
//   CHROMIUM_AGENT_HEADLESS   1/true -> headless    (default: headful)
//
// Headless vs headful, same setup, no rebuild: default is headful (a real
// window). Pass `--headless` (or set CHROMIUM_AGENT_HEADLESS=1) to run the
// exact same profile/spool/agent protocol with no window -- for servers, CI,
// or a roaming synced profile on a box with no display. The spool watcher,
// tabId registry, eval, and screenshots all work identically in both modes.
//
// The producer CLI (chromesendkeys.js) is already OS-agnostic; this launcher
// is the missing piece that makes the whole thing "run anywhere".

const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawn } = require('child_process');

const repoRoot = __dirname;
const outDir =
  process.env.CHROMIUM_SENDKEYS_OUT || path.join(repoRoot, 'out', 'Default');

// Per-OS built-binary layout. macOS ships an .app bundle; Windows and Linux
// ship a bare executable in the out dir.
function resolveBinary() {
  switch (process.platform) {
    case 'darwin':
      return path.join(
        outDir,
        'Chromium.app',
        'Contents',
        'MacOS',
        'Chromium',
      );
    case 'win32':
      return path.join(outDir, 'chrome.exe');
    default: // linux and other unixes
      return path.join(outDir, 'chrome');
  }
}

function main() {
  const binary = resolveBinary();
  if (!fs.existsSync(binary)) {
    console.error(
      `chromium-agent: built binary not found at ${binary}\n` +
        `  (build it first: autoninja -C ${path.relative(repoRoot, outDir)} chrome)`,
    );
    process.exit(1);
  }

  const spoolDir =
    process.env.CHROMIUM_SENDKEYS_DIR ||
    path.join(os.homedir(), 'chrome-agent-sendkeys');
  const profileDir =
    process.env.CHROMIUM_AGENT_PROFILE ||
    path.join(os.homedir(), 'chrome-agent-profile');

  // The watcher errors and does not start if the spool dir is missing, and it
  // never creates it -- so the launcher does, once, here.
  fs.mkdirSync(spoolDir, { recursive: true });

  // Split argv into a leading optional URL and passthrough flags after `--`.
  const argv = process.argv.slice(2);
  const sep = argv.indexOf('--');
  const head = sep === -1 ? argv : argv.slice(0, sep);
  const passthrough = sep === -1 ? [] : argv.slice(sep + 1);
  const startUrl = head.find((a) => !a.startsWith('-'));

  // Headless is opt-in via a `--headless` flag (before `--`) or
  // CHROMIUM_AGENT_HEADLESS=1/true. Same binary, profile, and agent protocol;
  // only the window presence changes. `--headless=new` is the modern headless
  // that shares the full browser feature set (not the legacy shell).
  const envHeadless = /^(1|true|yes)$/i.test(
    process.env.CHROMIUM_AGENT_HEADLESS || '',
  );
  const flagHeadless = head.some(
    (a) => a === '--headless' || a === '--headless=new',
  );
  const headless = envHeadless || flagHeadless;

  // Keep the agent browser to a single, predictable window/tab: no first-run
  // welcome tab, no default-browser prompt, no crash-restore bubble stealing
  // foreground. Without these the official build opens an extra welcome tab
  // that becomes the "active tab" the watcher targets instead of the page.
  const args = [
    `--user-data-dir=${profileDir}`,
    '--no-first-run',
    '--no-default-browser-check',
    '--disable-session-crashed-bubble',
    '--hide-crash-restore-bubble',
    // Audio testing: auto-grant mic/camera permission (no dialog) so
    // enumerateDevices() exposes device labels and a tab can select a specific
    // input (e.g. BlackHole) via getUserMedia({audio:{deviceId}}) and route
    // output via HTMLMediaElement.setSinkId(deviceId). Real devices, faked UI.
    '--use-fake-ui-for-media-stream',
  ];
  if (headless) {
    // Modern headless: full-featured, no window. Force software/consistent
    // rasterization so offscreen tab screenshots (CopyFromSurface) still work
    // where there is no GPU/display, mirroring the background-capture path.
    args.push('--headless=new');
  }
  args.push(...passthrough);
  if (startUrl) {
    args.push(startUrl);
  }

  console.error(
    `chromium-agent: launching\n` +
      `  binary : ${binary}\n` +
      `  mode   : ${headless ? 'headless (--headless=new)' : 'headful'}\n` +
      `  spool  : ${spoolDir}  (CHROMIUM_SENDKEYS_DIR)\n` +
      `  profile: ${profileDir}` +
      (startUrl ? `\n  url    : ${startUrl}` : ''),
  );

  const child = spawn(binary, args, {
    stdio: 'inherit',
    env: { ...process.env, CHROMIUM_SENDKEYS_DIR: spoolDir },
  });
  child.on('exit', (code) => process.exit(code === null ? 1 : code));
}

main();
