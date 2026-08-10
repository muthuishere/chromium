#!/usr/bin/env node
// ceo-call — place a call and answer a call, in voice.
//
// Layer 3 gave us a conversation loop (call-loop.cjs) once we are IN a call. This is the
// other half: getting into one. Two verbs.
//
//   ceo-call.cjs call --to someone@example.com [--subject "..."] [--brain "claude -p"]
//       Creates a real Teams meeting via Graph, sends the join link to that person,
//       joins it in the fork with mic+ears armed, and runs the conversation loop.
//
//   ceo-call.cjs answer [--within 10] [--brain "claude -p"]
//       Polls the calendar for a meeting that is live (or starting within N minutes),
//       joins it, and runs the loop. This is "answer any call".
//
//   ceo-call.cjs next            # read-only: what would `answer` pick up right now?
//
// WHY POLLING AND NOT A RINGING PHONE: a delegated Graph token cannot subscribe to an
// incoming 1:1 Teams *call* ring without the calling-bot media stack (an app registration
// with Calls.* application permissions, a public notification endpoint, and the media SDK).
// Meetings are the reachable surface today: anyone can put time on the calendar or paste us
// a join link, and we pick it up. Stated plainly so nobody reads "answer" as more than it is.
'use strict';

const { spawn, spawnSync } = require('child_process');
const path = require('path');
const HERE = __dirname;

const TAP_PORT = Number(process.env.CEO_CALL_TAP_PORT || 39701);
const MIC_PORT = Number(process.env.CEO_CALL_MIC_PORT || 39702);

function apl(method, url, body) {
  const args = ['call', 'ms:reqsume', method, url];
  if (body) args.push('--content-type', 'application/json', '--body', JSON.stringify(body));
  const r = spawnSync('apl', args, { encoding: 'utf8', timeout: 90000 });
  if (r.status !== 0) throw new Error(`graph ${method} failed: ${(r.stderr || r.stdout || '').slice(0, 200)}`);
  try { return JSON.parse(r.stdout); } catch { return {}; }
}

function iso(d) { return new Date(d).toISOString().replace(/\.\d{3}Z$/, 'Z'); }

function createMeeting(subject, minutes = 30) {
  const now = Date.now();
  return apl('POST', 'https://graph.microsoft.com/v1.0/me/onlineMeetings', {
    startDateTime: iso(now + 60_000),
    endDateTime: iso(now + minutes * 60_000),
    subject: subject || 'deemwar CEO',
  });
}

// Send the link the way the person will actually see it. Email via Graph is the one
// channel we know is live for an arbitrary outside address.
function sendInvite(to, joinUrl, subject) {
  apl('POST', 'https://graph.microsoft.com/v1.0/me/sendMail', {
    message: {
      subject: subject || 'Call link',
      body: { contentType: 'Text', content: `Join here:\n\n${joinUrl}\n` },
      toRecipients: [{ emailAddress: { address: to } }],
    },
    saveToSentItems: true,
  });
}

// A meeting is "answerable" if it is live now or starts within `within` minutes AND
// carries a join URL. Calendar entries without one are not calls.
function findJoinable(withinMin) {
  const now = Date.now();
  const start = iso(now - 30 * 60_000);
  const end = iso(now + withinMin * 60_000);
  const url = `https://graph.microsoft.com/v1.0/me/calendarView?startDateTime=${start}&endDateTime=${end}` +
              `&$select=subject,start,end,onlineMeeting,isOnlineMeeting,organizer&$orderby=start/dateTime&$top=25`;
  const r = apl('GET', url);
  const items = (r.value || []).filter((e) => e.isOnlineMeeting && e.onlineMeeting && e.onlineMeeting.joinUrl);
  const live = items.filter((e) => {
    const s = new Date(e.start.dateTime + 'Z').getTime();
    const en = new Date(e.end.dateTime + 'Z').getTime();
    return now >= s - withinMin * 60_000 && now <= en;
  });
  return live;
}

function chromeAgent(args, timeout = 180000) {
  const r = spawnSync('chrome-agent', args, { encoding: 'utf8', timeout });
  return { status: r.status, out: (r.stdout || '') + (r.stderr || '') };
}

// Joining the Teams web pre-join screen: land on the URL, prefer "continue on this
// browser", turn the camera OFF, then join. The fork auto-grants media
// (--use-fake-ui-for-media-stream) so no OS permission dialog appears.
function joinTeams(joinUrl) {
  chromeAgent(['goto', joinUrl]);
  const st = chromeAgent(['status']);
  if (!/teams\.microsoft\.com|teams\.live\.com/.test(st.out)) {
    return { joined: false, why: `browser did not land on Teams: ${st.out.trim().slice(0, 120)}` };
  }
  // Pre-join is a moving DOM target, so drive it by visible text and accept partial
  // success — being ON the page with audio armed is what the loop needs.
  const clicks = [
    'Continue on this browser', 'Use the web app instead',
    'Turn camera off', 'Join now',
  ];
  for (const label of clicks) {
    chromeAgent(['evalwithcsp', `
      (async () => {
        const want = ${JSON.stringify(label.toLowerCase())};
        const els = [...document.querySelectorAll('button,[role=button],a')];
        const hit = els.find(e => (e.innerText||e.getAttribute('aria-label')||'').toLowerCase().includes(want));
        if (hit) { hit.click(); return 'clicked'; }
        return 'not-found';
      })()`], 60000);
  }
  return { joined: true };
}

function runLoop(brain, voice) {
  const args = ['call-loop.cjs', 'run', '--tap', String(TAP_PORT), '--mic', String(MIC_PORT)];
  if (brain) args.push('--brain', brain);
  if (voice) args.push('--voice', voice);
  const child = spawn('node', args, { cwd: HERE, stdio: 'inherit' });
  return child;
}

const [cmd, ...rest] = process.argv.slice(2);
const flags = {};
for (let i = 0; i < rest.length; i++) if (rest[i].startsWith('--')) { flags[rest[i].slice(2)] = rest[i + 1]; i++; }

(async () => {
  if (cmd === 'next') {
    const live = findJoinable(Number(flags.within || 10));
    if (!live.length) { console.log(JSON.stringify({ joinable: 0 })); return; }
    for (const e of live) {
      console.log(JSON.stringify({
        subject: e.subject, start: e.start.dateTime,
        organizer: (e.organizer && e.organizer.emailAddress && e.organizer.emailAddress.address) || null,
        joinUrl: e.onlineMeeting.joinUrl.slice(0, 80) + '…',
      }));
    }
    return;
  }

  if (cmd === 'call') {
    const to = flags.to;
    const m = createMeeting(flags.subject, Number(flags.minutes || 30));
    if (!m.joinWebUrl) throw new Error('Graph returned no joinWebUrl');
    console.log(JSON.stringify({ event: 'meeting-created', joinUrl: m.joinWebUrl }));
    if (to) { sendInvite(to, m.joinWebUrl, flags.subject); console.log(JSON.stringify({ event: 'invite-sent', to })); }
    const j = joinTeams(m.joinWebUrl);
    console.log(JSON.stringify({ event: 'join', ...j }));
    if (!j.joined) process.exit(1);
    runLoop(flags.brain, flags.voice);
    return;
  }

  if (cmd === 'answer') {
    const within = Number(flags.within || 10);
    const poll = Number(flags.poll || 30);
    console.log(JSON.stringify({ event: 'watching', within, pollSeconds: poll }));
    for (;;) {
      let live = [];
      try { live = findJoinable(within); } catch (e) { console.error(JSON.stringify({ event: 'poll-error', error: e.message })); }
      if (live.length) {
        const e = live[0];
        console.log(JSON.stringify({ event: 'answering', subject: e.subject }));
        const j = joinTeams(e.onlineMeeting.joinUrl);
        console.log(JSON.stringify({ event: 'join', ...j }));
        if (j.joined) { runLoop(flags.brain, flags.voice); return; }
      }
      await new Promise((r) => setTimeout(r, poll * 1000));
    }
  }

  console.error('usage: ceo-call.cjs call --to <email> [--subject s] [--brain cmd] [--voice v]');
  console.error('       ceo-call.cjs answer [--within 10] [--poll 30] [--brain cmd]');
  console.error('       ceo-call.cjs next');
  process.exit(2);
})().catch((e) => { console.error(e.message); process.exit(1); });
