#!/usr/bin/env node
// call-loop — Layer 3 of ADR 0002: the loop that connects the fork's EARS to its MOUTH.
//
// Layers 1 and 2 already shipped and are verified:
//   * MOUTH — AUDIOSTART:<port> -> ws://127.0.0.1:<port>/mic  (int16 mono 48k in as the fake mic)
//   * EARS  — TAPSTART:<port>   -> ws://127.0.0.1:<port>/tap  (int16 mono 48k out of the tab)
//   * BRAIN — voice-agent.cjs speak (ElevenLabs TTS) / listen (Scribe STT)
// What was missing is this: hear a participant finish a sentence, transcribe it, think, and
// answer into the call — without transcribing our own voice.
//
//   node call-loop.cjs run  --tap 39517 --mic 39518 [--brain "<cmd>"] [--voice muthu]
//   node call-loop.cjs selftest          # deterministic, no browser, no network
//
// HALF-DUPLEX v1 (ADR 0002): we never listen while we speak. Full duplex needs echo
// cancellation we do not have — the tap carries our OWN TTS too, so an ungated loop
// transcribes itself and answers itself. The `speaking` gate below is the whole defense.
//
// Zero npm deps (Node 18+).
'use strict';

const net = require('net');
const fs = require('fs');
const os = require('os');
const path = require('path');
const crypto = require('crypto');
const { execFileSync, spawnSync } = require('child_process');

const RATE = 48000;            // what /tap emits and /mic expects
const TTS_RATE = 24000;        // what voice-agent speak writes to .wav
const HERE = __dirname;

// ── VAD ────────────────────────────────────────────────────────────────────────
// Deliberately an RMS gate, not a neural VAD: it needs no model, no deps, and is
// debuggable from a number. Tuned for a meeting where the far side is the only
// speaker; a noisy room will need SPEECH_RMS raised.
const FRAME_MS = 20;
const FRAME_SAMPLES = (RATE * FRAME_MS) / 1000;      // 960 samples @20ms
const SPEECH_RMS = Number(process.env.VOICE_VAD_RMS || 0.02);
const SILENCE_MS_END = Number(process.env.VOICE_VAD_SILENCE_MS || 900);  // end-of-utterance
const MIN_UTTERANCE_MS = 400;    // ignore coughs/clicks
const MAX_UTTERANCE_MS = 30000;  // force a cut so one monologue can't stall the loop

function rms(buf) {
  if (!buf.length) return 0;
  let sum = 0;
  const n = buf.length >> 1;
  for (let i = 0; i + 1 < buf.length; i += 2) {
    const s = buf.readInt16LE(i) / 32768;
    sum += s * s;
  }
  return Math.sqrt(sum / n);
}

// Segments a PCM stream into utterances. Pure + synchronous so selftest can drive
// it with synthetic audio and assert exact boundaries.
class Vad {
  constructor(onUtterance) {
    this.onUtterance = onUtterance;
    this.inSpeech = false;
    this.silenceMs = 0;
    this.voicedMs = 0;
    this.buf = [];
    this.carry = Buffer.alloc(0);
  }
  push(chunk) {
    this.carry = this.carry.length ? Buffer.concat([this.carry, chunk]) : chunk;
    const frameBytes = FRAME_SAMPLES * 2;
    while (this.carry.length >= frameBytes) {
      const frame = this.carry.subarray(0, frameBytes);
      this.carry = this.carry.subarray(frameBytes);
      this._frame(frame);
    }
  }
  _frame(frame) {
    const voiced = rms(frame) >= SPEECH_RMS;
    if (voiced) {
      if (!this.inSpeech) { this.inSpeech = true; this.voicedMs = 0; this.buf = []; }
      this.silenceMs = 0;
      this.voicedMs += FRAME_MS;
      this.buf.push(frame);
    } else if (this.inSpeech) {
      this.silenceMs += FRAME_MS;
      this.buf.push(frame);           // keep trailing silence; Scribe likes the tail
      if (this.silenceMs >= SILENCE_MS_END) this._flush();
    }
    if (this.inSpeech && this.voicedMs >= MAX_UTTERANCE_MS) this._flush();
  }
  _flush() {
    const audio = Buffer.concat(this.buf);
    const voicedMs = this.voicedMs;
    this.inSpeech = false; this.buf = []; this.silenceMs = 0; this.voicedMs = 0;
    if (voicedMs >= MIN_UTTERANCE_MS) this.onUtterance(audio, voicedMs);
  }
  // Called when the far side goes quiet for good (stream close).
  end() { if (this.inSpeech) this._flush(); }
}

// ── resample 24k -> 48k ────────────────────────────────────────────────────────
// Linear interpolation. TTS output is 24k and /mic demands 48k; feeding 24k to a
// 48k sink plays everything an octave down and at double speed, which is the kind
// of bug that sounds like "the model is broken" rather than "the rate is wrong".
function upsample24to48(pcm24) {
  const inN = pcm24.length >> 1;
  const out = Buffer.alloc(inN * 2 * 2);
  for (let i = 0; i < inN; i++) {
    const a = pcm24.readInt16LE(i * 2);
    const b = i + 1 < inN ? pcm24.readInt16LE((i + 1) * 2) : a;
    out.writeInt16LE(a, i * 4);
    out.writeInt16LE((a + b) >> 1, i * 4 + 2);
  }
  return out;
}

function stripWavHeader(buf) {
  if (buf.length < 12 || buf.toString('ascii', 0, 4) !== 'RIFF') return { pcm: buf, rate: TTS_RATE };
  let off = 12, rate = TTS_RATE;
  while (off + 8 <= buf.length) {
    const id = buf.toString('ascii', off, off + 4);
    const size = buf.readUInt32LE(off + 4);
    if (id === 'fmt ') rate = buf.readUInt32LE(off + 12);
    if (id === 'data') return { pcm: buf.subarray(off + 8, off + 8 + size), rate };
    off += 8 + size + (size & 1);
  }
  return { pcm: buf.subarray(44), rate };
}

// ── minimal RFC6455 client (same shape as tap-capture, plus masked SEND) ───────
function wsConnect(port, pathname, { onBinary, onOpen, onClose } = {}) {
  const key = crypto.randomBytes(16).toString('base64');
  const sock = net.connect(port, '127.0.0.1', () => {
    sock.write(
      `GET ${pathname} HTTP/1.1\r\nHost: 127.0.0.1:${port}\r\nUpgrade: websocket\r\n` +
      `Connection: Upgrade\r\nSec-WebSocket-Key: ${key}\r\nSec-WebSocket-Version: 13\r\n\r\n`,
    );
  });
  let handshook = false;
  let buf = Buffer.alloc(0);
  sock.on('data', (chunk) => {
    buf = Buffer.concat([buf, chunk]);
    if (!handshook) {
      const i = buf.indexOf('\r\n\r\n');
      if (i === -1) return;
      handshook = true;
      buf = buf.subarray(i + 4);
      onOpen && onOpen();
    }
    while (buf.length >= 2) {
      const opcode = buf[0] & 0x0f;
      const masked = (buf[1] & 0x80) !== 0;
      let len = buf[1] & 0x7f, off = 2;
      if (len === 126) { if (buf.length < 4) return; len = buf.readUInt16BE(2); off = 4; }
      else if (len === 127) { if (buf.length < 10) return; len = Number(buf.readBigUInt64BE(2)); off = 10; }
      if (masked) off += 4;
      if (buf.length < off + len) return;
      const payload = buf.subarray(off, off + len);
      if (opcode === 0x2 || opcode === 0x0) onBinary && onBinary(Buffer.from(payload));
      else if (opcode === 0x8) { try { sock.destroy(); } catch (_) {} onClose && onClose(); return; }
      buf = buf.subarray(off + len);
    }
  });
  sock.on('error', (e) => { console.error(`ws ${pathname} error:`, e.message); onClose && onClose(); });

  // client->server MUST be masked (RFC6455 §5.3); an unmasked frame is a protocol
  // error and the server closes the connection.
  function send(payload) {
    const mask = crypto.randomBytes(4);
    const n = payload.length;
    let header;
    if (n < 126) { header = Buffer.alloc(2); header[1] = 0x80 | n; }
    else if (n < 65536) { header = Buffer.alloc(4); header[1] = 0x80 | 126; header.writeUInt16BE(n, 2); }
    else { header = Buffer.alloc(10); header[1] = 0x80 | 127; header.writeBigUInt64BE(BigInt(n), 2); }
    header[0] = 0x82; // FIN + binary
    const masked = Buffer.alloc(n);
    for (let i = 0; i < n; i++) masked[i] = payload[i] ^ mask[i & 3];
    sock.write(Buffer.concat([header, mask, masked]));
  }
  return { sock, send, close: () => { try { sock.destroy(); } catch (_) {} } };
}

// ── fork spool ─────────────────────────────────────────────────────────────────
function spool(cmd) {
  const dir = process.env.CHROMIUM_SENDKEYS_DIR;
  if (!dir) throw new Error('CHROMIUM_SENDKEYS_DIR is not set — the fork watcher reads commands from there');
  const f = path.join(dir, `call-loop-${Date.now()}-${Math.floor(Math.random() * 1e6)}.txt`);
  fs.writeFileSync(f, cmd + '\n');
  return f;
}

// ── brain + voice ──────────────────────────────────────────────────────────────
function transcribe(wavPath, flags) {
  const args = ['voice-agent.cjs', 'listen', wavPath];
  if (flags.lang) args.push('--lang', flags.lang);
  const r = spawnSync('node', args, { cwd: HERE, encoding: 'utf8', timeout: 120000 });
  if (r.status !== 0) throw new Error(`listen failed: ${(r.stderr || '').trim().slice(0, 200)}`);
  return (r.stdout || '').trim();
}

function synthesize(text, outWav, flags) {
  const args = ['voice-agent.cjs', 'speak', text, outWav];
  if (flags.voice) args.push('--voice', flags.voice);
  const r = spawnSync('node', args, { cwd: HERE, encoding: 'utf8', timeout: 120000 });
  if (r.status !== 0) throw new Error(`speak failed: ${(r.stderr || '').trim().slice(0, 200)}`);
  return outWav;
}

// The brain is deliberately a SUBPROCESS, not an imported model client: it keeps this
// file free of any provider SDK, and lets the caller swap in `claude -p`, a scripted
// reply, or a test double. It receives the transcript on stdin and returns the reply
// on stdout.
function think(brainCmd, transcript) {
  const r = spawnSync('/bin/sh', ['-c', brainCmd], {
    input: transcript, encoding: 'utf8', timeout: 180000,
  });
  if (r.status !== 0) throw new Error(`brain failed: ${(r.stderr || '').trim().slice(0, 200)}`);
  return (r.stdout || '').trim();
}

// ── the loop ───────────────────────────────────────────────────────────────────
async function run(flags) {
  const tapPort = Number(flags.tap);
  const micPort = Number(flags.mic);
  if (!tapPort || !micPort) throw new Error('need --tap <port> and --mic <port>');
  const brainCmd = flags.brain || 'cat';   // default: echo what was heard
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'call-loop-'));

  spool(`AUDIOSTART:${micPort}`);
  spool(`TAPSTART:${tapPort}`);
  await new Promise((r) => setTimeout(r, 600));   // let both servers bind

  const mic = wsConnect(micPort, '/mic');
  let speaking = false;      // THE GATE — see the half-duplex note at the top
  let turn = 0;

  const vad = new Vad((audio, voicedMs) => {
    if (speaking) return;    // belt and braces; frames are already dropped below
    turn += 1;
    const id = turn;
    const wav = path.join(tmp, `utt-${id}.wav`);
    fs.writeFileSync(wav, wrapWav(audio, RATE));
    console.log(JSON.stringify({ event: 'utterance', turn: id, ms: voicedMs, wav }));

    let text = '';
    try { text = transcribe(wav, flags); } catch (e) {
      console.error(JSON.stringify({ event: 'stt-error', turn: id, error: e.message })); return;
    }
    if (!text) { console.log(JSON.stringify({ event: 'empty-transcript', turn: id })); return; }
    console.log(JSON.stringify({ event: 'heard', turn: id, text }));

    let reply = '';
    try { reply = think(brainCmd, text); } catch (e) {
      console.error(JSON.stringify({ event: 'brain-error', turn: id, error: e.message })); return;
    }
    if (!reply) { console.log(JSON.stringify({ event: 'no-reply', turn: id })); return; }
    console.log(JSON.stringify({ event: 'replying', turn: id, text: reply }));

    const outWav = path.join(tmp, `reply-${id}.wav`);
    try { synthesize(reply, outWav, flags); } catch (e) {
      console.error(JSON.stringify({ event: 'tts-error', turn: id, error: e.message })); return;
    }

    const { pcm, rate } = stripWavHeader(fs.readFileSync(outWav));
    const pcm48 = rate === RATE ? pcm : upsample24to48(pcm);

    // Speak: gate STT for the whole utterance plus a tail, so the trailing echo of
    // our own voice in the tap does not open a new utterance.
    speaking = true;
    const CHUNK = FRAME_SAMPLES * 2;
    let off = 0;
    const pump = setInterval(() => {
      if (off >= pcm48.length) {
        clearInterval(pump);
        const tailMs = SILENCE_MS_END + 300;
        setTimeout(() => {
          speaking = false;
          console.log(JSON.stringify({ event: 'spoke', turn: id, ms: Math.round((pcm48.length / 2 / RATE) * 1000) }));
        }, tailMs);
        return;
      }
      mic.send(pcm48.subarray(off, Math.min(off + CHUNK, pcm48.length)));
      off += CHUNK;
    }, FRAME_MS);
  });

  wsConnect(tapPort, '/tap', {
    onOpen: () => console.log(JSON.stringify({ event: 'listening', tap: tapPort, mic: micPort })),
    onBinary: (chunk) => { if (!speaking) vad.push(chunk); },  // the gate
    onClose: () => { vad.end(); console.log(JSON.stringify({ event: 'tap-closed' })); },
  });

  process.on('SIGINT', () => {
    try { spool('TAPSTOP'); } catch (_) {}
    console.log(JSON.stringify({ event: 'stopped', turns: turn }));
    process.exit(0);
  });
}

function wrapWav(data, rate, channels = 1, bits = 16) {
  const blockAlign = (channels * bits) >> 3;
  const h = Buffer.alloc(44);
  h.write('RIFF', 0); h.writeUInt32LE(36 + data.length, 4); h.write('WAVE', 8);
  h.write('fmt ', 12); h.writeUInt32LE(16, 16); h.writeUInt16LE(1, 20);
  h.writeUInt16LE(channels, 22); h.writeUInt32LE(rate, 24);
  h.writeUInt32LE(rate * blockAlign, 28); h.writeUInt16LE(blockAlign, 32);
  h.writeUInt16LE(bits, 34); h.write('data', 36); h.writeUInt32LE(data.length, 40);
  return Buffer.concat([h, data]);
}

// ── selftest: deterministic, no browser, no network, no API key ────────────────
// Proves the parts that are ours (segmentation, gating, resampling, WS framing).
// It cannot prove Teams works — only a live call does that — and says so.
function tone(ms, freq = 440, amp = 0.3, rate = RATE) {
  const n = Math.round((rate * ms) / 1000);
  const b = Buffer.alloc(n * 2);
  for (let i = 0; i < n; i++) b.writeInt16LE(Math.round(Math.sin((2 * Math.PI * freq * i) / rate) * amp * 32767), i * 2);
  return b;
}
function silence(ms, rate = RATE) { return Buffer.alloc(Math.round((rate * ms) / 1000) * 2); }

function selftest() {
  let pass = 0, fail = 0;
  const ok = (c, m) => { if (c) { console.log('PASS  ' + m); pass++; } else { console.log('FAIL  ' + m); fail++; } };

  // 1) two utterances separated by a real gap -> exactly two segments
  let segs = [];
  let v = new Vad((a, ms) => segs.push(ms));
  v.push(tone(1000)); v.push(silence(1200)); v.push(tone(800)); v.push(silence(1200)); v.end();
  ok(segs.length === 2, `two utterances -> two segments (got ${segs.length})`);
  ok(segs[0] >= 900 && segs[0] <= 1100, `first utterance ~1000ms (got ${segs[0]})`);

  // 2) a short gap must NOT split a sentence
  segs = []; v = new Vad((a, ms) => segs.push(ms));
  v.push(tone(600)); v.push(silence(300)); v.push(tone(600)); v.push(silence(1200)); v.end();
  ok(segs.length === 1, `300ms pause does not split a sentence (got ${segs.length})`);

  // 3) a click below MIN_UTTERANCE_MS is discarded
  segs = []; v = new Vad((a, ms) => segs.push(ms));
  v.push(tone(120)); v.push(silence(1200)); v.end();
  ok(segs.length === 0, `120ms click discarded (got ${segs.length})`);

  // 4) silence alone never produces an utterance — the control. If this ever passes
  //    an utterance through, every "we heard something" result is meaningless.
  segs = []; v = new Vad((a, ms) => segs.push(ms));
  v.push(silence(3000)); v.end();
  ok(segs.length === 0, `pure silence yields nothing (control) (got ${segs.length})`);

  // 5) resample doubles the sample count and preserves amplitude
  const s24 = tone(100, 440, 0.3, TTS_RATE);
  const s48 = upsample24to48(s24);
  ok(s48.length === s24.length * 2, `24k->48k doubles length (${s24.length} -> ${s48.length})`);
  const r24 = rms(s24), r48 = rms(s48);
  ok(Math.abs(r24 - r48) < 0.02, `resample preserves RMS (${r24.toFixed(3)} vs ${r48.toFixed(3)})`);

  // 6) WAV header round-trips
  const wav = wrapWav(tone(50), RATE);
  const { pcm, rate } = stripWavHeader(wav);
  ok(rate === RATE && pcm.length === tone(50).length, `wav header round-trips (rate ${rate})`);

  console.log(`\n=== ${pass}/${pass + fail} passed ===`);
  console.log('NOTE: this proves segmentation, gating math, resampling and framing.');
  console.log('It does NOT prove a live Teams call — only joining one does that.');
  process.exit(fail ? 1 : 0);
}

const [cmd, ...rest] = process.argv.slice(2);
const flags = {};
for (let i = 0; i < rest.length; i++) {
  if (rest[i].startsWith('--')) { flags[rest[i].slice(2)] = rest[i + 1]; i++; }
}
(async () => {
  try {
    if (cmd === 'selftest') return selftest();
    if (cmd === 'run') return await run(flags);
    console.error('usage: call-loop.cjs run --tap <port> --mic <port> [--brain "<cmd>"] [--voice <name>]');
    console.error('       call-loop.cjs selftest');
    process.exit(2);
  } catch (e) { console.error(e.message); process.exit(1); }
})();
