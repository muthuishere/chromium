// recipes-path.mjs — where the browser-research recipe registry lives, resolved in ONE place.
//
// The path used to be hardcoded to the owner's checkout in two files, with two different env var
// names. On a server that means `chrome-agent recipes` reports 6 verbs instead of 48 and every
// `recipe <site:name>` fails — found by running the CLI inside an Ubuntu container (2026-09-12).
//
// Order, highest first — the same shape as the site definitions (ADR 0004):
//   $CHROME_AGENT_RECIPES / $BR_REGISTRY   explicit override (a file OR a directory)
//   ~/.config/chrome-agent/recipes/        VENDORED copy — what `recipes vendor` installs
//   the owner's browser-research checkout  the development source
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const asRegistry = (p) => {
  if (!p) return null;
  try {
    if (fs.statSync(p).isDirectory()) {
      const r = path.join(p, 'registry.js');
      return fs.existsSync(r) ? r : null;
    }
    return fs.existsSync(p) ? p : null;
  } catch { return null; }
};

export const VENDOR_DIR = path.join(os.homedir(), '.config', 'chrome-agent', 'recipes');
export const SOURCE_DIR = path.join(
  os.homedir(),
  'muthu/deemwarworkspace/browser-research-workspace/browser-research',
  'extensions/browser-research/src/recipes',
);

export function resolveRegistry() {
  for (const cand of [process.env.CHROME_AGENT_RECIPES, process.env.BR_REGISTRY, VENDOR_DIR, SOURCE_DIR]) {
    const hit = asRegistry(cand);
    if (hit) return hit;
  }
  return null;
}
