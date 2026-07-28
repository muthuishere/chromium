#!/usr/bin/env node
// Copyright 2026 The Chromium Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.
//
// Producer CLI for the CHROMIUM_SENDKEYS_DIR spool-directory protocol
// implemented by chrome/browser/sendkeys_watcher.{h,cc}. See
// //CHROMIUM_SENDKEYS_SPEC.md for the design writeup.
//
// Dependency-free Node script -- same shape as the Ghostty terminal spike's
// sendkeys.js, extended with browser-specific commands (click/rightclick/
// goto/screenshot).
//
// Usage:
//   chromesendkeys.js --dir <spool> add   <TEXT:...|KEY:...|CLICK:...|literal>
//   chromesendkeys.js --dir <spool> type  <text>
//   chromesendkeys.js --dir <spool> key   <combo>
//   chromesendkeys.js --dir <spool> click <x> <y>
//   chromesendkeys.js --dir <spool> rightclick <x> <y>
//   chromesendkeys.js --dir <spool> goto  <url>
//   chromesendkeys.js --dir <spool> screenshot <path>
//   chromesendkeys.js --dir <spool> eval  <js>              (waits for result)
//   chromesendkeys.js --dir <spool> getdom                  (waits for result)
//   chromesendkeys.js --dir <spool> http  <url> [method]    (waits for result)
//   chromesendkeys.js --dir <spool> waitfor <timeout_ms> <js-expr> (waits for result)
//   chromesendkeys.js --dir <spool> netlog start
//   chromesendkeys.js --dir <spool> netlog stop <path>
//   chromesendkeys.js --dir <spool> send  <TEXT:...|KEY:...|literal>
//   chromesendkeys.js --dir <spool> push
//
// add/type/key/click/rightclick/goto/screenshot/eval/waitfor/netlog append a
// line to .chromium-sendkeys-staging inside the spool dir, then push
// publishes it atomically (fs.renameSync) as <timestamp>-<pid>-<rand>.txt --
// the watcher only ever sees fully-written files. send = add + push in one
// call. --dir falls back to $CHROMIUM_SENDKEYS_DIR.
//
// eval/getdom/http/waitfor are two-way: the browser writes its result to
// <spool>/results/<id>.json. This CLI generates a random id, pushes the
// command, then polls for that result file (deleting it once read) and
// prints its contents -- there is no push-notification, only polling, same
// as the watcher's own directory-scan design.

const fs = require('fs');
const path = require('path');

// When set (via --tab <tabId>), every emitted spool line is prefixed with
// `TAB:<tabId>|` so the fork pins the command to that tab regardless of window
// focus. Only targeting verbs (goto/eval/evalasync/click/text/key/screenshot/
// waitfor/netlog) should be invoked with --tab; tab-management verbs
// (newtab/listtabs/selecttab/closetab) are never prefixed.
let TAB_PREFIX = '';

function usageAndExit() {
  console.error(
    'usage: chromesendkeys.js --dir <spool> ' +
      '<add|type|key|click|rightclick|goto|screenshot|eval|evalasync|getdom|' +
      'http|waitfor|netlog|newtab|newwindow|closetab|selecttab|listtabs|send|' +
      'push> [args...]',
  );
  process.exit(1);
}

function parseArgs(argv) {
  let dir = process.env.CHROMIUM_SENDKEYS_DIR || null;
  let tab = null;
  const rest = [];
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === '--dir') {
      dir = argv[++i];
    } else if (argv[i] === '--tab') {
      tab = argv[++i];
    } else {
      rest.push(argv[i]);
    }
  }
  if (!dir) usageAndExit();
  return { dir, tab, rest };
}

function stagingPath(dir) {
  return path.join(dir, '.chromium-sendkeys-staging');
}

function appendLine(dir, line) {
  fs.appendFileSync(stagingPath(dir), (TAB_PREFIX ? TAB_PREFIX + line : line) + '\n');
}

function push(dir) {
  const staging = stagingPath(dir);
  if (!fs.existsSync(staging)) {
    return; // nothing staged
  }
  const dest = path.join(
    dir,
    `${Date.now()}-${process.pid}-${Math.random().toString(36).slice(2)}.txt`,
  );
  fs.renameSync(staging, dest); // atomic publish
}

function randomId() {
  return `${Date.now()}-${process.pid}-${Math.random().toString(36).slice(2)}`;
}

// Polls <dir>/results/<id>.json until it appears (or timeoutMs elapses),
// then reads+deletes it and returns the parsed JSON. The browser side never
// deletes result files itself, so the reader owns cleanup.
function waitForResult(dir, id, timeoutMs) {
  const resultPath = path.join(dir, 'results', `${id}.json`);
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (fs.existsSync(resultPath)) {
      const contents = fs.readFileSync(resultPath, 'utf8');
      fs.unlinkSync(resultPath);
      return JSON.parse(contents);
    }
    // Busy-wait with a short sleep; this is a CLI, not the watcher itself,
    // so a blocking sleep here is fine.
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 50);
  }
  throw new Error(`timed out waiting for result ${resultPath}`);
}

// buildLine(id) must return the full spool-file line, with `id` embedded in
// the right position for that command's protocol (EVAL:<id>|<js>,
// WAITFOR:<timeout_ms>|<id>|<js> -- id position differs per command, hence
// the callback rather than fixed string concatenation).
function sendAndAwait(dir, buildLine, timeoutMs) {
  const id = randomId();
  appendLine(dir, buildLine(id));
  push(dir);
  return waitForResult(dir, id, timeoutMs || 10000);
}

function main() {
  const { dir, tab, rest } = parseArgs(process.argv.slice(2));
  TAB_PREFIX = tab ? `TAB:${tab}|` : '';
  const [cmd, ...args] = rest;
  if (!cmd) usageAndExit();

  switch (cmd) {
    // `add` is the only stage-only verb (for building a multi-line batch with
    // an explicit `push`). Every other action verb auto-pushes so a single
    // `chromesendkeys <verb>` call takes effect immediately, rather than
    // silently waiting for a later push to flush it.
    case 'add':
      appendLine(dir, args.join(' '));
      break;
    case 'type':
      appendLine(dir, `TEXT:${args.join(' ')}`);
      push(dir);
      break;
    case 'key':
      appendLine(dir, `KEY:${args.join(' ')}`);
      push(dir);
      break;
    case 'click':
      appendLine(dir, `CLICK:${args[0]},${args[1]}`);
      push(dir);
      break;
    case 'rightclick':
      appendLine(dir, `RIGHTCLICK:${args[0]},${args[1]}`);
      push(dir);
      break;
    case 'goto':
      appendLine(dir, `GOTO:${args.join(' ')}`);
      push(dir);
      break;
    case 'screenshot': {
      // Two-way: the fork writes the PNG AND acks results/<id>.json with
      // {ok,path,bytes} (or {ok:false,error}) -- no more blind poll for a file
      // that may never appear.
      const p = args.join(' ');
      const result = sendAndAwait(dir, (id) => `SCREENSHOT:${id}|${p}`);
      console.log(JSON.stringify(result));
      break;
    }
    case 'eval': {
      const js = args.join(' ');
      const result = sendAndAwait(dir, (id) => `EVAL:${id}|${js}`);
      console.log(JSON.stringify(result));
      break;
    }
    case 'evalasync': {
      // Runs the body as an async function body (may `await`/`return`) inside
      // the fork and acks {ok:true,value} / {ok:false,error}. Replaces the old
      // client-side "stash result on window[token] then poll" dance.
      const body = args[0] || '';
      const tmoS = Number(args[1] || 20);
      const result = sendAndAwait(
        dir,
        (id) => `EVALASYNC:${id}|${body}`,
        tmoS * 1000 + 5000,
      );
      console.log(JSON.stringify(result));
      break;
    }
    case 'getdom': {
      const js = 'document.documentElement.outerHTML';
      const result = sendAndAwait(dir, (id) => `EVAL:${id}|${js}`);
      console.log(result.ok ? result.value : JSON.stringify(result));
      break;
    }
    case 'http': {
      const [url, method] = args;
      // Synchronous XHR, not fetch(): the in-browser EVAL runs with
      // resolve_promises=false (the public ExecuteJavaScriptForTests default),
      // so an async fetch()'s Promise serializes to {}. A synchronous XHR
      // returns a plain object in the same turn, which round-trips correctly.
      const js =
        `(function(){var x=new XMLHttpRequest();` +
        `x.open(${JSON.stringify(method || 'GET')},${JSON.stringify(url)},false);` +
        `x.send();` +
        `return {status:x.status, body:x.responseText};})()`;
      const result = sendAndAwait(dir, (id) => `EVAL:${id}|${js}`);
      console.log(JSON.stringify(result));
      break;
    }
    case 'waitfor': {
      const [timeoutMsArg, ...jsParts] = args;
      const js = jsParts.join(' ');
      const result = sendAndAwait(
        dir,
        (id) => `WAITFOR:${timeoutMsArg}|${id}|${js}`,
        Number(timeoutMsArg) + 5000,
      );
      console.log(JSON.stringify(result));
      break;
    }
    case 'newtab': {
      // Two-way: the fork opens the tab AND acks results/<id>.json with
      // {ok:true,tabId} so the caller can pin later commands to it via --tab.
      const url = args.join(' ');
      const result = sendAndAwait(dir, (id) => `NEWTAB:${id}|${url}`);
      console.log(JSON.stringify(result));
      break;
    }
    case 'newwindow':
      appendLine(dir, `NEWWINDOW:${args.join(' ')}`);
      push(dir);
      break;
    case 'closetab':
      // index optional; empty -> active tab
      appendLine(dir, `CLOSETAB:${args[0] || ''}`);
      push(dir);
      break;
    case 'selecttab':
      appendLine(dir, `SELECTTAB:${args[0] || ''}`);
      push(dir);
      break;
    case 'listtabs': {
      const result = sendAndAwait(dir, (id) => `LISTTABS:${id}`);
      console.log(JSON.stringify(result));
      break;
    }
    case 'netlog': {
      const [sub, netlogPath] = args;
      if (sub === 'start') {
        appendLine(dir, 'NETLOG:START');
        push(dir);
      } else if (sub === 'stop') {
        appendLine(dir, `NETLOG:STOP:${netlogPath}`);
        push(dir);
      } else {
        usageAndExit();
      }
      break;
    }
    case 'send':
      appendLine(dir, args.join(' '));
      push(dir);
      break;
    case 'push':
      push(dir);
      break;
    default:
      usageAndExit();
  }
}

main();
