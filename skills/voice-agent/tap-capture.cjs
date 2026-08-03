#!/usr/bin/env node
// tap-capture — record the fork's /tap audio-out stream to a WAV file.
// Minimal RFC6455 WebSocket client (zero deps) for ws://127.0.0.1:<port>/tap,
// which streams int16 mono 48kHz PCM (media::AgentAudioTapBridge). Writes a
// 16-bit mono 48k WAV you can feed to `voice-agent listen`.
//
//   node tap-capture.cjs <port> <seconds> <out.wav>
'use strict';
const net = require('net');
const crypto = require('crypto');
const fs = require('fs');

const port = parseInt(process.argv[2], 10);
const seconds = parseFloat(process.argv[3] || '5');
const out = process.argv[4];
if (!port || !out) {
  console.error('usage: tap-capture <port> <seconds> <out.wav>');
  process.exit(2);
}
const RATE = 48000;

const key = crypto.randomBytes(16).toString('base64');
const sock = net.connect(port, '127.0.0.1', () => {
  sock.write(
    `GET /tap HTTP/1.1\r\nHost: 127.0.0.1:${port}\r\nUpgrade: websocket\r\n` +
    `Connection: Upgrade\r\nSec-WebSocket-Key: ${key}\r\n` +
    `Sec-WebSocket-Version: 13\r\n\r\n`,
  );
});

let handshook = false;
let buf = Buffer.alloc(0);
const pcm = [];

function parseFrames() {
  // Server->client frames are unmasked.
  while (buf.length >= 2) {
    const opcode = buf[0] & 0x0f;
    const masked = (buf[1] & 0x80) !== 0;
    let len = buf[1] & 0x7f;
    let off = 2;
    if (len === 126) {
      if (buf.length < 4) return;
      len = buf.readUInt16BE(2); off = 4;
    } else if (len === 127) {
      if (buf.length < 10) return;
      len = Number(buf.readBigUInt64BE(2)); off = 10;
    }
    if (masked) off += 4; // shouldn't happen from server, but be safe
    if (buf.length < off + len) return;
    const payload = buf.subarray(off, off + len);
    if (opcode === 0x2 || opcode === 0x0) pcm.push(Buffer.from(payload));
    else if (opcode === 0x8) { finish(); return; } // close
    buf = buf.subarray(off + len);
  }
}

sock.on('data', (chunk) => {
  buf = Buffer.concat([buf, chunk]);
  if (!handshook) {
    const i = buf.indexOf('\r\n\r\n');
    if (i === -1) return;
    handshook = true;
    buf = buf.subarray(i + 4);
  }
  parseFrames();
});

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

let done = false;
function finish() {
  if (done) return;
  done = true;
  try { sock.destroy(); } catch (e) {}
  const data = Buffer.concat(pcm);
  fs.writeFileSync(out, wrapWav(data, RATE));
  const frames = data.length / 2;
  // quick RMS so the caller can gate on "was there speech".
  let sum = 0;
  for (let i = 0; i + 1 < data.length; i += 2) { const s = data.readInt16LE(i) / 32768; sum += s * s; }
  const rms = frames ? Math.sqrt(sum / frames) : 0;
  console.log(JSON.stringify({ out, seconds: +(frames / RATE).toFixed(2), frames, rms: +rms.toFixed(4) }));
  process.exit(0);
}

sock.on('error', (e) => { console.error('tap-capture socket error:', e.message); process.exit(1); });
setTimeout(finish, Math.round(seconds * 1000) + 500);
