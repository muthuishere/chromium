#!/usr/bin/env node
// chrome-agent-tap-selftest — regression test for the audio-IN tap bridge
// (TAPSTART/TAPSTOP -> ws://127.0.0.1:<port>/tap, media::AgentAudioTapBridge).
//
//   node chrome-agent-tap-selftest.cjs [path/to/Chromium]
//
// Proves the /tap path built in this fork: a tab renders a known 440Hz sine to
// the audio OUTPUT; the same tab opens the /tap WebSocket, accumulates the
// int16 mono PCM the browser streams back, and computes its RMS. If the tap is
// wired correctly the received RMS is close to the tone's (~0.21 for gain 0.3);
// with the tap OFF (TAPSTOP) essentially no samples arrive. Runs headless.
// Needs a built out/Default carrying the /tap patch.
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawn } = require('child_process');

const REPO = __dirname;
const BIN = process.argv[2] ||
  path.join(REPO, 'out/Default/Chromium.app/Contents/MacOS/Chromium');
if (!fs.existsSync(BIN)) { console.error('binary not found:', BIN); process.exit(2); }

const PORT = 39517;
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'tap-test-'));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let seq = 0;

function mkSpool(name) {
  const s = path.join(tmp, name);
  fs.mkdirSync(path.join(s, 'results'), { recursive: true });
  return s;
}
function send(spool, line) {
  fs.writeFileSync(path.join(spool, `c-${Date.now()}-${seq++}.txt`), line + '\n');
}
async function wait(spool, id, ms = 15000) {
  const p = path.join(spool, 'results', id + '.json');
  const dl = Date.now() + ms;
  while (Date.now() < dl) {
    if (fs.existsSync(p)) { const t = fs.readFileSync(p, 'utf8'); fs.rmSync(p, { force: true }); return JSON.parse(t); }
    await sleep(80);
  }
  return { __timeout: true };
}
async function boot(spool) {
  for (let i = 0; i < 100; i++) { send(spool, `LISTTABS:b${i}`); const r = await wait(spool, 'b' + i, 800); if (r && r.ok) return true; }
  return false;
}
async function openBlank(spool, tag) {
  send(spool, `NEWTAB:${tag}|about:blank`);
  const nt = await wait(spool, tag);
  await sleep(1500);
  return nt && nt.tabId;
}
function launch(profile, spool) {
  return spawn(BIN, [
    `--user-data-dir=${profile}`, '--headless=new', '--no-first-run',
    '--no-default-browser-check', '--disable-session-crashed-bubble',
    '--autoplay-policy=no-user-gesture-required',
    'about:blank',
  ], { env: { ...process.env, CHROMIUM_SENDKEYS_DIR: spool }, stdio: 'ignore' });
}
async function stop(child) {
  try { process.kill(child.pid, 'SIGTERM'); } catch (e) {}
  for (let i = 0; i < 40 && child.exitCode === null && child.signalCode === null; i++) await sleep(100);
  try { process.kill(child.pid, 'SIGKILL'); } catch (e) {}
  await sleep(300);
}

// Runs in the page: play a 440Hz tone to the output, connect to /tap, gather
// int16 mono samples for `ms`, return {rms, count}. Escaped as a b64 EVALASYNC.
function measureBody(port, ms, playTone) {
  // A bare async IIFE EXPRESSION: the caller does `return eval(atob(...))`, so
  // this must evaluate to a Promise, not contain a top-level `return`.
  return `
    (async () => {
      const ctx = new (window.AudioContext||window.webkitAudioContext)();
      try { await ctx.resume(); } catch(e) {}
      let osc, gain;
      if (${playTone}) {
        osc = ctx.createOscillator(); gain = ctx.createGain();
        osc.frequency.value = 440; gain.gain.value = 0.3;
        osc.connect(gain).connect(ctx.destination); osc.start();
      }
      const samples = [];
      const ws = new WebSocket('ws://127.0.0.1:${port}/tap');
      ws.binaryType = 'arraybuffer';
      await new Promise((res) => { ws.onopen = res; ws.onerror = res; setTimeout(res, 3000); });
      ws.onmessage = (ev) => {
        const a = new Int16Array(ev.data);
        for (let i = 0; i < a.length; i++) samples.push(a[i] / 32768);
      };
      await new Promise((r) => setTimeout(r, ${ms}));
      try { ws.close(); } catch(e) {}
      if (osc) { try { osc.stop(); } catch(e){} }
      try { await ctx.close(); } catch(e) {}
      let sum = 0; for (const s of samples) sum += s*s;
      const rms = samples.length ? Math.sqrt(sum/samples.length) : 0;
      return { rms, count: samples.length };
    })()
  `;
}

(async () => {
  const results = [];
  const ok = (n, c, d) => { results.push([n, c]); console.log(`${c ? 'PASS' : 'FAIL'}  ${n}${c ? '' : '  <- ' + (d || '')}`); };
  try {
    const P = path.join(tmp, 'p');
    const s = mkSpool('s');
    const child = launch(P, s);
    ok('boot headless', await boot(s));
    const tab = await openBlank(s, 'nt1');
    ok('opened tab', !!tab, 'no tabId');

    // 1) arm the tap, play a tone, measure what /tap streams back
    send(s, `TAPSTART:${PORT}`);
    await sleep(1200); // let the WS server bind on the IO thread
    const b64on = Buffer.from(measureBody(PORT, 2500, true)).toString('base64');
    send(s, `TAB:${tab}|EVALASYNC:m1|return eval(atob('${b64on}'))`);
    const on = await wait(s, 'm1', 20000);
    console.log('   tap ON  ->', JSON.stringify(on.value ?? on));
    ok('tap streams PCM while armed (frames received)',
       on.ok && on.value && on.value.count > 2000, JSON.stringify(on));
    ok('tapped audio matches the 440Hz tone (RMS ~0.21)',
       on.ok && on.value && on.value.rms > 0.05, JSON.stringify(on));

    // 2) disarm the tap; the /tap socket should now receive ~nothing
    send(s, 'TAPSTOP');
    await sleep(800);
    const b64off = Buffer.from(measureBody(PORT, 1800, true)).toString('base64');
    send(s, `TAB:${tab}|EVALASYNC:m2|return eval(atob('${b64off}'))`);
    const off = await wait(s, 'm2', 20000);
    console.log('   tap OFF ->', JSON.stringify(off.value ?? off));
    // After TAPSTOP the server is gone: the WS won't open, so count stays ~0.
    ok('tap disarmed -> no PCM streamed',
       (!off.ok) || !off.value || off.value.count < 200, JSON.stringify(off));

    await stop(child);
    const failed = results.filter((r) => !r[1]);
    console.log(`\n=== ${results.length - failed.length}/${results.length} passed ===`);
    fs.rmSync(tmp, { recursive: true, force: true });
    process.exit(failed.length ? 1 : 0);
  } catch (e) {
    console.error('test error:', e && e.message);
    fs.rmSync(tmp, { recursive: true, force: true });
    process.exit(2);
  }
})();
