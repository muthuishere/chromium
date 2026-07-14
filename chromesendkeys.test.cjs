#!/usr/bin/env node
// Copyright 2026 The Chromium Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.
//
// Unit tests for the chromesendkeys.cjs producer CLI's spool-directory
// protocol: staging, atomic publish, and the per-verb line encoding. These
// run with Node's built-in test runner (no deps, no browser):
//
//   node --test chromesendkeys.test.cjs
//
// They test the CLI as a black box (spawn it against a temp spool dir and
// inspect the published files), which is exactly the surface where the
// "action verbs never push()" bug lived -- a bug these tests now guard.

const { test } = require('node:test');
const assert = require('node:assert');
const { execFileSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const CLI = path.join(__dirname, 'chromesendkeys.cjs');

function freshDir() {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'sk-test-'));
}

// Runs the CLI once against `dir` and returns the list of PUBLISHED command
// files (the <ts>-<pid>-<rand>.txt ones the watcher would consume), each with
// its contents. Ignores the .chromium-sendkeys-staging dotfile and results/.
function run(dir, args) {
  execFileSync('node', [CLI, '--dir', dir, ...args], { stdio: 'pipe' });
  return fs
    .readdirSync(dir)
    .filter((f) => f.endsWith('.txt') && !f.startsWith('.'))
    .map((f) => fs.readFileSync(path.join(dir, f), 'utf8'));
}

function stagingContent(dir) {
  const p = path.join(dir, '.chromium-sendkeys-staging');
  return fs.existsSync(p) ? fs.readFileSync(p, 'utf8') : null;
}

test('type auto-publishes a TEXT: line', () => {
  const dir = freshDir();
  const published = run(dir, ['type', 'hello world']);
  assert.strictEqual(published.length, 1, 'exactly one file published');
  assert.strictEqual(published[0], 'TEXT:hello world\n');
});

test('key auto-publishes a KEY: line', () => {
  const dir = freshDir();
  const published = run(dir, ['key', 'ctrl+shift+t']);
  assert.strictEqual(published[0], 'KEY:ctrl+shift+t\n');
});

test('click encodes CLICK:x,y and auto-publishes', () => {
  const dir = freshDir();
  const published = run(dir, ['click', '400', '300']);
  assert.strictEqual(published[0], 'CLICK:400,300\n');
});

test('rightclick encodes RIGHTCLICK:x,y', () => {
  const dir = freshDir();
  const published = run(dir, ['rightclick', '12', '34']);
  assert.strictEqual(published[0], 'RIGHTCLICK:12,34\n');
});

test('goto encodes GOTO:<url> and auto-publishes', () => {
  const dir = freshDir();
  const published = run(dir, ['goto', 'https://example.com/']);
  assert.strictEqual(published[0], 'GOTO:https://example.com/\n');
});

// Regression test for the real bug found while running the feature: the
// action verbs used to only append to staging and never push, so the command
// never reached the watcher. This asserts screenshot actually publishes.
test('screenshot auto-publishes (regression: verbs must push)', () => {
  const dir = freshDir();
  const published = run(dir, ['screenshot', '/tmp/x.png']);
  assert.strictEqual(published.length, 1, 'screenshot must publish, not just stage');
  assert.strictEqual(published[0], 'SCREENSHOT:/tmp/x.png\n');
});

test('add stages WITHOUT publishing; push then publishes', () => {
  const dir = freshDir();
  // `add` is the one stage-only verb (for batching).
  const afterAdd = run(dir, ['add', 'TEXT:one']);
  assert.strictEqual(afterAdd.length, 0, 'add must not publish');
  assert.strictEqual(stagingContent(dir), 'TEXT:one\n', 'add wrote to staging');
  // A second add appends to the same batch.
  run(dir, ['add', 'KEY:enter']);
  assert.strictEqual(stagingContent(dir), 'TEXT:one\nKEY:enter\n');
  // push publishes the whole batch as one file, atomically.
  const afterPush = run(dir, ['push']);
  assert.strictEqual(afterPush.length, 1, 'push publishes one file');
  assert.strictEqual(afterPush[0], 'TEXT:one\nKEY:enter\n');
  assert.strictEqual(stagingContent(dir), null, 'staging consumed by push');
});

test('send stages + publishes in one call', () => {
  const dir = freshDir();
  const published = run(dir, ['send', 'TEXT:hi']);
  assert.strictEqual(published.length, 1);
  assert.strictEqual(published[0], 'TEXT:hi\n');
});

test('netlog start publishes NETLOG:START', () => {
  const dir = freshDir();
  const published = run(dir, ['netlog', 'start']);
  assert.strictEqual(published[0], 'NETLOG:START\n');
});

test('netlog stop publishes NETLOG:STOP:<path>', () => {
  const dir = freshDir();
  const published = run(dir, ['netlog', 'stop', '/tmp/net.json']);
  assert.strictEqual(published[0], 'NETLOG:STOP:/tmp/net.json\n');
});

test('published filename is unique per call (no clobber)', () => {
  const dir = freshDir();
  run(dir, ['type', 'a']);
  run(dir, ['type', 'b']);
  const files = fs
    .readdirSync(dir)
    .filter((f) => f.endsWith('.txt') && !f.startsWith('.'));
  assert.strictEqual(files.length, 2, 'two distinct published files');
});

test('$CHROMIUM_SENDKEYS_DIR is used when --dir is omitted', () => {
  const dir = freshDir();
  execFileSync('node', [CLI, 'type', 'envtest'], {
    stdio: 'pipe',
    env: { ...process.env, CHROMIUM_SENDKEYS_DIR: dir },
  });
  const published = fs
    .readdirSync(dir)
    .filter((f) => f.endsWith('.txt') && !f.startsWith('.'))
    .map((f) => fs.readFileSync(path.join(dir, f), 'utf8'));
  assert.strictEqual(published[0], 'TEXT:envtest\n');
});
