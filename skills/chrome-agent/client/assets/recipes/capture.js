// capture.js — network CAPTURE recipes (learn the real write calls a site makes).
//
// WHY: the WRITE recipes in post.js are hand-reconstructed internal-API replays. When a
// site changes its endpoint/headers/body shape they silently break (e.g. X error 226, the
// LinkedIn image schema). Instead of exporting a HAR in DevTools and eyeballing it, we
// instrument the page's OWN fetch/XHR from MAIN world, let the operator perform the real
// action (Post / Reply / Delete) by hand, then read back the exact call — endpoint, header
// NAMES, request+response body shape — as a recipe-ready skeleton.
//
// NO debugger attach, NO CDP, NO driven browser — it is the same inject-into-MAIN-world
// primitive every recipe uses, so it works on strict-CSP sites (LinkedIn) where a raw
// `eval` <script> would be blocked, and never trips the "extension is debugging" banner or
// fights remote-view for the debugger.
//
// SECURITY: sanitization happens AT CAPTURE TIME, in the page, before anything crosses back
// to the CLI. Header values matching /csrf|cookie|authorization|token|jsessionid|li_at|
// auth_token|bearer/ are replaced with "<REDACTED>". So no session secret ever lands in a
// capture file or in an agent's context — only the header NAME + that it's needed. The real
// WRITE recipes keep re-deriving those live from document.cookie (see postLinkedin).
//
// FLOW (all via `browser-bridge recipe`, one tab):
//   1. recipe capture:arm    -> installs the fetch/XHR interceptor (idempotent, buffers on
//                               window.__bridgeCap). MUST run BEFORE the action.
//   2. <operator does the action by hand in the tab — Post / Reply / Delete>
//   3. recipe capture:dump   -> returns the filtered, sanitized skeletons. Filter auto-tunes
//                               to the host (voyager / graphql / reddit api); override with
//                               opts.filter.
//   4. recipe capture:clear  -> empties the buffer between separate actions.
//
// CAVEAT: a FULL page navigation/reload wipes the MAIN-world patch — re-arm after it. SPA
// route changes (LinkedIn feed post/comment/delete, an X reply, Reddit comment) do NOT
// reload the window, so arm-then-act works. X top-level compose DOES navigate to
// /compose/post — arm AFTER the composer is open, or capture a reply instead.

// Self-contained (serialized into the tab) — every helper inlined, no closures over imports.

async function captureArm(opts) {
  const o = opts || {};
  const w = window;
  const host = location.hostname;
  if (w.__bridgeCap && w.__bridgeCap.installed) {
    return { armed: true, already: true, host, count: w.__bridgeCap.log.length, note: "already armed on this page — do the action, then capture:dump" };
  }

  const SKEY = "__bridgeCap_v1"; // sessionStorage mirror — survives full page reloads
  const buf = { installed: true, seq: 0, max: o.max || 300, log: [] };
  // rehydrate anything captured before a reload wiped the live patch
  try {
    const saved = JSON.parse(sessionStorage.getItem(SKEY) || "null");
    if (saved && Array.isArray(saved.log)) { buf.log = saved.log; buf.seq = saved.seq || saved.log.length; }
  } catch (e) {}
  const SENSITIVE = /csrf|cookie|authorization|token|jsessionid|li_at|auth_token|bearer|x-csrf|guest_id|set-cookie/i;
  const CAP = 6000; // per-body char cap kept in the buffer
  const redactHeaders = (h) => {
    const out = {};
    if (!h) return out;
    let entries = [];
    try {
      if (typeof Headers !== "undefined" && h instanceof Headers) entries = [...h.entries()];
      else if (Array.isArray(h)) entries = h;
      else entries = Object.entries(h);
    } catch (e) { return out; }
    for (const [k, v] of entries) out[k] = SENSITIVE.test(k) ? "<REDACTED>" : String(v).slice(0, 400);
    return out;
  };
  const bodyToText = (b) => {
    try {
      if (b == null) return null;
      if (typeof b === "string") return b.slice(0, CAP);
      if (typeof URLSearchParams !== "undefined" && b instanceof URLSearchParams) return b.toString().slice(0, CAP);
      if (typeof FormData !== "undefined" && b instanceof FormData) {
        const parts = [];
        for (const [k, v] of b.entries()) parts.push(k + "=" + (typeof v === "string" ? v.slice(0, 200) : "[" + (v && v.constructor && v.constructor.name) + "]"));
        return "FormData{ " + parts.join(", ") + " }";
      }
      return "[" + (b.constructor && b.constructor.name || typeof b) + "]";
    } catch (e) { return "[unserializable body]"; }
  };
  const push = (e) => {
    e.seq = ++buf.seq;
    buf.log.push(e);
    while (buf.log.length > buf.max) buf.log.shift();
    // mirror to sessionStorage so a subsequent full reload doesn't lose what we've captured
    try { sessionStorage.setItem(SKEY, JSON.stringify({ seq: buf.seq, log: buf.log })); } catch (e2) {}
  };

  // ---- patch fetch ----
  const origFetch = w.fetch;
  if (typeof origFetch === "function") {
    w.fetch = async function (input, init) {
      const method = ((init && init.method) || (input && input.method) || "GET").toUpperCase();
      let url = "";
      try { url = typeof input === "string" ? input : (input && input.url) || String(input); } catch (e) {}
      // merge headers from a Request object + the init override
      let reqHeaders = {};
      try {
        if (input && typeof input === "object" && input.headers) reqHeaders = { ...redactHeaders(input.headers) };
        if (init && init.headers) reqHeaders = { ...reqHeaders, ...redactHeaders(init.headers) };
      } catch (e) {}
      const reqBody = bodyToText(init && init.body);
      let res, err = null;
      try { res = await origFetch.apply(this, arguments); }
      catch (e) { err = String((e && e.message) || e); }
      let status = null, respBody = null;
      if (res) {
        status = res.status;
        try { respBody = (await res.clone().text()).slice(0, CAP); } catch (e) {}
      }
      push({ via: "fetch", method, url, reqHeaders, reqBody, status, respBody, error: err });
      if (err) throw new Error(err);
      return res;
    };
  }

  // ---- patch XMLHttpRequest ----
  const XHR = w.XMLHttpRequest;
  if (XHR && XHR.prototype) {
    const oOpen = XHR.prototype.open;
    const oSet = XHR.prototype.setRequestHeader;
    const oSend = XHR.prototype.send;
    XHR.prototype.open = function (method, url) {
      this.__cap = { method: String(method || "GET").toUpperCase(), url: String(url || ""), headers: {} };
      return oOpen.apply(this, arguments);
    };
    XHR.prototype.setRequestHeader = function (k, v) {
      if (this.__cap) this.__cap.headers[k] = SENSITIVE.test(k) ? "<REDACTED>" : String(v).slice(0, 400);
      return oSet.apply(this, arguments);
    };
    XHR.prototype.send = function (body) {
      const c = this.__cap;
      if (c) {
        const reqBody = bodyToText(body);
        this.addEventListener("loadend", () => {
          let respBody = null;
          try { respBody = String(this.responseText || "").slice(0, CAP); } catch (e) {}
          push({ via: "xhr", method: c.method, url: c.url, reqHeaders: c.headers, reqBody, status: this.status, respBody });
        });
      }
      return oSend.apply(this, arguments);
    };
  }

  w.__bridgeCap = buf;
  return {
    armed: true, host, note: "interceptor installed — perform the action by hand in this tab, then run capture:dump. A full page reload wipes it (re-arm).",
  };
}

async function captureDump(opts) {
  const o = opts || {};
  const host = location.hostname;
  let buf = window.__bridgeCap;
  let source = "live";
  // if a full reload wiped the live patch, recover what was mirrored to sessionStorage
  if (!buf || !buf.installed) {
    try {
      const saved = JSON.parse(sessionStorage.getItem("__bridgeCap_v1") || "null");
      if (saved && Array.isArray(saved.log)) { buf = { log: saved.log }; source = "sessionStorage (patch was wiped by a reload — re-arm before capturing more)"; }
    } catch (e) {}
  }
  if (!buf || !Array.isArray(buf.log)) return { error: "no capture — run capture:arm first, THEN do the action, THEN dump" };

  // default filter auto-tunes to the site's write endpoints; override with opts.filter
  let pattern = o.filter;
  if (!pattern) {
    if (/linkedin\.com/.test(host)) pattern = "voyager/api|/graphql|feed/comment|socialActivity|contentcreation";
    else if (/(^|\.)x\.com|twitter\.com/.test(host)) pattern = "i/api/graphql|/1\\.1/|CreateTweet|DeleteTweet|CreateRetweet|FavoriteTweet|CreateScheduledTweet";
    else if (/reddit\.com/.test(host)) pattern = "/svc/|/graphql|gql|/api/(submit|comment|del|vote|editusertext|morechildren|compose)";
    else pattern = null; // no host default -> return everything non-GET below
  }
  const rx = pattern ? new RegExp(pattern) : null;

  const onlyWrites = o.writesOnly !== false; // default: emphasize state-changing calls
  const entries = buf.log.filter((e) => {
    if (rx) return rx.test(e.url);
    if (onlyWrites) return e.method !== "GET" && e.method !== "HEAD";
    return true;
  });

  return {
    host, source, total: buf.log.length, matched: entries.length,
    filter: pattern || "(none — non-GET only)",
    entries,
    note: entries.length ? "each entry is a recipe-ready skeleton (header values redacted; recipes re-derive them live from document.cookie)" : "no matches — the action may not have fired, a reload wiped the patch, or widen opts.filter",
  };
}

async function captureClear() {
  const buf = window.__bridgeCap;
  if (!buf || !buf.installed) return { cleared: false, note: "not armed" };
  const had = buf.log.length;
  buf.log = [];
  buf.seq = 0;
  try { sessionStorage.removeItem("__bridgeCap_v1"); } catch (e) {}
  return { cleared: true, dropped: had };
}

const CAP_MATCH = "*://*/*"; // generic — the fn auto-tunes its dump filter to the host

export const capture = {
  "capture:arm": {
    world: "MAIN",
    match: CAP_MATCH,
    describe: "CAPTURE: install a fetch/XHR interceptor in the page (MAIN world) BEFORE you act. Header values are redacted at capture time. Then perform Post/Reply/Delete by hand and run capture:dump. Idempotent. opts:{max}.",
    fn: captureArm,
  },
  "capture:dump": {
    world: "MAIN",
    match: CAP_MATCH,
    describe: "CAPTURE: return the recorded calls as sanitized, recipe-ready skeletons. Filter auto-tunes to the host (linkedin voyager / x graphql / reddit api); override opts.filter (regex string). opts:{filter,writesOnly}.",
    fn: captureDump,
  },
  "capture:clear": {
    world: "MAIN",
    match: CAP_MATCH,
    describe: "CAPTURE: empty the interceptor buffer (between separate actions). Keeps the patch installed.",
    fn: captureClear,
  },
};
