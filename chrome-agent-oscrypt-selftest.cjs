#!/usr/bin/env node
// chrome-agent-oscrypt-selftest — regression test for the portable, keychain-free
// cookie-encryption key ($CHROMIUM_SAFE_STORAGE_KEY).
//
//   node chrome-agent-oscrypt-selftest.cjs [path/to/Chromium]
//
// Proves the OSCrypt patch in components/os_crypt/common/keychain_password_mac.mm:
// a persistent cookie written under key A survives into a COPIED profile when
// reopened with key A (this is the rclone/S3 roam scenario), and is UNREADABLE
// when reopened with a different key B (the env var truly gates decryption, so
// the OS Keychain is never involved). Runs entirely headless, so it also
// exercises --headless=new. Needs a built out/Default carrying the patch.
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawn } = require('child_process');

const REPO = __dirname;
const BIN = process.argv[2] ||
  path.join(REPO, 'out/Default/Chromium.app/Contents/MacOS/Chromium');
if (!fs.existsSync(BIN)) { console.error('binary not found:', BIN); process.exit(2); }

const KEY_A = Buffer.from('roam-key-alpha-0123456789abcdef').toString('base64');
const KEY_B = Buffer.from('roam-key-BRAVO-9876543210zyxwvut').toString('base64');
const COOKIE = 'agentroam=hello123';

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'oscrypt-roam-'));
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
async function wait(spool, id, ms = 12000) {
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
// open example.com in a fresh tab and return its uuid (evals get pinned to it)
async function openExample(spool, tag) {
  send(spool, `NEWTAB:${tag}|https://example.com/`);
  const nt = await wait(spool, tag);
  await sleep(2500); // let it commit
  return nt && nt.tabId;
}
function launch(profile, spool, key) {
  return spawn(BIN, [
    `--user-data-dir=${profile}`, '--headless=new', '--no-first-run',
    '--no-default-browser-check', '--disable-session-crashed-bubble',
    'https://example.com/',
  ], { env: { ...process.env, CHROMIUM_SENDKEYS_DIR: spool, CHROMIUM_SAFE_STORAGE_KEY: key }, stdio: 'ignore' });
}
async function stop(child) {
  // graceful (SIGTERM) so the cookie store flushes to disk
  try { process.kill(child.pid, 'SIGTERM'); } catch (e) {}
  for (let i = 0; i < 40 && child.exitCode === null && child.signalCode === null; i++) await sleep(100);
  try { process.kill(child.pid, 'SIGKILL'); } catch (e) {}
  await sleep(300);
}

(async () => {
  const results = [];
  const ok = (n, c, d) => { results.push([n, c]); console.log(`${c ? 'PASS' : 'FAIL'}  ${n}${c ? '' : '  <- ' + (d || '')}`); };
  try {
    const P1 = path.join(tmp, 'p1');
    const s1 = mkSpool('s1');

    // 1) write a persistent cookie under KEY_A
    let child = launch(P1, s1, KEY_A);
    ok('boot #1 headless (write, key A)', await boot(s1));
    let tab = await openExample(s1, 'nt1');
    send(s1, `TAB:${tab}|EVALASYNC:w1|document.cookie=${JSON.stringify(COOKIE + '; expires=Fri, 31 Dec 2027 23:59:59 GMT; path=/')};return document.cookie;`);
    const w = await wait(s1, 'w1');
    ok('cookie set in live session', w.ok && /hello123/.test(w.value || ''), JSON.stringify(w));
    await sleep(4000); // let the cookie store commit to disk
    await stop(child);
    ok('Cookies DB written to profile', fs.existsSync(path.join(P1, 'Default', 'Cookies')));

    // 2) COPY the profile (simulates rclone pull on machine B) and reopen with SAME key
    const P2 = path.join(tmp, 'p2');
    fs.cpSync(P1, P2, { recursive: true });
    const s2 = mkSpool('s2');
    child = launch(P2, s2, KEY_A);
    ok('boot #2 headless (roam, key A)', await boot(s2));
    tab = await openExample(s2, 'nt2');
    send(s2, `TAB:${tab}|EVALASYNC:r1|return document.cookie;`);
    const r1 = await wait(s2, 'r1');
    ok('SAME key -> synced profile decrypts cookie', r1.ok && /hello123/.test(r1.value || ''), JSON.stringify(r1));
    await stop(child);

    // 3) reopen a fresh copy with a DIFFERENT key -> cookie must NOT decrypt
    const P3 = path.join(tmp, 'p3');
    fs.cpSync(P1, P3, { recursive: true });
    const s3 = mkSpool('s3');
    child = launch(P3, s3, KEY_B);
    ok('boot #3 headless (wrong key B)', await boot(s3));
    tab = await openExample(s3, 'nt3');
    send(s3, `TAB:${tab}|EVALASYNC:r2|return document.cookie;`);
    const r2 = await wait(s3, 'r2');
    ok('WRONG key -> cookie is NOT readable (key truly gates)', r2.ok && !/hello123/.test(r2.value || ''), JSON.stringify(r2));
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
