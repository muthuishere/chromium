#!/usr/bin/env node
// chrome-agent-selftest — integration regression test for the fork's agent
// spool protocol. Launches a throwaway instance of out/Default Chromium with its
// own profile + spool, exercises the tabId registry, eval-await, and the
// screenshot ack (incl. BACKGROUND-tab capture), asserts each, and exits
// non-zero on any failure.
//
//   node chrome-agent-selftest.cjs [path/to/Chromium]
//
// These are the behaviours verified by hand when the features shipped; keeping
// them here makes that verification repeatable. Needs a built out/Default.
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawn, execSync } = require('child_process');

const REPO = __dirname;
const BIN = process.argv[2] ||
  path.join(REPO, 'out/Default/Chromium.app/Contents/MacOS/Chromium');
if (!fs.existsSync(BIN)) { console.error('binary not found:', BIN); process.exit(2); }

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'ca-selftest-'));
const PROFILE = path.join(tmp, 'profile');
const SPOOL = path.join(tmp, 'spool');
const RES = path.join(SPOOL, 'results');
const OUTDIR = path.join(tmp, 'out'); // screenshots go OUTSIDE the spool dir
fs.mkdirSync(RES, { recursive: true });
fs.mkdirSync(OUTDIR, { recursive: true });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let seq = 0;
const results = [];
function ok(name, cond, detail) {
  results.push({ name, pass: !!cond, detail });
  console.log(`${cond ? 'PASS' : 'FAIL'}  ${name}${cond ? '' : '  <- ' + (detail || '')}`);
}
function send(line) {
  fs.writeFileSync(path.join(SPOOL, `c-${Date.now()}-${seq++}.txt`), line + '\n');
}
async function wait(id, ms = 12000) {
  const p = path.join(RES, id + '.json');
  const dl = Date.now() + ms;
  while (Date.now() < dl) {
    if (fs.existsSync(p)) { const t = fs.readFileSync(p, 'utf8'); fs.rmSync(p, { force: true }); return JSON.parse(t); }
    await sleep(80);
  }
  return { __timeout: true };
}
async function evalOn(tabId, js, id) {
  id = id || 'e' + seq;
  send((tabId ? `TAB:${tabId}|` : '') + `EVAL:${id}|${js}`);
  return wait(id);
}
function validPng(file) {
  if (!fs.existsSync(file)) return false;
  const b = fs.readFileSync(file);
  return b.length > 1000 && b[0] === 0x89 && b[1] === 0x50 && b[2] === 0x4e && b[3] === 0x47;
}

const child = spawn(BIN, [
  `--user-data-dir=${PROFILE}`, '--no-first-run', '--no-default-browser-check',
  '--disable-session-crashed-bubble', '--hide-crash-restore-bubble',
  '--enable-logging=stderr', '--v=0', 'https://example.com/',
], { env: { ...process.env, CHROMIUM_SENDKEYS_DIR: SPOOL }, stdio: 'ignore', detached: false });

function teardown() {
  try { process.kill(child.pid, 'SIGKILL'); } catch (e) {}
  try { fs.rmSync(tmp, { recursive: true, force: true }); } catch (e) {}
}

(async () => {
  try {
    // wait for the watcher to answer
    let booted = false;
    for (let i = 0; i < 80; i++) { send(`LISTTABS:boot${i}`); const r = await wait('boot' + i, 1000); if (r && r.ok) { booted = true; break; } }
    ok('browser boots + spool watcher answers LISTTABS', booted);
    if (!booted) throw new Error('no boot');

    // T1 NEWTAB acks a uuid
    send('NEWTAB:nt1|https://www.iana.org/help/example-domains');
    const nt = await wait('nt1');
    const tabId = nt && nt.tabId;
    ok('NEWTAB acks {ok,tabId:uuid}', nt.ok && /^[0-9a-f-]{36}$/.test(tabId || ''), JSON.stringify(nt));
    await sleep(2500); // let the new tab commit

    // T2 TAB pins to the right tab
    const r2 = await evalOn(tabId, 'location.href');
    ok('TAB:<uuid>| pins EVAL to that tab', r2.ok && /iana\.org/.test(r2.value || ''), JSON.stringify(r2));

    // T3 focus-independence: activate tab 0, pinned eval still hits the new tab
    send('SELECTTAB:0'); await sleep(600);
    const active = await evalOn(null, 'location.href');
    const r3 = await evalOn(tabId, 'location.href');
    ok('pinning is focus-independent (race fix)',
      /example\.com/.test(active.value || '') && r3.ok && /iana\.org/.test(r3.value || ''),
      `active=${active.value} pinned=${r3.value}`);

    // T4 unknown tabId hard-errors, never falls back
    const r4 = await evalOn('deadbeef-0000-0000-0000-not-real-abcd', '1+1');
    ok('unknown tabId -> {ok:false,error:"unknown tabId"}', r4.ok === false && r4.error === 'unknown tabId', JSON.stringify(r4));

    // T5 LISTTABS reports tabId across windows
    send('LISTTABS:lt'); const lt = await wait('lt');
    ok('LISTTABS reports tabId per tab', lt.ok && Array.isArray(lt.value) && lt.value.every(t => 'tabId' in t), JSON.stringify(lt.value && lt.value[0]));

    // T6 EVALASYNC awaits a real promise / timer / surfaces throws
    send('EVALASYNC:a1|const r=await fetch("https://example.com/");return (await r.text()).length;');
    const a1 = await wait('a1');
    ok('EVALASYNC awaits fetch -> resolved value', a1.ok && typeof a1.value === 'number' && a1.value > 0, JSON.stringify(a1));
    send('EVALASYNC:a2|await new Promise(r=>setTimeout(r,400));return 40+2;');
    const a2 = await wait('a2');
    ok('EVALASYNC truly awaits a timer', a2.ok && a2.value === 42, JSON.stringify(a2));
    send('EVALASYNC:a3|throw new Error("boom-xyz");');
    const a3 = await wait('a3');
    ok('EVALASYNC surfaces thrown errors', a3.ok === false && /boom-xyz/.test(a3.error || ''), JSON.stringify(a3));

    // T7 SCREENSHOT of the FOREGROUND tab acks + writes a valid PNG (path OUTSIDE spool)
    send('SELECTTAB:0'); await sleep(400);
    const fg = path.join(OUTDIR, 'fg.png');
    send(`SCREENSHOT:s1|${fg}`);
    const s1 = await wait('s1', 15000); await sleep(300);
    ok('SCREENSHOT acks {ok,bytes} + valid PNG (foreground)', s1.ok === true && s1.bytes > 1000 && validPng(fg), JSON.stringify(s1));

    // T8 SCREENSHOT of a BACKGROUND tab (capturer-count path): the iana tab is not active
    const bg = path.join(OUTDIR, 'bg.png');
    send(`TAB:${tabId}|SCREENSHOT:s2|${bg}`);
    const s2 = await wait('s2', 15000); await sleep(300);
    ok('SCREENSHOT captures a BACKGROUND tab (no hang)', s2.ok === true && s2.bytes > 1000 && validPng(bg), JSON.stringify(s2));

    const failed = results.filter(r => !r.pass);
    console.log(`\n=== ${results.length - failed.length}/${results.length} passed ===`);
    teardown();
    process.exit(failed.length ? 1 : 0);
  } catch (e) {
    console.error('selftest error:', e && e.message);
    teardown();
    process.exit(2);
  }
})();
