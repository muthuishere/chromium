// registry.js — the single source of truth for named recipes.
//
// A recipe entry: { world: "ISOLATED"|"MAIN", match, describe, fn }
//   - world "ISOLATED": runs in the extension content-script world — shares the page DOM
//     but is NOT subject to the page CSP (use for DOM scraping on strict-CSP sites).
//   - world "MAIN": runs in the real page window (use only when you must touch page globals
//     like window.fetch, or do a same-origin credentialed fetch that rides page cookies).
//
// Add a new site = add a file under recipes/ exporting an object, then spread it here.

import { linkedin } from "./linkedin.js";
import { x } from "./x.js";
import { facebook } from "./facebook.js";
import { instagram } from "./instagram.js";
import { reddit } from "./reddit.js";
import { youtube } from "./youtube.js";
import { generic } from "./generic.js";
import { search } from "./search.js";
import { research } from "./research.js";
import { gsc } from "./gsc.js";
import { post } from "./post.js";
import { compose } from "./compose.js";
import { chatgpt } from "./chatgpt.js";
import { capture } from "./capture.js";

// Built-in recipes — a plain, SYNCHRONOUS module export. Critically, there is NO top-level
// `await` here: this module is in background.js's static import graph, and an MV3 service
// worker must register its event listeners during synchronous first evaluation. A top-level
// await (e.g. `await import(...)`) upstream defers background.js's listener registration past
// a microtask, so Chrome never wires up onInstalled/onStartup/onAlarm and the worker never
// polls (symptom: SW "Inactive" + Errors, extension never answers). See background.js.
export const RECIPES = {
  ...linkedin,
  ...x,
  ...facebook,
  ...instagram,
  ...reddit,
  ...youtube,
  ...generic,
  ...search,
  ...research,
  ...gsc,
  ...post, // WRITE recipes — staged unless opts.confirm=true
  ...compose, // editor-aware composer-fill (#2503) — fill-only, never publishes
  ...chatgpt, // crawl the logged-in ChatGPT session's history via its backend API (read-only)
  ...capture, // CAPTURE: instrument page fetch/XHR to learn the real write calls (arm/dump/clear)
};

// User recipes (drop-in userscripts synced into ./user/registry.generated.js, git-ignored)
// load LAZILY — a dynamic import inside a handler, never at module top — so the SW's listener
// registration stays synchronous. Cached after first load; missing file degrades gracefully
// (built-ins still work). This is the MV3-correct way to fold in an optional async module.
let _userLoaded = false;
let _userRecipes = {};
async function loadUserRecipes() {
  if (_userLoaded) return _userRecipes;
  _userLoaded = true;
  try {
    const m = await import("./user/registry.generated.js");
    _userRecipes = (m && m.USER_RECIPES) || {};
  } catch { _userRecipes = {}; }
  return _userRecipes;
}

// The full catalog (built-ins + user recipes). User recipes last, so one can shadow a built-in.
export async function getRecipes() {
  return { ...RECIPES, ...(await loadUserRecipes()) };
}

export function describeRecipes(map) {
  return Object.entries(map).map(([name, r]) => ({
    name,
    world: r.world,
    write: !!r.write,
    match: r.match,
    describe: r.describe,
  }));
}
