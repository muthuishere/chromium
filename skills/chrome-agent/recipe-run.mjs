#!/usr/bin/env node
// recipe-run.mjs — resolve a browser-research recipe by key and emit an injectable JS
// payload that runs its (self-contained, possibly async) fn with opts, and stashes the
// JSON result on a global token so the caller can poll it via a sync eval (evalAsync).
//
// This is the bridge that lets the UNDETECTABLE chromium fork run browser-research's
// VERIFIED recipes (linkedin/x/youtube/reddit/… reads AND the staged-safe post.js writes)
// UNCHANGED — no LinkedIn-special-casing, no DOM grinding, no fork changes.
//
// usage: node recipe-run.mjs <site:name> <token> [opts-json]
//   prints the JS to inject; the wrapper fires it then polls window[token].
import { pathToFileURL } from "node:url";

const REG =
  process.env.BR_REGISTRY ||
  process.env.HOME +
    "/muthu/deemwarworkspace/browser-research-workspace/browser-research/extensions/browser-research/src/recipes/registry.js";

const [, , key, token, optsJson] = process.argv;
if (!key || !token) {
  console.error("usage: recipe-run.mjs <site:name> <token> [opts-json]");
  process.exit(2);
}

const mod = await import(pathToFileURL(REG).href);
const RECIPES = mod.RECIPES || {};
const entry = RECIPES[key];
if (!entry || typeof entry.fn !== "function") {
  console.error(
    `no recipe "${key}". known: ${Object.keys(RECIPES).sort().join(", ")}`
  );
  process.exit(3);
}

const opts = optsJson ? optsJson : "undefined";
// Emit an async-function BODY (uses await + return) that the caller runs through the fork's
// native EVALASYNC verb. Await handles both sync + async recipe fns; a throw propagates and
// EVALASYNC acks it as {ok:false,error}. No stash-on-global + poll dance. `token` is still
// accepted for signature compatibility but is no longer needed.
void token;
const payload = `const __fn=(${entry.fn.toString()});
const __r=await __fn(${opts});
return (__r===undefined?{ok:true}:__r);`;
process.stdout.write(payload);
