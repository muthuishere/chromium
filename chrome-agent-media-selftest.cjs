#!/usr/bin/env node
// chrome-agent-media-selftest — integration test for the fork's MEDIA bridges:
// the fake microphone (AUDIOSTART/AUDIOSTOP + /mic WebSocket) and the fake
// camera (VIDEOSTART/VIDEOSTOP + /cam WebSocket). Launches a throwaway instance,
// injects real PCM/I420 over the localhost bridges, then asserts getUserMedia()
// actually receives non-silent audio / non-black video frames.
//
//   node chrome-agent-media-selftest.cjs [path/to/Chromium]
//
// Kept separate from chrome-agent-selftest.cjs: media capture is timing-sensitive
// and needs --use-fake-ui-for-media-stream to auto-grant the mic/camera prompt.
const fs = require('fs');
const os = require('os');
const net = require('net');
const path = require('path');
const crypto = require('crypto');
const { spawn } = require('child_process');

const REPO = __dirname;
const BIN = process.argv[2] ||
  path.join(REPO, 'out/Default/Chromium.app/Contents/MacOS/Chromium');
if (!fs.existsSync(BIN)) { console.error('binary not found:', BIN); process.exit(2); }

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'ca-media-'));
const PROFILE = path.join(tmp, 'profile');
const SPOOL = path.join(tmp, 'spool');
const RES = path.join(SPOOL, 'results');
fs.mkdirSync(RES, { recursive: true });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let seq = 0;
const results = [];
function ok(name, cond, detail) { results.push({ name, pass: !!cond }); console.log(`${cond ? 'PASS' : 'FAIL'}  ${name}${cond ? '' : '  <- ' + (detail || '')}`); }
function send(line) { fs.writeFileSync(path.join(SPOOL, `c-${Date.now()}-${seq++}.txt`), line + '\n'); }
async function wait(id, ms = 12000) {
  const p = path.join(RES, id + '.json'); const dl = Date.now() + ms;
  while (Date.now() < dl) { if (fs.existsSync(p)) { const t = fs.readFileSync(p, 'utf8'); fs.rmSync(p, { force: true }); return JSON.parse(t); } await sleep(80); }
  return { __timeout: true };
}
function evalAsync(body, id) { id = id || 'ea' + seq; send(`EVALASYNC:${id}|${body}`); return wait(id, 15000); }

// --- minimal WebSocket client (no deps): connect + send masked binary frames ---
function wsConnect(port, pathname) {
  return new Promise((resolve, reject) => {
    const sock = net.connect(port, '127.0.0.1', () => {
      const key = crypto.randomBytes(16).toString('base64');
      sock.write(`GET ${pathname} HTTP/1.1\r\nHost: 127.0.0.1:${port}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: ${key}\r\nSec-WebSocket-Version: 13\r\n\r\n`);
    });
    let buf = Buffer.alloc(0), up = false;
    sock.on('data', (d) => { if (up) return; buf = Buffer.concat([buf, d]); if (buf.includes('\r\n\r\n')) { const status = buf.toString('latin1').split('\r\n')[0]; if (/101/.test(status)) { up = true; resolve(sock); } else reject(new Error('handshake: ' + status)); } });
    sock.on('error', reject);
    setTimeout(() => reject(new Error('ws connect timeout')), 5000);
  });
}
function wsSendBinary(sock, payload) {
  const len = payload.length, mask = crypto.randomBytes(4);
  let header;
  if (len < 126) header = Buffer.from([0x82, 0x80 | len]);
  else if (len < 65536) header = Buffer.from([0x82, 0x80 | 126, (len >> 8) & 0xff, len & 0xff]);
  else { header = Buffer.alloc(10); header[0] = 0x82; header[1] = 0x80 | 127; header.writeUInt32BE(0, 2); header.writeUInt32BE(len, 6); }
  const masked = Buffer.alloc(len);
  for (let i = 0; i < len; i++) masked[i] = payload[i] ^ mask[i & 3];
  sock.write(Buffer.concat([header, mask, masked]));
}
// [int32 LE w][int32 LE h][I420] — solid colour (BT.601 red ~ Y76 U84 V255)
function i420Frame(w, h, Y, U, V) {
  const ys = w * h, cs = (w >> 1) * (h >> 1);
  const b = Buffer.alloc(8 + ys + 2 * cs);
  b.writeInt32LE(w, 0); b.writeInt32LE(h, 4);
  b.fill(Y, 8, 8 + ys); b.fill(U, 8 + ys, 8 + ys + cs); b.fill(V, 8 + ys + cs, 8 + ys + 2 * cs);
  return b;
}
function pcmSine(n, phase, freq = 440, rate = 48000, amp = 22000) {
  const b = Buffer.alloc(n * 2);
  for (let i = 0; i < n; i++) b.writeInt16LE(Math.round(amp * Math.sin(2 * Math.PI * freq * (phase + i) / rate)), i * 2);
  return b;
}

const child = spawn(BIN, [
  `--user-data-dir=${PROFILE}`, '--no-first-run', '--no-default-browser-check',
  '--disable-session-crashed-bubble', '--hide-crash-restore-bubble',
  '--use-fake-ui-for-media-stream', // auto-grant the mic/camera permission
  '--enable-logging=stderr', '--v=0', 'https://example.com/',
], { env: { ...process.env, CHROMIUM_SENDKEYS_DIR: SPOOL }, stdio: 'ignore' });
function teardown() { try { process.kill(child.pid, 'SIGKILL'); } catch (e) {} try { fs.rmSync(tmp, { recursive: true, force: true }); } catch (e) {} }

(async () => {
  try {
    let booted = false;
    for (let i = 0; i < 80; i++) { send(`LISTTABS:boot${i}`); const r = await wait('boot' + i, 1000); if (r && r.ok) { booted = true; break; } }
    ok('browser boots (media flags)', booted); if (!booted) throw new Error('no boot');

    // ---- CAMERA: VIDEOSTART -> push red I420 over /cam -> getUserMedia sees red ----
    const VPORT = 8123;
    send(`VIDEOSTART:${VPORT}`); await sleep(700);
    let vsock = null;
    try { vsock = await wsConnect(VPORT, '/cam'); } catch (e) { ok('camera /cam WebSocket accepts connection', false, e.message); }
    if (vsock) {
      ok('camera /cam WebSocket accepts connection', true);
      const frame = i420Frame(320, 240, 76, 84, 255); // red
      const push = setInterval(() => { try { wsSendBinary(vsock, frame); } catch (e) {} }, 100);
      await sleep(300);
      const v = await evalAsync(
        `const s=await navigator.mediaDevices.getUserMedia({video:true});` +
        `const t=s.getVideoTracks()[0];const p=new MediaStreamTrackProcessor({track:t});` +
        `const r=p.readable.getReader();let px=null;` +
        `for(let i=0;i<10;i++){const {value:f}=await r.read();` +
        `const c=new OffscreenCanvas(f.displayWidth,f.displayHeight);const x=c.getContext('2d');` +
        `x.drawImage(f,0,0);px=Array.from(x.getImageData(f.displayWidth>>1,f.displayHeight>>1,1,1).data);f.close();` +
        `if(px[0]+px[1]+px[2]>30)break;}` +
        `r.releaseLock();t.stop();return px;`);
      clearInterval(push);
      const px = v && v.value;
      const reddish = Array.isArray(px) && px[0] > 100 && px[1] < 130 && px[2] < 130;
      ok('camera: getUserMedia video shows injected RED frame', v.ok && reddish, JSON.stringify(v));
      try { vsock.destroy(); } catch (e) {}
    }
    send('VIDEOSTOP'); await sleep(300);

    // ---- MIC: AUDIOSTART -> stream PCM sine over /mic -> getUserMedia hears it ----
    const APORT = 8124;
    send(`AUDIOSTART:${APORT}`); await sleep(700);
    let asock = null;
    try { asock = await wsConnect(APORT, '/mic'); } catch (e) { ok('mic /mic WebSocket accepts connection', false, e.message); }
    if (asock) {
      ok('mic /mic WebSocket accepts connection', true);
      let phase = 0;
      const push = setInterval(() => { const n = 960; wsSendBinary(asock, pcmSine(n, phase)); phase += n; }, 20);
      const a = await evalAsync(
        `const s=await navigator.mediaDevices.getUserMedia({audio:true});` +
        `const ac=new AudioContext();const src=ac.createMediaStreamSource(s);` +
        `const an=ac.createAnalyser();an.fftSize=2048;src.connect(an);` +
        `const buf=new Float32Array(an.fftSize);let peak=0;` +
        `for(let i=0;i<50;i++){an.getFloatTimeDomainData(buf);let m=0;for(const v of buf)m=Math.max(m,Math.abs(v));peak=Math.max(peak,m);await new Promise(r=>setTimeout(r,20));}` +
        `s.getTracks().forEach(t=>t.stop());ac.close();return peak;`);
      clearInterval(push);
      ok('mic: getUserMedia audio hears injected PCM (peak>0.02)', a.ok && typeof a.value === 'number' && a.value > 0.02, JSON.stringify(a));
      try { asock.destroy(); } catch (e) {}
    }
    send('AUDIOSTOP'); await sleep(200);

    const failed = results.filter(r => !r.pass);
    console.log(`\n=== ${results.length - failed.length}/${results.length} passed ===`);
    teardown();
    process.exit(failed.length ? 1 : 0);
  } catch (e) { console.error('media-selftest error:', e && e.message); teardown(); process.exit(2); }
})();
