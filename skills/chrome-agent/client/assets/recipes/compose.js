// compose.js — editor-aware composer-fill recipe (#2503, lesson #3893).
//
// The old generic fill used innerText / execCommand('insertText'). Rich editors keep
// their OWN model and reject or mangle direct DOM mutation:
//   • Quill (LinkedIn .ql-editor) — scrambles paragraphs / drops blocks
//   • DraftJS (X)                 — ignores it; the React model never updates
// This recipe drives each editor the way it actually accepts input — its own instance
// API where reachable, else REAL beforeinput InputEvent sequences (insertText +
// insertParagraph), which the editors' own input handlers process correctly.
//
// IMPORTANT: a recipe fn is shipped to the page via chrome.scripting.executeScript
// ({func}), which SERIALIZES the function — it cannot close over imports. So the
// adapter logic is INLINED inside fn (self-contained). The pure helpers are also
// exported below for unit testing; fn does not depend on those exports at runtime.
//
// NOTE: LinkedIn (linkedin:post) and X (x:post) auto-post via their API / intent
// pre-fill and do NOT need this. `compose:fill` is the editor-aware path for any
// OTHER rich composer (Facebook, generic contenteditable, in-page reply boxes).

// ---- PURE helpers (exported for tests; mirrored inside fn for injection) ----
export function composeOps(text) {
  const ops = [];
  const lines = String(text == null ? "" : text).split("\n");
  lines.forEach(function (line, i) {
    if (i > 0) ops.push({ t: "para" });
    if (line.length) ops.push({ t: "text", data: line });
  });
  return ops;
}

export function detectEditor(el) {
  if (!el) return "none";
  var cl = (el.className || "") + "";
  var has = function (sel) { return typeof el.closest === "function" && el.closest(sel); };
  if (cl.indexOf("ql-editor") >= 0 || has(".ql-editor") || has(".ql-container")) return "quill";
  if (cl.indexOf("public-DraftEditor-content") >= 0 || has(".DraftEditor-root") ||
      (typeof el.querySelector === "function" && el.querySelector("[data-contents]"))) return "draftjs";
  if (el.isContentEditable || (el.getAttribute && el.getAttribute("contenteditable") === "true")) return "plain";
  return "none";
}

// composeFill — the self-contained page function. Runs in MAIN world (needs the
// Quill instance global + the editors' own handlers). opts:{selector?,text,confirm?}.
function composeFill(opts) {
  var o = opts || {};
  var text = (o.text == null ? "" : String(o.text));
  if (!text) return { ok: false, error: "opts.text required" };

  // --- locate the target composer ---
  var el = null;
  if (o.selector) el = document.querySelector(o.selector);
  if (!el) {
    // a focused/active contenteditable, else the first plausible rich composer
    var a = document.activeElement;
    if (a && (a.isContentEditable || a.getAttribute && a.getAttribute("contenteditable") === "true")) el = a;
  }
  if (!el) el = document.querySelector('.ql-editor, .public-DraftEditor-content, [data-testid="tweetTextarea_0"], [contenteditable="true"]');
  if (!el) return { ok: false, error: "no composer found (pass opts.selector)" };

  // --- pure ops (inlined: executeScript can't see module exports) ---
  function ops(t) {
    var out = [], lines = String(t).split("\n");
    lines.forEach(function (line, i) { if (i > 0) out.push({ t: "para" }); if (line.length) out.push({ t: "text", data: line }); });
    return out;
  }
  function detect(node) {
    if (!node) return "none";
    var cl = (node.className || "") + "";
    var has = function (sel) { return typeof node.closest === "function" && node.closest(sel); };
    if (cl.indexOf("ql-editor") >= 0 || has(".ql-editor") || has(".ql-container")) return "quill";
    if (cl.indexOf("public-DraftEditor-content") >= 0 || has(".DraftEditor-root") ||
        (typeof node.querySelector === "function" && node.querySelector("[data-contents]"))) return "draftjs";
    if (node.isContentEditable || (node.getAttribute && node.getAttribute("contenteditable") === "true")) return "plain";
    return "none";
  }
  function fire(node, type, init) {
    try { node.dispatchEvent(new InputEvent(type, Object.assign({ bubbles: true, cancelable: true }, init))); } catch (e) {}
  }
  function applyBeforeInput(node, t) {
    node.focus();
    ops(t).forEach(function (op) {
      if (op.t === "para") { fire(node, "beforeinput", { inputType: "insertParagraph" }); fire(node, "input", { inputType: "insertParagraph" }); }
      else { fire(node, "beforeinput", { inputType: "insertText", data: op.data }); fire(node, "input", { inputType: "insertText", data: op.data }); }
    });
  }

  var editor = detect(el), method;
  try {
    if (editor === "quill") {
      var container = (el.closest && (el.closest(".ql-container") || el.closest(".ql-editor"))) || el;
      var q = (window.Quill && window.Quill.find && window.Quill.find(container)) || container.__quill ||
              (el.closest && el.closest(".ql-container") && el.closest(".ql-container").__quill);
      if (q && typeof q.setText === "function") { q.setText(text + "\n"); method = "quill:instance"; }
      else { applyBeforeInput((el.querySelector && el.querySelector(".ql-editor")) || el, text); method = "quill:beforeinput"; }
    } else if (editor === "draftjs") {
      var target = (el.querySelector && el.querySelector(".public-DraftEditor-content")) || el;
      target.focus();
      try {
        var dt = new DataTransfer(); dt.setData("text/plain", text);
        target.dispatchEvent(new ClipboardEvent("paste", { bubbles: true, cancelable: true, clipboardData: dt }));
        method = "draftjs:paste";
      } catch (e) { applyBeforeInput(target, text); method = "draftjs:beforeinput"; }
    } else if (editor === "plain") {
      el.focus();
      ops(text).forEach(function (op) {
        if (op.t === "para") { try { document.execCommand("insertParagraph"); } catch (e) {} }
        else { try { document.execCommand("insertText", false, op.data); } catch (e) {} }
      });
      method = "plain:execCommand";
    } else {
      return { ok: false, editor: editor, error: "target is not a recognised rich editor" };
    }
  } catch (e) {
    return { ok: false, editor: editor, error: String(e && e.message || e) };
  }
  var rendered = (el.innerText != null ? el.innerText : "").trim();
  return { ok: true, editor: editor, method: method, rendered: rendered.slice(0, 2000), staged: true,
           note: "Filled the composer editor-aware. Review, then click the site's Post/Send — this recipe only fills, never publishes." };
}

export const compose = {
  "compose:fill": {
    world: "MAIN", // needs the Quill instance global + the editors' own input handlers
    match: "*://*/*",
    write: true,
    describe: "WRITE (fill-only, never publishes): editor-aware fill of a rich composer — Quill (instance API), DraftJS (synthetic paste), plain contenteditable (execCommand). For composers WITHOUT an API recipe (use linkedin:post / x:post for those). opts:{text, selector?}.",
    fn: composeFill,
  },
};
