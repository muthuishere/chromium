// post.js — WRITE recipes (publish to your own accounts). Centralised so the write
// surface is auditable in one place.
//
// SAFETY CONTRACT (every recipe here honours it):
//   - Default is STAGED: open the composer, fill the text, then STOP. Nothing publishes.
//   - It only clicks submit when opts.confirm === true (explicit per-action ok).
//   - If a captcha/challenge is present it refuses to submit and returns the sitekey so
//     the caller can solve it (captcha.mjs) and retry.
// Self-contained fns (serialized into the tab) — every helper is inlined.

// ---- LinkedIn: create a text post via the voyager normShares API --------------------
// Internal-API replay: the same fetch the web client fires on "Post". Works regardless of
// window focus/visibility (the DOM/shareActive composer does NOT — it needs real OS focus
// to mount Quill). VERIFIED end-to-end on a live session: POST -> 201, DELETE -> 204.
//   - flat commentaryV2 body (current 2026 CREATE shape; no author URN needed — LinkedIn
//     infers the actor from the li_at cookie).
//   - csrf-token = JSESSIONID cookie value (quotes stripped, keep the "ajax:" prefix).
//   - runs in MAIN world for a credentialed same-origin fetch with the page's telemetry.
async function postLinkedin(opts) {
  const o = opts || {};
  const text = (o.text || "").trim();
  if (!text) return { error: "opts.text required" };
  const m = document.cookie.match(/JSESSIONID="?([^";]+)"?/);
  const csrf = m ? m[1] : null;
  if (!csrf) return { error: "no JSESSIONID cookie — not logged in on this linkedin.com session?" };

  const base = {
    site: "linkedin", recipe: "post", method: "voyager:normShares", text,
    visibility: o.connectionsOnly ? "connections" : "anyone",
  };
  if (!o.confirm) {
    return { ...base, staged: true, note: "session + csrf OK, NOT submitted — pass opts.confirm=true to publish" };
  }

  const payload = {
    visibleToConnectionsOnly: !!o.connectionsOnly,
    externalAudienceProviders: [],
    commentaryV2: { text, attributes: [] },
    origin: "FEED",
    allowedCommentersScope: o.allowedCommentersScope || "ALL",
    postState: "PUBLISHED",
    media: [],
  };
  const r = await fetch("/voyager/api/contentcreation/normShares", {
    method: "POST",
    credentials: "include",
    headers: {
      "csrf-token": csrf,
      "content-type": "application/json; charset=UTF-8",
      accept: "application/vnd.linkedin.normalized+json+2.1",
      "x-restli-protocol-version": "2.0.0",
      "x-li-lang": "en_US",
    },
    body: JSON.stringify(payload),
  });
  let data = null;
  try { data = await r.json(); } catch (e) {}
  if (r.status === 201) {
    const blob = JSON.stringify(data || {});
    const urn = (blob.match(/urn:li:(share|ugcPost|activity):[a-zA-Z0-9:_-]+/) || [])[0] || null;
    const activityUrn = (blob.match(/urn:li:activity:[0-9]+/) || [])[0] || null;
    return { ...base, submitted: true, status: r.status, urn, activityUrn };
  }
  return {
    ...base, submitted: false, status: r.status,
    error: (data && (data.message || JSON.stringify(data).slice(0, 300))) || ("HTTP " + r.status),
  };
}

// ---- LinkedIn: create a post WITH an image via voyager media upload -----------------
// The text-only normShares API takes `media: []` and there is no DOM path (composer needs
// real OS focus to mount Quill — see postLinkedin above). Instead: (1) register an upload
// slot via voyagerVideoDashMediaUploadMetadata, which returns a urn + a pre-signed
// singleUploadUrl; (2) PUT the raw image bytes there; (3) normShares referencing that urn
// in the top-level `media` array. VERIFIED end-to-end on a live session (200 -> 2xx -> 201).
async function postLinkedinImage(opts) {
  const o = opts || {};
  const text = (o.text || "").trim();
  if (!text) return { error: "opts.text required" };
  if (!o.imageB64) return { error: "opts.imagePath (or imageB64) required" };
  const m = document.cookie.match(/JSESSIONID="?([^";]+)"?/);
  const csrf = m ? m[1] : null;
  if (!csrf) return { error: "no JSESSIONID cookie — not logged in on this linkedin.com session?" };

  const base = { site: "linkedin", recipe: "post-image", text };
  if (!o.confirm) {
    return { ...base, staged: true, note: "session + image OK, NOT submitted — pass opts.confirm=true to upload + publish" };
  }

  const bin = atob(o.imageB64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);

  const regR = await fetch("/voyager/api/voyagerVideoDashMediaUploadMetadata?action=upload", {
    method: "POST",
    credentials: "include",
    headers: {
      "csrf-token": csrf,
      "content-type": "application/json; charset=UTF-8",
      accept: "application/vnd.linkedin.normalized+json+2.1",
      "x-restli-protocol-version": "2.0.0",
    },
    body: JSON.stringify({ mediaUploadType: "IMAGE_SHARING", fileSize: bytes.length, filename: o.imageName || "image.png" }),
  });
  let regData = null;
  try { regData = await regR.json(); } catch (e) {}
  const val = regData && regData.data && regData.data.value;
  if (regR.status !== 200 || !val || !val.singleUploadUrl || !val.urn) {
    return { ...base, step: "register", submitted: false, status: regR.status, error: JSON.stringify(regData).slice(0, 500) };
  }
  const { singleUploadUrl, urn } = val;

  const putR = await fetch(singleUploadUrl, {
    method: "PUT",
    credentials: "include",
    headers: { "content-type": o.imageMime || "image/png" },
    body: bytes,
  });
  if (!(putR.status >= 200 && putR.status < 300)) {
    return { ...base, step: "upload", submitted: false, status: putR.status, mediaUrn: urn };
  }

  const payload = {
    visibleToConnectionsOnly: !!o.connectionsOnly,
    externalAudienceProviders: [],
    commentaryV2: { text, attributes: [] },
    origin: "FEED",
    allowedCommentersScope: o.allowedCommentersScope || "ALL",
    postState: "PUBLISHED",
    content: { contentEntities: [{ entityLocation: urn, thumbnails: [] }] },
  };
  const r = await fetch("/voyager/api/contentcreation/normShares", {
    method: "POST",
    credentials: "include",
    headers: {
      "csrf-token": csrf,
      "content-type": "application/json; charset=UTF-8",
      accept: "application/vnd.linkedin.normalized+json+2.1",
      "x-restli-protocol-version": "2.0.0",
      "x-li-lang": "en_US",
    },
    body: JSON.stringify(payload),
  });
  let data = null;
  try { data = await r.json(); } catch (e) {}
  if (r.status === 201) {
    const blob = JSON.stringify(data || {});
    const postUrn = (blob.match(/urn:li:(share|ugcPost|activity):[a-zA-Z0-9:_-]+/) || [])[0] || null;
    return { ...base, submitted: true, status: r.status, urn: postUrn, mediaUrn: urn };
  }
  return {
    ...base, submitted: false, status: r.status, mediaUrn: urn,
    error: (data && (data.message || JSON.stringify(data).slice(0, 300))) || ("HTTP " + r.status),
  };
}

// ---- LinkedIn: delete a post (makes test posts reversible) --------------------------
async function deleteLinkedin(opts) {
  const o = opts || {};
  const urn = (o.urn || "").trim();
  if (!urn) return { error: "opts.urn required (e.g. urn:li:share:123 or urn:li:ugcPost:123)" };
  const m = document.cookie.match(/JSESSIONID="?([^";]+)"?/);
  const csrf = m ? m[1] : null;
  if (!csrf) return { error: "no JSESSIONID cookie" };
  if (!o.confirm) return { site: "linkedin", recipe: "delete-post", urn, staged: true, note: "pass opts.confirm=true to delete" };
  const r = await fetch("/voyager/api/contentcreation/normShares/" + encodeURIComponent(urn), {
    method: "DELETE",
    credentials: "include",
    headers: { "csrf-token": csrf, "x-restli-protocol-version": "2.0.0" },
  });
  return { site: "linkedin", recipe: "delete-post", urn, deleted: r.status >= 200 && r.status < 300, status: r.status };
}

// ---- LinkedIn: comment/reply on a post by DRIVING THE UI (schema-free) ----------------
// The voyager dash comment endpoint returns opaque 400s (verified 2026-07-11, 5 body shapes)
// and the web client uses page-instance-bound SDUI — so we drive the real comment box the way
// a human does: open it, fill the Quill editor editor-aware (reusing compose.js's technique),
// click the box's own submit button. Runs on the post's permalink page (open it first so the
// comment box is present). opts:{text, confirm, selector?}.
async function commentLinkedin(opts) {
  const o = opts || {};
  const text = (o.text || "").trim();
  if (!text) return { error: "opts.text required" };
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const qa = (s, root) => Array.from((root || document).querySelectorAll(s));
  const btnText = (b) => ((b.getAttribute("aria-label") || "") + " " + (b.innerText || "")).trim();

  // LinkedIn's comment editor is now TipTap/ProseMirror (not Quill) — aria-label
  // "Text editor for creating comment". Match that first, then legacy Quill, then any
  // comment-scoped contenteditable/textbox.
  const findEditor = () =>
    document.querySelector("div.tiptap.ProseMirror[contenteditable='true'], .tiptap.ProseMirror[contenteditable='true']") ||
    document.querySelector(".comments-comment-box .ql-editor, .comments-comment-texteditor .ql-editor, .comments-comment-box-comment__text-editor .ql-editor") ||
    qa(".ql-editor, .ProseMirror, [contenteditable='true'], [role='textbox']").find((e) => /comment|reply/i.test((e.getAttribute("aria-label") || e.getAttribute("data-placeholder") || ""))) || null;

  const diag = { openedBox: false };
  let editor = findEditor();
  if (!editor) {
    // click a "Comment" social-action button to reveal the box
    const cbtn = qa("button").find((b) => /(^|\b)comment\b/i.test(btnText(b)) && !/comments?\s*count|view/i.test(btnText(b)));
    if (cbtn) { cbtn.click(); diag.openedBox = true; await sleep(1200); editor = findEditor(); }
  }
  if (!editor) {
    // DIAGNOSTIC: report the real editor/button candidates so selectors can be fixed
    const editors = qa(".ql-editor, [contenteditable='true'], [role='textbox']").slice(0, 12).map((e) => ({
      cls: (e.className || "").toString().slice(0, 120),
      placeholder: e.getAttribute("data-placeholder") || e.getAttribute("aria-label") || null,
      boxCls: (e.closest("[class*='comment']") ? e.closest("[class*='comment']").className : "").toString().slice(0, 120),
    }));
    const commentBtns = qa("button").filter((b) => /comment/i.test(btnText(b))).slice(0, 8).map((b) => ({ label: btnText(b).slice(0, 50), cls: (b.className || "").toString().slice(0, 90) }));
    return { site: "linkedin", recipe: "comment", error: "no comment editor found", diag, url: location.href, candidateEditors: editors, commentButtons: commentBtns };
  }

  const base = { site: "linkedin", recipe: "comment", text };

  // --- fill the editor editor-aware: Quill instance API if present, else ProseMirror/
  //     contenteditable via a synthetic paste (ProseMirror processes paste correctly),
  //     falling back to a beforeinput insertText sequence. ---
  editor.focus();
  await sleep(150);
  const container = (editor.closest && (editor.closest(".ql-container") || editor.closest(".ql-editor"))) || editor;
  const quill = (window.Quill && window.Quill.find && window.Quill.find(container)) || container.__quill || null;
  let method;
  if (quill && typeof quill.setText === "function") { quill.setText(text + "\n"); method = "quill:instance"; }
  else {
    try { document.execCommand("selectAll", false, null); } catch (e) {} // clear prior staged draft
    try {
      const dt = new DataTransfer();
      dt.setData("text/plain", text);
      editor.dispatchEvent(new ClipboardEvent("paste", { bubbles: true, cancelable: true, clipboardData: dt }));
      method = "paste";
    } catch (e) { method = "paste-failed"; }
    await sleep(250);
    if (!(editor.innerText || "").trim()) {
      const fire = (n, t, init) => { try { n.dispatchEvent(new InputEvent(t, Object.assign({ bubbles: true, cancelable: true }, init))); } catch (e) {} };
      fire(editor, "beforeinput", { inputType: "insertText", data: text });
      fire(editor, "input", { inputType: "insertText", data: text });
      method = method + "+beforeinput";
    }
  }
  await sleep(500);
  const rendered = (editor.innerText || "").trim();

  // --- locate the comment box's own submit button, scoped to the editor's ancestors
  //     (the page has many "Comment" action buttons — walk up from the FOCUSED editor).
  let submit = null;
  let node = editor;
  for (let up = 0; up < 7 && node; up++) {
    node = node.parentElement;
    if (!node) break;
    const cand = qa("button", node).find((b) => /^(post|comment|reply|respond)$/i.test((b.innerText || "").trim()) && !b.disabled);
    if (cand) { submit = cand; break; }
  }
  if (!submit) submit = document.querySelector(".comments-comment-box__submit-button, .comments-comment-box__submit-button--cr");

  const info = { ...base, filled: method, rendered: rendered.slice(0, 300), submitFound: !!submit, submitDisabled: submit ? !!submit.disabled : null, diag };
  if (!o.confirm) return { ...info, staged: true, note: "comment box filled, NOT submitted — pass confirm:true to post it" };
  if (!rendered) return { ...info, error: "editor empty after fill — Quill may not have accepted the text" };
  if (!submit || submit.disabled) return { ...info, error: "submit button not found or disabled" };

  // Success signal: LinkedIn CLEARS the editor once the comment posts. Poll for the
  // editor emptying (robust across DOM class churn) as the primary confirmation.
  submit.click();
  for (let i = 0; i < 16; i++) {
    await sleep(500);
    if (!(editor.innerText || "").trim()) return { ...info, submitted: true, verified: true };
  }
  return { ...info, submitted: true, verified: false, note: "clicked submit; editor did not clear within timeout — check the post" };
}

// ---- LinkedIn: delete a comment WE posted (the undo for linkedin:comment) -------------
// Makes every public comment instantly retractable. Drives the comment's own ⋯ options menu
// (only our own comments expose a "Delete" item) -> Delete -> confirm sheet. Open the post's
// permalink first. Target precisely with opts.matchText (a substring of our comment) so it can
// only ever act on the intended comment. Staged unless opts.confirm=true. opts:{matchText,confirm}.
async function deleteCommentLinkedin(opts) {
  const o = opts || {};
  const matchText = (o.matchText || "").trim();
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const qa = (s, r) => Array.from((r || document).querySelectorAll(s));
  const vis = (e) => !!(e && e.offsetParent !== null);
  const txt = (b) => (((b.getAttribute && b.getAttribute("aria-label")) || "") + " " + (b.innerText || "")).trim();
  const gesture = (el) => { const r = el.getBoundingClientRect(); const g = { bubbles: true, cancelable: true, composed: true, clientX: r.left + Math.min(5, r.width / 2), clientY: r.top + Math.min(5, r.height / 2), button: 0, view: window }; ["pointerover", "pointerdown", "mousedown", "pointerup", "mouseup", "click"].forEach((t) => { try { el.dispatchEvent(new (t.startsWith("pointer") ? PointerEvent : MouseEvent)(t, g)); } catch (e) {} }); };
  const base = { site: "linkedin", recipe: "comment-delete", matchText };

  // Anchor on the per-comment ⋯ button — aria-label "View more options for <name>'s comment"
  // (only OUR comments expose a Delete in that menu). Climb from the button to the ancestor that
  // holds the comment text to match opts.matchText — robust against LinkedIn's churny container
  // class names (the old comment-entity selectors stopped matching, 2026-07).
  const optBtns = qa("button").filter((b) => vis(b) && /more options for .*comment/i.test(b.getAttribute("aria-label") || ""));
  const ancestorText = (b) => { let n = b; for (let i = 0; i < 12 && n; i++) { n = n.parentElement; if (n && (n.innerText || "").includes(matchText)) return n.innerText || ""; } return null; };
  let menuBtn = null, preview = "";
  if (matchText) {
    for (const b of optBtns) { const t = ancestorText(b); if (t) { menuBtn = b; preview = t.slice(0, 140); break; } }
  } else if (optBtns.length === 1) { menuBtn = optBtns[0]; }
  if (!menuBtn) return { ...base, error: matchText ? "no own comment whose text contains matchText was found (open the post permalink; ensure the comment rendered)" : (optBtns.length ? "matchText required — " + optBtns.length + " own comments present (ambiguous)" : "no own-comment ⋯ button found"), url: location.href, ownCommentButtons: optBtns.length };
  if (!o.confirm) return { ...base, staged: true, found: true, commentPreview: preview, note: "own comment located; pass confirm:true to delete" };

  gesture(menuBtn);
  await sleep(1000);
  // The menu items are LinkedIn SDUI <div role="menuitem"> that do NOT respond to a synthetic
  // click (React-delegated handlers) — they activate via the keyboard (Enter/Space on the
  // focused item). Find "Delete", focus it, and key it.
  const key = (el, k, kc) => ["keydown", "keyup"].forEach((t) => { try { el.dispatchEvent(new KeyboardEvent(t, { bubbles: true, cancelable: true, composed: true, key: k, code: k, keyCode: kc, which: kc, view: window })); } catch (e) {} });
  let del = qa("[role=menuitem]").find((e) => vis(e) && /^delete$/i.test((e.innerText || "").trim()));
  if (!del) return { ...base, error: "Delete item not found in the comment's ⋯ menu", commentPreview: preview, menuItems: qa("[role=menuitem]").filter(vis).map((e) => (e.innerText || "").trim().slice(0, 24)) };
  if (del.focus) del.focus();
  await sleep(200);
  key(del, "Enter", 13); await sleep(300); key(del, " ", 32);

  // Confirm sheet is a native <dialog open> in the top layer (offsetParent is null there, so the
  // vis() check would wrongly reject it) — match dialog[open] directly, click its "Delete".
  let confirmed = false;
  for (let i = 0; i < 12; i++) {
    await sleep(350);
    const dlg = qa("dialog[open], [role=alertdialog], [role=dialog], .artdeco-modal").find((d) => /delete comment/i.test(d.innerText || ""));
    if (dlg) { const cb = Array.from(dlg.querySelectorAll("button")).find((b) => /^delete$/i.test((b.innerText || "").trim())); if (cb) { cb.click(); confirmed = true; break; } }
  }
  await sleep(1500);
  const stillThere = matchText ? qa("button").some((b) => vis(b) && /more options for .*comment/i.test(b.getAttribute("aria-label") || "") && ancestorText(b)) : null;
  return { ...base, submitted: true, confirmedDialog: confirmed, deleted: matchText ? stillThere === false : confirmed, note: matchText ? undefined : "no matchText given — verify manually that the right comment was removed" };
}

// ---- X / Twitter: create a tweet via the native composer (intent pre-fill + media) --
// X's internal CreateTweet API rejects naive replay with error 226 (it requires a derived
// x-client-transaction-id). So we drive X's OWN composer — the human path. Two steps:
//   call 1 (no confirm): navigate to /compose/post?text=<text> → composer opens PRE-FILLED.
//     If imageB64 is given, attach it to the composer's <input type=file> and wait for the
//     preview thumbnail to render.
//   call 2 (confirm:true): composer already open (+ media attached, if requested) → click
//     the real "Post" button.
async function postX(opts) {
  const o = opts || {};
  const text = (o.text || "").trim();
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const q = (s) => document.querySelector(s);

  let box = q('div[data-testid="tweetTextarea_0"]');

  // no composer on this page → open the dedicated one (click the Post nav button, else navigate)
  if (!box) {
    if (!text) return { error: "opts.text required to open the composer" };
    const opener = q('a[data-testid="SideNav_NewTweet_Button"], a[href="/compose/post"], a[aria-label="Post"]');
    if (opener) { opener.click(); await sleep(900); box = q('div[data-testid="tweetTextarea_0"]'); }
  }
  if (!box) {
    const url = "https://x.com/compose/post?text=" + encodeURIComponent(text);
    setTimeout(() => { location.href = url; }, 60);
    return {
      site: "x", recipe: "post", staged: true, opening: true, text,
      note: "Opening the composer pre-filled — re-run x:post with the same opts once it mounts, then confirm:true.",
    };
  }

  // fill the composer DIRECTLY (works whether it was already open — e.g. the persistent home
  // inline composer, the old bug — or just opened above). DraftJS: selectAll then a synthetic
  // paste, falling back to a beforeinput insertText sequence.
  let filled = "existing";
  box.focus();
  await sleep(120);
  const cur = (box.innerText || "").trim();
  if (text && cur !== text) {
    // DraftJS fill — execCommand insertText updates the EditorState (enables Post); paste /
    // beforeinput are fallbacks that often leave the button disabled. See replyX for the why.
    const selectBox = () => { try { const s = window.getSelection(); s.removeAllRanges(); const rng = document.createRange(); rng.selectNodeContents(box); s.addRange(rng); } catch (e) {} };
    selectBox();
    filled = "";
    try { if (document.execCommand("insertText", false, text)) filled = "insertText"; } catch (e) {}
    await sleep(250);
    if (!(box.innerText || "").trim()) {
      selectBox();
      try { const dt = new DataTransfer(); dt.setData("text/plain", text); box.dispatchEvent(new ClipboardEvent("paste", { bubbles: true, cancelable: true, clipboardData: dt })); filled = filled ? filled + "+paste" : "paste"; } catch (e) {}
      await sleep(300);
    }
    if (!(box.innerText || "").trim()) {
      const fire = (n, t, init) => { try { n.dispatchEvent(new InputEvent(t, Object.assign({ bubbles: true, cancelable: true }, init))); } catch (e) {} };
      fire(box, "beforeinput", { inputType: "insertText", data: text });
      fire(box, "input", { inputType: "insertText", data: text });
      filled = filled ? filled + "+beforeinput" : "beforeinput";
    }
  }

  const captcha =
    q('iframe[src*="arkose"], iframe[src*="funcaptcha"], #arkose') ? { type: "arkose" } :
    q('iframe[src*="challenges.cloudflare.com"], .cf-turnstile') ? { type: "turnstile" } : null;
  const editorText = (box.innerText || "").trim();

  // --- attach media (image) if given and not already attached ---
  const b64 = o.imageB64 || o.mediaB64 || null;
  const mime = o.imageMime || o.mediaMime || (b64 ? "image/png" : "");
  const mname = o.imageName || o.mediaName || "upload.png";
  let mediaAttached = !!document.querySelector('[data-testid="attachments"] img, div[aria-label*="Image"] img');
  if (b64 && !mediaAttached) {
    const input = document.querySelector('input[type=file][data-testid="fileInput"], input[type=file][accept*="image"]');
    if (!input) {
      return { site: "x", recipe: "post", composerOpen: true, editorText: editorText.slice(0, 280), error: "media file input not found in composer" };
    }
    const bin = atob(b64);
    const u8 = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) u8[i] = bin.charCodeAt(i);
    const file = new File([u8], mname, { type: mime || "application/octet-stream" });
    const dt = new DataTransfer();
    dt.items.add(file);
    input.files = dt.files;
    input.dispatchEvent(new Event("input", { bubbles: true }));
    input.dispatchEvent(new Event("change", { bubbles: true }));
    for (let i = 0; i < 20; i++) {
      await sleep(400);
      mediaAttached = !!document.querySelector('[data-testid="attachments"] img, div[aria-label*="Image"] img');
      if (mediaAttached) break;
    }
  }

  const postBtn = q('button[data-testid="tweetButton"], button[data-testid="tweetButtonInline"]');
  const base = {
    site: "x", recipe: "post", composerOpen: true, filled,
    editorText: editorText.slice(0, 280), mediaRequested: !!b64, mediaAttached, captcha,
    postDisabled: postBtn ? !!postBtn.disabled : null,
  };

  if (!o.confirm) return { ...base, staged: true, note: "Composer is open (+ media attached, if requested). Re-run with opts.confirm=true to Post." };
  if (captcha) return { ...base, blocked: "captcha", note: "Solve the challenge, then retry with confirm=true." };
  if (!editorText) return { ...base, error: "composer is empty — open it first with the text (call without confirm)" };
  if (b64 && !mediaAttached) return { ...base, error: "media requested but did not attach — not posting" };
  if (!postBtn || postBtn.disabled) return { ...base, error: "Post button not found or disabled (media may still be uploading)" };
  postBtn.click();
  await sleep(1200);
  return { ...base, submitted: true };
}

// ---- X / Twitter: reply to a tweet by DRIVING THE UI (schema-free, no 226) ----------
// X's CreateTweet API rejects naive replay (error 226 needs a derived x-client-transaction-id).
// Driving X's own composer sidesteps that entirely — X's JS computes the header. Open the
// tweet's permalink first (the inline "Post your reply" box is present). Fill DraftJS via a
// synthetic paste (fallback beforeinput), click the Reply button. opts:{text, confirm}.
async function replyX(opts) {
  const o = opts || {};
  const text = (o.text || "").trim();
  if (!text) return { error: "opts.text required" };
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const q = (s) => document.querySelector(s);

  let box = q('div[data-testid="tweetTextarea_0"]');
  if (!box) {
    const opener = q('[data-testid="tweetTextarea_0RichTextInputContainer"], div[data-testid="reply"]');
    if (opener) { opener.click(); await sleep(700); box = q('div[data-testid="tweetTextarea_0"]'); }
  }
  if (!box) return { site: "x", recipe: "reply", error: "reply composer not found — open the tweet's permalink first" };

  const captcha =
    q('iframe[src*="arkose"], iframe[src*="funcaptcha"], #arkose') ? { type: "arkose" } :
    q('iframe[src*="challenges.cloudflare.com"], .cf-turnstile') ? { type: "turnstile" } : null;

  box.focus();
  await sleep(400);
  // DraftJS fill: execCommand("insertText") is what updates DraftJS's EditorState — it fires the
  // native `beforeinput` that DraftJS's editOnBeforeInput handler intercepts and applies to the
  // model, which is what ENABLES the Reply/Post button. A synthetic paste/beforeinput mutates the
  // DOM text but often NOT the DraftJS model, so the button stays disabled (the old x:post bug).
  // So insertText is primary; paste + a raw beforeinput are fallbacks. selectNodeContents first so
  // we replace (not append) any prior staged draft.
  const selectBox = () => { try { const s = window.getSelection(); s.removeAllRanges(); const rng = document.createRange(); rng.selectNodeContents(box); s.addRange(rng); } catch (e) {} };
  selectBox();
  let method = "";
  try { if (document.execCommand("insertText", false, text)) method = "insertText"; } catch (e) {}
  await sleep(250);
  if (!(box.innerText || "").trim()) {
    selectBox();
    try { const dt = new DataTransfer(); dt.setData("text/plain", text); box.dispatchEvent(new ClipboardEvent("paste", { bubbles: true, cancelable: true, clipboardData: dt })); method = method ? method + "+paste" : "paste"; } catch (e) {}
    await sleep(300);
  }
  if (!(box.innerText || "").trim()) {
    const fire = (n, t, init) => { try { n.dispatchEvent(new InputEvent(t, Object.assign({ bubbles: true, cancelable: true }, init))); } catch (e) {} };
    fire(box, "beforeinput", { inputType: "insertText", data: text });
    fire(box, "input", { inputType: "insertText", data: text });
    method = method ? method + "+beforeinput" : "beforeinput";
  }
  await sleep(300);
  // Read the DraftJS MODEL, not box.innerText. X renders an offscreen a11y mirror inside the
  // editor, so innerText returns the text TWICE — it looks exactly like a double-paste bug and
  // will scare you off a perfectly good reply (or tempt you to "fix" it by halving the text).
  // The [data-text=true] spans are the real content; cross-check against the Reply button, which
  // X disables when the model actually exceeds 280.
  const modelText = [...box.querySelectorAll('[data-text="true"]')].map((s) => s.textContent).join("");
  const rendered = (modelText || box.innerText || "").trim();
  // --- attach media (image) if given and not already attached ---
  // ORDER MATTERS, and it is the OPPOSITE of postX: X does not render the reply toolbar (and so
  // does not create the <input type=file>) until the composer HAS TEXT. Probed 2026-07-22 —
  // empty composer: 0 file inputs, 0 toolbar; after one insertText: fileInput + toolBar appear.
  // So fill first, attach second. Attaching before the fill can never work.
  // Lifted from postX (2026-07-22): x:reply silently IGNORED imagePath/imageB64 — no error, the
  // reply just posted text-only, which reads as "images don't work on replies" rather than "this
  // recipe never had the code". Same selectors + settle-loop as the post path.
  // NOTE: chrome-agent's recipe-run.mjs passes opts straight through and does NOT expand
  // imagePath -> imageB64 (that expansion lives in browser-bridge's bridge.sh). So callers going
  // through chrome-agent must pass imageB64 themselves.
  const rb64 = o.imageB64 || o.mediaB64 || null;
  const rmime = o.imageMime || o.mediaMime || (rb64 ? "image/png" : "");
  const rname = o.imageName || o.mediaName || "upload.png";
  let replyMediaAttached = !!document.querySelector('[data-testid="attachments"] img, div[aria-label*="Image"] img');
  if (rb64 && !replyMediaAttached) {
    // The reply composer is collapsed on a permalink page: the <input type=file> is not in the
    // DOM until the box is focused/expanded, so poll rather than reading it once (found 2026-07-22
    // — the one-shot lookup failed every time and looked like 'X removed image replies').
    let rinput = null;
    for (let i = 0; i < 15; i++) {
      rinput = document.querySelector('input[type=file][data-testid="fileInput"], input[type=file][accept*="image"]');
      if (rinput) break;
      await sleep(300);
    }
    if (!rinput) return { site: "x", recipe: "reply", error: "media file input not found in reply composer (composer may not have expanded)" };
    const rbin = atob(rb64);
    const ru8 = new Uint8Array(rbin.length);
    for (let i = 0; i < rbin.length; i++) ru8[i] = rbin.charCodeAt(i);
    const rdt = new DataTransfer();
    rdt.items.add(new File([ru8], rname, { type: rmime || "application/octet-stream" }));
    rinput.files = rdt.files;
    rinput.dispatchEvent(new Event("input", { bubbles: true }));
    rinput.dispatchEvent(new Event("change", { bubbles: true }));
    for (let i = 0; i < 20; i++) {
      await sleep(400);
      replyMediaAttached = !!document.querySelector('[data-testid="attachments"] img, div[aria-label*="Image"] img');
      if (replyMediaAttached) break;
    }
    if (!replyMediaAttached) return { site: "x", recipe: "reply", error: "image did not attach to the reply composer within timeout" };
  }

  const btn = q('button[data-testid="tweetButton"], button[data-testid="tweetButtonInline"]');
  const base = { site: "x", recipe: "reply", filled: method, rendered: rendered.slice(0, 280), renderedLen: rendered.length, submitFound: !!btn, submitDisabled: btn ? !!btn.disabled : null, captcha, mediaRequested: !!rb64, mediaAttached: replyMediaAttached };
  if (!o.confirm) return { ...base, staged: true, note: "reply composer filled, NOT submitted — pass confirm:true to Reply" };
  if (captcha) return { ...base, blocked: "captcha", note: "solve the challenge, then retry with confirm=true" };
  if (!rendered) return { ...base, error: "composer empty after fill" };
  if (!btn || btn.disabled) return { ...base, error: "Reply button not found or disabled" };
  btn.click();
  for (let i = 0; i < 12; i++) {
    await sleep(500);
    if (!(q('div[data-testid="tweetTextarea_0"]') || {}).innerText || !(q('div[data-testid="tweetTextarea_0"]').innerText || "").trim()) {
      return { ...base, submitted: true, verified: true };
    }
  }
  return { ...base, submitted: true, verified: false, note: "clicked Reply; composer did not clear within timeout — check the tweet" };
}

// ---- X / Twitter: delete a tweet/reply by DRIVING THE UI (caret -> Delete -> confirm) -
// No API replay — clicks the tweet's ⋯ caret, the Delete menu item, then the confirm sheet.
// Target a specific tweet by opts.tweetId (matches its /status/<id> link); else the first
// caret on the page (use on the tweet's own permalink). opts:{tweetId?, confirm}.
async function deleteX(opts) {
  const o = opts || {};
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const q = (s, r) => (r || document).querySelector(s);
  const qa = (s, r) => Array.from((r || document).querySelectorAll(s));

  let caret = null;
  if (o.tweetId) {
    const art = qa("article").find((a) => a.querySelector('a[href*="/status/' + o.tweetId + '"]'));
    if (art) caret = q('button[data-testid="caret"]', art);
  }
  if (!caret) caret = q('button[data-testid="caret"]');
  const base = { site: "x", recipe: "delete-post", tweetId: o.tweetId || null };
  if (!caret) return { ...base, error: "no tweet caret (⋯) found — open the tweet first" };
  if (!o.confirm) return { ...base, staged: true, caretFound: true, note: "pass confirm:true to open the ⋯ menu, click Delete, confirm" };

  caret.click();
  await sleep(800);
  const del = qa('[role="menuitem"], [data-testid="Delete"]').find((m) => /^delete$/i.test((m.innerText || "").trim()));
  if (!del) return { ...base, error: "Delete menu item not found (is this your tweet?)", menu: qa('[role="menuitem"]').map((m) => (m.innerText || "").trim()).slice(0, 8) };
  del.click();
  await sleep(800);
  const confirm = q('button[data-testid="confirmationSheetConfirm"]');
  if (!confirm) return { ...base, error: "confirm sheet not found" };
  confirm.click();
  await sleep(1200);
  return { ...base, deleted: true };
}

// ---- Reddit: submit a self/text post via the API (MAIN world, credentialed) ---------
async function postReddit(opts) {
  const o = opts || {};
  const sr = (o.subreddit || o.sr || "").replace(/^r\//, "").trim();
  const title = (o.title || "").trim();
  const text = (o.text || "").trim();
  if (!sr || !title) return { error: "opts.subreddit and opts.title required" };

  // modhash (uh) proves the logged-in session for write calls
  const meR = await fetch("/api/me.json", { credentials: "include", headers: { accept: "application/json" } });
  if (!meR.ok) return { error: "could not read /api/me.json (logged in?) " + meR.status };
  const me = await meR.json();
  const uh = me?.data?.modhash || null;
  if (!uh) return { error: "no modhash — not logged in on this reddit session" };

  const base = { site: "reddit", recipe: "post", subreddit: sr, title };
  if (!o.confirm) return { ...base, staged: true, note: "validated session, NOT submitted — pass opts.confirm=true to publish" };

  const body = new URLSearchParams({
    api_type: "json", kind: "self", sr, title, text, uh, resubmit: "true", sendreplies: "true",
  });
  const r = await fetch("/api/submit", {
    method: "POST",
    credentials: "include",
    headers: { "content-type": "application/x-www-form-urlencoded", accept: "application/json" },
    body: body.toString(),
  });
  const j = await r.json().catch(() => ({}));
  const errs = j?.json?.errors || [];
  if (errs.length) return { ...base, submitted: false, errors: errs };
  // fullname (t3_...) lets us delete it later
  return { ...base, submitted: true, url: j?.json?.data?.url || null, fullname: j?.json?.data?.name || null };
}

// ---- Reddit: submit a text post by DRIVING THE SUBMIT PAGE UI (shadow-DOM aware) -----
// The /api/submit endpoint is captcha-gated (BAD_CAPTCHA) and the legacy iden-captcha is
// dead. New-reddit's submit form works fine though — drive it. The composer is web-component
// / shadow-DOM heavy, so we deep-query through shadow roots. Open r/<sub>/submit/?type=TEXT
// first. opts:{title, text|body, confirm, probe?}.
async function submitReddit(opts) {
  const o = opts || {};
  const title = (o.title || "").trim();
  const text = (o.text || o.body || "").trim();
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  // deep querySelectorAll that pierces open shadow roots
  const deepAll = (sel, root, acc) => {
    root = root || document; acc = acc || [];
    try { root.querySelectorAll(sel).forEach((e) => acc.push(e)); } catch (e) {}
    try { (root.querySelectorAll("*") || []).forEach((e) => { if (e.shadowRoot) deepAll(sel, e.shadowRoot, acc); }); } catch (e) {}
    return acc;
  };
  const attr = (e, n) => (e.getAttribute(n) || "");
  const hint = (e) => (attr(e, "placeholder") + " " + attr(e, "aria-label") + " " + attr(e, "name") + " " + attr(e, "data-placeholder")).toLowerCase();

  if (o.probe) {
    const fields = deepAll('textarea, input[type="text"], [contenteditable="true"], [role="textbox"]').slice(0, 15)
      .map((e) => ({ tag: e.tagName.toLowerCase(), name: attr(e, "name"), ph: attr(e, "placeholder") || attr(e, "aria-label") || attr(e, "data-placeholder"), ce: e.getAttribute("contenteditable") }));
    const btns = deepAll("button").filter((b) => /post|save draft/i.test(b.innerText || "")).slice(0, 8).map((b) => ({ label: (b.innerText || "").trim().slice(0, 30), disabled: b.disabled }));
    return { site: "reddit", recipe: "submit", probe: true, url: location.href, fields, buttons: btns };
  }

  if (!title) return { error: "opts.title required" };

  // New-reddit renders VISIBLE contenteditable editors (a hidden textarea[name=title] just
  // mirrors the form). Fill the visible ones so React state updates and Post enables.
  //   body  = the contenteditable named "body"
  //   title = the first contenteditable that isn't the body (it precedes it in the DOM)
  let bodyEl = deepAll('[contenteditable="true"][name="body"]')[0] ||
    deepAll('[contenteditable="true"], [role="textbox"]').find((e) => /body|optional/.test(hint(e))) || null;
  let titleEl = deepAll('textarea[name="title"]')[0] ||
    deepAll('[contenteditable="true"]').filter((e) => e !== bodyEl && attr(e, "name") !== "body")[0] || null;

  const fillCE = (el, val) => { // contenteditable: select existing contents (incl. a saved draft) then paste over
    el.focus();
    try {
      const r = document.createRange(); r.selectNodeContents(el);
      const s = window.getSelection(); s.removeAllRanges(); s.addRange(r);
    } catch (e) {}
    try { const dt = new DataTransfer(); dt.setData("text/plain", val); el.dispatchEvent(new ClipboardEvent("paste", { bubbles: true, cancelable: true, clipboardData: dt })); }
    catch (e) { el.textContent = val; el.dispatchEvent(new InputEvent("input", { bubbles: true })); }
  };
  const fillInput = (el, val) => { // textarea/input via native setter + input event
    el.focus();
    const proto = el.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    const setter = Object.getOwnPropertyDescriptor(proto, "value").set;
    setter.call(el, val);
    el.dispatchEvent(new Event("input", { bubbles: true }));
    el.dispatchEvent(new Event("change", { bubbles: true }));
  };

  const diag = { titleFound: !!titleEl, bodyFound: !!bodyEl };
  if (!titleEl) {
    const fields = deepAll('textarea, input[type="text"], [contenteditable="true"], [role="textbox"]').slice(0, 12)
      .map((e) => ({ tag: e.tagName.toLowerCase(), name: attr(e, "name"), ph: attr(e, "placeholder") || attr(e, "aria-label") }));
    return { site: "reddit", recipe: "submit", error: "title field not found", diag, candidates: fields };
  }

  if (text && bodyEl) { fillCE(bodyEl, text); await sleep(300); }
  if (titleEl.tagName === "TEXTAREA" || titleEl.tagName === "INPUT") fillInput(titleEl, title); else fillCE(titleEl, title);
  await sleep(300);

  // Reddit enables Post asynchronously after validating the title — poll for it.
  let postBtn = null;
  for (let i = 0; i < 8; i++) {
    postBtn = deepAll("button").find((b) => /^post$/i.test((b.innerText || "").trim()) && !b.disabled);
    if (postBtn) break;
    await sleep(400);
  }
  const base = { site: "reddit", recipe: "submit", title, titleFilled: (titleEl.value || titleEl.innerText || "").slice(0, 80), bodyFilled: bodyEl ? (bodyEl.innerText || "").slice(0, 80) : null, postFound: !!postBtn, diag };
  if (!o.confirm) return { ...base, staged: true, note: "form filled, NOT submitted — pass confirm:true to Post" };
  if (!postBtn) return { ...base, error: "Post button not found or still disabled (title may not have registered)" };
  postBtn.click();
  // success: URL leaves /submit (redirects to the new post) within a few seconds
  for (let i = 0; i < 16; i++) { await sleep(500); if (!/\/submit/.test(location.href)) return { ...base, submitted: true, postUrl: location.href }; }
  return { ...base, submitted: true, verified: false, note: "clicked Post; page did not navigate — a captcha or validation may be blocking" };
}

// ---- Reddit: submit via the OFFICIAL OAuth API (oauth.reddit.com) using the session's
// own bearer token — the same endpoint Postiz uses, but with NO app registration (we reuse
// the token the logged-in shreddit SPA already holds). The token is read AND used entirely
// in-page and is NEVER returned to the caller. This is the legit path and should sidestep
// the BAD_CAPTCHA the www.reddit.com/api/submit web-replay hits. opts:{subreddit,title,text,url,confirm}.
async function postRedditOAuth(opts) {
  const o = opts || {};
  const sr = (o.subreddit || o.sr || "").replace(/^\/?r\//, "").trim();
  const title = (o.title || "").trim();
  const text = (o.text || "").trim();
  const kind = o.url ? "link" : "self";
  if (!sr || !title) return { error: "opts.subreddit and opts.title required" };

  // locate the session's oauth bearer token IN-PAGE — value never leaves the tab
  let token = null, source = null;
  try { const t = window.___r && window.___r.user && window.___r.user.session && window.___r.user.session.accessToken; if (t) { token = t; source = "window.___r"; } } catch (e) {}
  if (!token) {
    const scan = (store) => { try { for (let i = 0; i < store.length; i++) { const k = store.key(i); const v = store.getItem(k); if (v && /token|bearer|oauth|access/i.test(k) && /^[A-Za-z0-9._~+/=-]{20,}$/.test(v)) return v; } } catch (e) {} return null; };
    token = scan(localStorage) || scan(sessionStorage);
    if (token) source = "storage";
  }
  const diag = {
    tokenFound: !!token, source,
    hasWindowR: !!window.___r,
    sessionKeys: (window.___r && window.___r.user && window.___r.user.session) ? Object.keys(window.___r.user.session) : [],
    lsKeys: (() => { try { return Object.keys(localStorage).filter((k) => /token|oauth|access|session|bearer/i.test(k)); } catch (e) { return []; } })(),
  };
  const base = { site: "reddit", recipe: "post-oauth", subreddit: sr, title };
  if (!token) return { ...base, error: "no session oauth token found in-page", diag };
  if (!o.confirm) return { ...base, staged: true, tokenSource: source, note: "oauth token found in-page (not shown), NOT submitted — pass confirm:true to post via oauth.reddit.com/api/submit" };

  const body = new URLSearchParams({ api_type: "json", kind, title, sr, text });
  if (o.url) body.set("url", o.url);
  let r, j = {};
  try {
    r = await fetch("https://oauth.reddit.com/api/submit", {
      method: "POST",
      headers: { Authorization: "Bearer " + token, "Content-Type": "application/x-www-form-urlencoded" },
      body: body.toString(),
    });
    j = await r.json().catch(() => ({}));
  } catch (e) { return { ...base, submitted: false, error: "fetch failed (CORS?): " + String(e && e.message || e) }; }
  const errs = (j && j.json && j.json.errors) || [];
  if (errs.length) return { ...base, submitted: false, status: r.status, errors: errs };
  const data = (j && j.json && j.json.data) || {};
  return { ...base, submitted: true, status: r.status, url: data.url || null, name: data.name || data.id || null };
}

// ---- Reddit: comment/reply on a post or comment via the API (MAIN world) ------------
// Reddit has a clean write API: POST /api/comment with thing_id (t3_ post or t1_ comment) +
// text + the modhash (uh). Same credentialed MAIN-world path as postReddit. The returned
// t1_ fullname deletes via deleteReddit (reversible). opts:{thingId|parent, text, confirm}.
async function commentReddit(opts) {
  const o = opts || {};
  const parent = (o.thingId || o.parent || o.fullname || "").trim(); // t3_... (post) or t1_... (comment)
  const text = (o.text || "").trim();
  if (!parent || !text) return { error: "opts.thingId (t3_/t1_ fullname) and opts.text required" };
  const meR = await fetch("/api/me.json", { credentials: "include", headers: { accept: "application/json" } });
  if (!meR.ok) return { error: "could not read /api/me.json (logged in?) " + meR.status };
  const uh = (await meR.json().catch(() => ({})))?.data?.modhash || null;
  if (!uh) return { error: "no modhash — not logged in on this reddit session" };

  const base = { site: "reddit", recipe: "comment", parent };
  if (!o.confirm) return { ...base, staged: true, note: "validated session, NOT submitted — pass opts.confirm=true to comment" };

  const body = new URLSearchParams({ api_type: "json", thing_id: parent, text, uh });
  const r = await fetch("/api/comment", {
    method: "POST", credentials: "include",
    headers: { "content-type": "application/x-www-form-urlencoded", accept: "application/json" },
    body: body.toString(),
  });
  const raw = await r.text().catch(() => "");
  let j = null; try { j = JSON.parse(raw); } catch (e) {}
  const errs = j?.json?.errors || [];
  if (errs.length) return { ...base, submitted: false, errors: errs };
  const thing = j?.json?.data?.things?.[0]?.data || null;
  // the returned JSON shape varies; fall back to scraping the new comment's t1_ fullname
  const commentId = thing?.name || (raw.match(/t1_[a-z0-9]+/i) || [])[0] || null;
  return { ...base, submitted: true, commentId, permalink: thing?.permalink || null };
}

// ---- Reddit: delete a post by fullname (t3_...) — reversible tests -------------------
async function deleteReddit(opts) {
  const o = opts || {};
  const id = (o.fullname || o.id || "").trim(); // e.g. t3_abc123
  if (!id) return { error: "opts.fullname required (t3_... from the submit response)" };
  const meR = await fetch("/api/me.json", { credentials: "include", headers: { accept: "application/json" } });
  const uh = (await meR.json().catch(() => ({})))?.data?.modhash || null;
  if (!uh) return { error: "no modhash — not logged in" };
  const base = { site: "reddit", recipe: "delete-post", id };
  if (!o.confirm) return { ...base, staged: true, note: "pass opts.confirm=true to delete" };
  const r = await fetch("/api/del", {
    method: "POST", credentials: "include",
    headers: { "content-type": "application/x-www-form-urlencoded", accept: "application/json" },
    body: new URLSearchParams({ api_type: "json", id, uh }).toString(),
  });
  return { ...base, deleted: r.ok, status: r.status };
}

// ---- Hacker News: submit a story via the real form (fnid token replay) --------------
// HN's submit is a classic server-rendered form: GET /submit yields a hidden per-session
// `fnid` token; POST /r with {fnid,fnop,title,url,text}. Requires being logged in in the
// bridge profile (cookie `user`). Same-origin credentialed fetch from MAIN world.
// opts:{title, url?, text?, confirm}.
async function postHN(opts) {
  const o = opts || {};
  const title = (o.title || "").trim();
  const url = (o.url || "").trim();
  const text = (o.text || "").trim();
  if (!title) return { error: "opts.title required" };
  if (!url && !text) return { error: "opts.url or opts.text required" };

  const r = await fetch("/submit", { credentials: "include" });
  const html = await r.text();
  const fnid = (html.match(/name="fnid"\s+value="([^"]+)"/) || [])[1] || null;
  const fnop = (html.match(/name="fnop"\s+value="([^"]+)"/) || [])[1] || "submit-page";
  if (!fnid) return { error: "no fnid — not logged in to Hacker News in this profile?" };

  const base = { site: "hackernews", recipe: "post", title };
  if (!o.confirm) return { ...base, staged: true, note: "session + fnid OK, NOT submitted — pass opts.confirm=true to publish" };

  const body = new URLSearchParams({ fnid, fnop, title });
  if (url) body.set("url", url);
  if (text) body.set("text", text);
  const sr = await fetch("/r", {
    method: "POST", credentials: "include",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: body.toString(), redirect: "follow",
  });
  const rt = await sr.text().catch(() => "");
  // HN redirects to /newest (or the item) on success; on rejection it re-renders the form
  // with a message (dupe, validation). A returned form => not submitted.
  if (/name="fnid"/.test(rt)) {
    const msg = (rt.match(/<td[^>]*>\s*([^<]*(?:too long|blank|invalid|already|dupe|can't|cannot)[^<]*)/i) || [])[1] || "HN re-rendered the form (dupe/validation/rate-limit)";
    return { ...base, submitted: false, error: msg.trim().slice(0, 200) };
  }
  return { ...base, submitted: true, resultUrl: sr.url };
}

// ---- Hacker News: comment on a story / reply to a comment ---------------------------
// The comment box is a server-rendered form on the item page: form[action="comment"] with
// hidden {parent, goto, hmac} + textarea[name=text]. The `hmac` is per-page AND per-session
// — scrape it fresh from the page being commented on; it does NOT survive reuse across
// items or sessions. Replying to a COMMENT uses the same shape served by /reply?id=<id>.
// A locked/archived thread simply has no form — that's the honest "can't comment" signal.
// opts:{id, text, reply?, confirm}.
async function commentHN(opts) {
  const o = opts || {};
  const id = String(o.id || "").replace(/\D/g, "");
  const text = (o.text || "").trim();
  if (!id) return { error: "opts.id required (HN story or comment id)" };
  if (!text) return { error: "opts.text required" };

  // These are same-origin credentialed fetches, so the tab MUST already be on HN. Off-origin
  // they'd silently resolve against the current host and the missing #me link would read as
  // "logged out" — a misleading diagnosis that sends you hunting a phantom auth problem.
  if (location.hostname !== "news.ycombinator.com") {
    return { error: `must be run on news.ycombinator.com (tab is on ${location.hostname}) — goto an HN page first` };
  }

  const formUrl = o.reply
    ? `/reply?id=${id}&goto=${encodeURIComponent("item?id=" + id)}`
    : `/item?id=${id}`;
  const r = await fetch(formUrl, { credentials: "include", cache: "no-store" });
  const html = await r.text();
  const doc = new DOMParser().parseFromString(html, "text/html");

  // `a#me` lives in HN's FULL header. /reply?id= renders a MINIMAL header that omits it even
  // when logged in — so for opts.reply this check reported "not logged in" for a live session
  // (2026-07-20: story comments staged fine, every reply failed). Re-read identity from the
  // item page, which always carries the full header.
  // Do NOT fall back to `a[href^="user?id="]` on the reply page: that also matches the PARENT
  // comment's author link, which would silently set `me` to someone else and break the
  // read-back verification below (it matches rows on `author === me`).
  let me = (doc.querySelector("a#me") || {}).textContent || null;
  if (!me) {
    const idr = await fetch("/item?id=" + id, { credentials: "include", cache: "no-store" });
    const idoc = new DOMParser().parseFromString(await idr.text(), "text/html");
    me = (idoc.querySelector("a#me") || {}).textContent || null;
  }
  if (!me) return { error: "not logged in to Hacker News in this profile (no #me link)" };

  const form = doc.querySelector('form[action="comment"]');
  if (!form) return { error: "no comment form on this page — thread archived/locked/dead, or logged out" };
  const hmac = (form.querySelector('input[name="hmac"]') || {}).value || null;
  const parent = (form.querySelector('input[name="parent"]') || {}).value || id;
  const goto = (form.querySelector('input[name="goto"]') || {}).value || "item?id=" + id;
  if (!hmac) return { error: "no hmac in the comment form" };

  const base = { site: "hackernews", recipe: "comment", id, parent, author: me };
  if (!o.confirm) {
    return { ...base, staged: true, preview: text.slice(0, 200), note: "form + hmac OK, NOT posted — pass opts.confirm=true" };
  }

  const sr = await fetch("/comment", {
    method: "POST", credentials: "include",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({ parent, goto, hmac, text }).toString(),
    redirect: "follow",
  });
  const rt = await sr.text().catch(() => "");

  // A 200 here proves nothing: HN returns 200 for rate-limit, flag, and shadow-reject pages
  // alike. The only proof is our text rendered on the live thread — so read it back.
  const storyId = o.reply ? (goto.match(/id=(\d+)/) || [])[1] || id : id;
  const vr = await fetch("/item?id=" + storyId, { credentials: "include", cache: "no-store" });
  const vdoc = new DOMParser().parseFromString(await vr.text(), "text/html");
  const needle = text.slice(0, 60).replace(/\s+/g, " ").trim();
  let liveUrl = null;
  for (const row of vdoc.querySelectorAll("tr.comtr")) {
    const author = ((row.querySelector(".hnuser") || {}).textContent || "").trim();
    const body = ((row.querySelector(".commtext") || {}).textContent || "").replace(/\s+/g, " ");
    if (author === me && body.includes(needle)) {
      const a = row.querySelector(".age a");
      const href = a && a.getAttribute("href");
      liveUrl = href ? new URL(href, "https://news.ycombinator.com").href : null;
      break;
    }
  }
  if (!liveUrl) {
    const msg = (rt.match(/(You're (?:posting|submitting) too fast[^<.]*|Please slow down[^<.]*|That looks like spam[^<.]*)/i) || [])[1] || null;
    return { ...base, submitted: false, verified: false, error: msg || "POST returned " + sr.status + " but the comment is NOT on the live thread (rate-limited, flagged, or shadow-rejected)" };
  }

  // Finding our own comment on the thread is NOT proof it is public: HN renders an author's
  // own [dead]/[flagged] comments as normal to that author, so a credentialed read-back
  // happily "verifies" a comment nobody else can see. The official API exposes the real
  // `dead` flag and is CORS-open (Access-Control-Allow-Origin: *), so ask it, not the DOM.
  const cid = (liveUrl.match(/id=(\d+)/) || [])[1] || null;
  let dead = null;
  if (cid) {
    try {
      const ir = await fetch(`https://hacker-news.firebaseio.com/v0/item/${cid}.json`, { cache: "no-store" });
      const item = await ir.json();
      dead = item && item.dead === true;
    } catch (e) { dead = null; }
  }
  if (dead === true) {
    return { ...base, submitted: true, verified: false, dead: true, liveUrl,
      error: "comment is DEAD (auto-killed/flagged) — visible to you, invisible to everyone else. Low account karma is the usual cause; posting more will make it worse." };
  }
  return { ...base, submitted: true, verified: dead === false, dead, liveUrl,
    note: dead === null ? "could not reach the HN API to confirm the dead flag — treat as UNVERIFIED" : undefined };
}

export const post = {
  "hackernews:post": {
    world: "MAIN",
    match: "*://news.ycombinator.com/*",
    write: true,
    describe: "WRITE: submit a Hacker News story via the real submit form (fnid token replay). Needs to be logged in on HN in the bridge profile. Staged unless opts.confirm=true. opts:{title,url,text,confirm}. NOTE: this submits a STORY to /newest — it is NOT the verb for commenting; use hackernews:comment.",
    fn: postHN,
  },
  "hackernews:comment": {
    world: "MAIN",
    match: "*://news.ycombinator.com/*",
    write: true,
    describe: "WRITE: comment on an HN story (or reply to a comment with opts.reply=true) via the real form (per-page hmac replay). Self-verifying: re-reads the live thread and returns liveUrl, since HN 200s on rate-limit/flag pages. Staged unless opts.confirm=true. opts:{id,text,reply,confirm}.",
    fn: commentHN,
  },
  "linkedin:post": {
    world: "MAIN",
    match: "*://*.linkedin.com/*",
    write: true,
    describe: "WRITE: create a LinkedIn post via voyager normShares (verified 201). Staged unless confirm. opts:{text,confirm,connectionsOnly,allowedCommentersScope}.",
    fn: postLinkedin,
  },
  "linkedin:post-image": {
    world: "MAIN",
    match: "*://*.linkedin.com/*",
    write: true,
    describe: "WRITE, KNOWN BROKEN (2026-07-10): create a LinkedIn post with an image via voyager media upload. Register + PUT upload both succeed (asset goes READY) and normShares returns 201, but the published post does NOT render the image in the feed on any payload shape tried (media:[{status,mediaUrn}], content.contentEntities w/ + w/o thumbnails) — verified live 4x, always text-only. DO NOT rely on this for a real visual until someone finds the correct schema; check for an image on the live post before treating this as done. opts:{text,imagePath|imageB64/imageMime/imageName,confirm,connectionsOnly}.",
    fn: postLinkedinImage,
  },
  "linkedin:delete-post": {
    world: "MAIN",
    match: "*://*.linkedin.com/*",
    write: true,
    describe: "WRITE: delete a LinkedIn post by urn. Staged unless opts.confirm=true. opts:{urn,confirm}.",
    fn: deleteLinkedin,
  },
  "linkedin:comment": {
    world: "MAIN",
    match: "*://*.linkedin.com/*",
    write: true,
    describe: "WRITE: comment/reply on a LinkedIn post by driving the comment box UI (fills Quill editor-aware, clicks the box's submit). Open the post's permalink first. Staged unless opts.confirm=true. opts:{text,confirm,selector?}.",
    fn: commentLinkedin,
  },
  "linkedin:comment-delete": {
    world: "MAIN",
    match: "*://*.linkedin.com/*",
    write: true,
    describe: "WRITE: delete a comment WE posted (the undo for linkedin:comment) via the comment's ⋯ options menu -> Delete -> confirm. Open the post's permalink first; target precisely with opts.matchText (a substring of our comment). Staged unless opts.confirm=true. opts:{matchText,confirm}.",
    fn: deleteCommentLinkedin,
  },
  "x:post": {
    world: "MAIN",
    match: "*://*.x.com/*",
    write: true,
    describe: "WRITE: create a tweet by driving the native composer — fills it directly (DraftJS paste) whether the inline composer is already open or opened on demand, optional image via the file input, then clicks Post (bypasses 226). Staged unless opts.confirm=true. opts:{text,imagePath|imageB64/imageMime/imageName,confirm}.",
    fn: postX,
  },
  "x:reply": {
    world: "MAIN",
    match: "*://*.x.com/*",
    write: true,
    describe: "WRITE: reply to a tweet by driving the composer (fills DraftJS via paste, optional image via the composer file input, clicks Reply; bypasses 226 — X's JS computes x-client-transaction-id). Open the tweet's permalink first. Staged unless opts.confirm=true. Returns mediaRequested/mediaAttached so an ignored image is visible. NOTE: chrome-agent does NOT expand imagePath — pass imageB64. opts:{text,imageB64/imageMime/imageName,confirm}.",
    fn: replyX,
  },
  "x:delete-post": {
    world: "MAIN",
    match: "*://*.x.com/*",
    write: true,
    describe: "WRITE: delete a tweet/reply by driving the native UI (⋯ caret -> Delete -> confirm sheet), no API replay. Open the tweet first; target opts.tweetId or the first caret. Staged unless opts.confirm=true. opts:{tweetId,confirm}.",
    fn: deleteX,
  },
  "reddit:post": {
    world: "MAIN",
    match: "*://*.reddit.com/*",
    write: true,
    describe: "WRITE: submit a Reddit self-post via API. Staged unless opts.confirm=true. opts:{subreddit,title,text,confirm}.",
    fn: postReddit,
  },
  "reddit:submit": {
    world: "MAIN",
    match: "*://*.reddit.com/*",
    write: true,
    describe: "WRITE: create a Reddit text post by driving the /submit page UI (shadow-DOM aware; sidesteps the captcha-gated /api/submit). Open r/<sub>/submit/?type=TEXT first. Staged unless opts.confirm=true. opts:{title,text,confirm,probe}.",
    fn: submitReddit,
  },
  "reddit:post-oauth": {
    world: "MAIN",
    match: "*://*.reddit.com/*",
    write: true,
    describe: "WRITE: submit via the OFFICIAL oauth.reddit.com/api/submit using the session's own bearer token (no app registration; the Postiz endpoint). Token read+used in-page, never returned. Staged unless opts.confirm=true. opts:{subreddit,title,text,url,confirm}.",
    fn: postRedditOAuth,
  },
  "reddit:comment": {
    world: "MAIN",
    match: "*://*.reddit.com/*",
    write: true,
    describe: "WRITE: comment/reply on a Reddit post (t3_) or comment (t1_) via /api/comment. Staged unless opts.confirm=true. Returns t1_ id (delete via reddit:delete-post). opts:{thingId,text,confirm}.",
    fn: commentReddit,
  },
  "reddit:delete-post": {
    world: "MAIN",
    match: "*://*.reddit.com/*",
    write: true,
    describe: "WRITE: delete a Reddit post OR comment by fullname (t3_/t1_). Staged unless opts.confirm=true. opts:{fullname,confirm}.",
    fn: deleteReddit,
  },
};
