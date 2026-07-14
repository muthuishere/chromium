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

  const args = [`--user-data-dir=${profileDir}`, ...passthrough];
  if (startUrl) {
    args.push(startUrl);
  }

  console.error(
    `chromium-agent: launching\n` +
      `  binary : ${binary}\n` +
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
