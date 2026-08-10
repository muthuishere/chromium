#!/usr/bin/env node
// call-loop-integration — end-to-end proof of ADR 0002 Layer 3 against the REAL fork.
//
// The unit selftest (`call-loop.cjs selftest`) proves segmentation/resampling math on
// synthetic buffers. It cannot tell you the loop works, because it never touches the
// browser. This does the whole chain for real:
//
//   voice-agent speak  ->  a tab plays that speech  ->  /tap streams it out of the fork
//   ->  call-loop's VAD cuts the utterance  ->  Scribe transcribes it  ->  brain replies
//
// PASS means the fork genuinely HEARD a spoken sentence and understood it.
//
//   node call-loop-integration.cjs [path/to/Chromium]
//
// Requires $ELEVENLABS_API_KEY (never printed) and a built out/Default carrying /tap.
'use strict';

const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawn, spawnSync } = require('child_process');

const HERE = __dirname;
const REPO = path.resolve(HERE, '../..');
const BIN = process.argv[2] || path.join(REPO, 'out/Default/Chromium.app/Contents/MacOS/Chromium');
const SENTENCE = 'The quick brown fox jumps over the lazy dog.';
const TAP_PORT = 39621;
const MIC_PORT = 39622;

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'call-loop-it-'));
const spoolDir = path.join(tmp, 'spool');
fs.mkdirSync(path.join(spoolDir, 'results'), { recursive: true });

let pass = 0, fail = 0;
const ok = (c, m) => { if (c) { console.log('PASS  ' + m); pass++; } else { console.log('FAIL  ' + m); fail++; } };
let seq = 0;
const send = (line) => fs.writeFileSync(path.join(spoolDir, `c-${Date.now()}-${seq++}.txt`), line + '\n');

async function waitResult(id, ms = 15000) {
  const p = path.join(spoolDir, 'results', id + '.json');
  const dl = Date.now() + ms;
  while (Date.now() < dl) {
    if (fs.existsSync(p)) { const t = fs.readFileSync(p, 'utf8'); fs.rmSync(p, { force: true }); return JSON.parse(t); }
    await sleep(80);
  }
  return { __timeout: true };
}

(async () => {
  if (!fs.existsSync(BIN)) { console.error('binary not found:', BIN); process.exit(2); }
  if (!process.env.ELEVENLABS_API_KEY) {
    console.error('ELEVENLABS_API_KEY not set — cannot run the real STT leg.');
    console.error('load it without echoing: export ELEVENLABS_API_KEY="$(zsh -ic \'echo $ELEVENLABS_API_KEY\' 2>/dev/null)"');
    process.exit(2);
  }

  // 1) Make the far side's voice.
  const speechWav = path.join(tmp, 'far-side.wav');
  const sp = spawnSync('node', ['voice-agent.cjs', 'speak', SENTENCE, speechWav],
    { cwd: HERE, encoding: 'utf8', timeout: 120000 });
  ok(sp.status === 0 && fs.existsSync(speechWav), 'synthesized the far-side sentence');
  if (sp.status !== 0) { console.error(sp.stderr); process.exit(1); }

  // 2) Boot the fork headless with our spool.
  const child = spawn(BIN, [
    `--user-data-dir=${path.join(tmp, 'profile')}`, '--headless=new', '--no-first-run',
    '--no-default-browser-check', '--disable-session-crashed-bubble',
    '--autoplay-policy=no-user-gesture-required', 'about:blank',
  ], { env: { ...process.env, CHROMIUM_SENDKEYS_DIR: spoolDir }, stdio: 'ignore' });

  let booted = false;
  for (let i = 0; i < 100; i++) { send(`LISTTABS:b${i}`); const r = await waitResult('b' + i, 800); if (r && r.ok) { booted = true; break; } }
  ok(booted, 'fork booted headless and is reading the spool');
  if (!booted) { try { process.kill(child.pid, 'SIGKILL'); } catch (_) {} process.exit(1); }

  // 3) Start the loop FIRST so it is listening before any audio plays.
  //    Brain is a fixed string: this test is about hearing, not about what we say back.
  const loop = spawn('node', ['call-loop.cjs', 'run', '--tap', String(TAP_PORT), '--mic', String(MIC_PORT),
    '--brain', "printf 'acknowledged'"], {
    cwd: HERE, env: { ...process.env, CHROMIUM_SENDKEYS_DIR: spoolDir }, stdio: ['ignore', 'pipe', 'pipe'],
  });
  const events = [];
  let raw = '';
  loop.stdout.on('data', (d) => {
    raw += d.toString();
    let i;
    while ((i = raw.indexOf('\n')) !== -1) {
      const line = raw.slice(0, i).trim(); raw = raw.slice(i + 1);
      if (!line) continue;
      try { events.push(JSON.parse(line)); console.log('   loop> ' + line); } catch (_) { console.log('   loop| ' + line); }
    }
  });
  loop.stderr.on('data', (d) => process.stderr.write('   loop! ' + d.toString()));

  for (let i = 0; i < 60 && !events.some((e) => e.event === 'listening'); i++) await sleep(250);
  ok(events.some((e) => e.event === 'listening'), 'call-loop armed /tap and /mic');

  // 4) Play the speech into a tab. file:// in an <audio> element; the fork taps the
  //    tab's rendered output, so this is exactly what a meeting participant sounds like.
  const pageHtml = `<!doctype html><audio id=a src="file://${speechWav}" autoplay></audio>`;
  const pagePath = path.join(tmp, 'play.html');
  fs.writeFileSync(pagePath, pageHtml);
  send(`NEWTAB:t1|file://${pagePath}`);
  const nt = await waitResult('t1');
  ok(nt && nt.tabId, 'opened a tab that plays the sentence');

  // 5) Wait for the loop to cut the utterance and transcribe it.
  //    Speech is ~3s; VAD needs 900ms of trailing silence; STT is a network call.
  const deadline = Date.now() + 75000;
  while (Date.now() < deadline && !events.some((e) => e.event === 'heard')) await sleep(400);

  const utt = events.find((e) => e.event === 'utterance');
  const heard = events.find((e) => e.event === 'heard');
  ok(!!utt, `VAD cut an utterance out of the live tap${utt ? ` (${utt.ms}ms)` : ''}`);
  ok(!!heard, 'the utterance was transcribed');

  if (heard) {
    const norm = (s) => s.toLowerCase().replace(/[^a-z ]/g, '').split(/\s+/).filter(Boolean);
    const want = norm(SENTENCE), got = norm(heard.text);
    const hits = want.filter((w) => got.includes(w)).length;
    const ratio = hits / want.length;
    ok(ratio >= 0.7, `transcript matches what was spoken (${hits}/${want.length} words: "${heard.text}")`);
  }

  // 6) CONTROL — with nothing playing, the loop must NOT invent an utterance.
  //    Without this, a VAD stuck open would "pass" every test above.
  const before = events.filter((e) => e.event === 'utterance').length;
  await sleep(6000);
  const after = events.filter((e) => e.event === 'utterance').length;
  ok(after === before, `control: silence produced no new utterance (${before} -> ${after})`);

  try { loop.kill('SIGKILL'); } catch (_) {}
  try { process.kill(child.pid, 'SIGKILL'); } catch (_) {}
  await sleep(400);

  console.log(`\n=== ${pass}/${pass + fail} passed ===`);
  if (!fail) console.log('The fork HEARD a spoken sentence off a live tab and understood it.');
  process.exit(fail ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
