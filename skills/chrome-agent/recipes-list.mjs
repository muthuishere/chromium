// recipes-list.mjs — the ONE source of truth for "what verbs exist", for humans and machines.
//
// WHY (2026-09-12): apl's playbook generator used to regex-parse the browser-research
// registry.js to learn the verb list — reaching across a repo boundary into someone else's
// source layout, breaking on any formatting change, and inferring "is this a write?" from the
// FILE the key happened to live in (post.js). chrome-agent already knows the answer: it is the
// thing that runs them. So it answers, in JSON.
//
// It also reports the verbs chrome-agent implements ITSELF. The registry has no reaction recipe
// for any site, but this CLI has `linkedin like`, `x like`, `x repost`, `reddit upvote` as
// self-verifying DOM actions. A generator that saw only the registry concluded reacting was
// impossible and wrote that into a playbook. Both sources, one list, each entry naming its own.
//
//   node recipes-list.mjs            human list (unchanged format)
//   node recipes-list.mjs --json     [{key, site, verb, describe, write, world, source, cli}]

import os from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const REGISTRY =
  process.env.CHROME_AGENT_RECIPES ||
  path.join(
    os.homedir(),
    'muthu/deemwarworkspace/browser-research-workspace/browser-research',
    'extensions/browser-research/src/recipes/registry.js',
  );

// chrome-agent's own verbs. They are NOT in the registry — they are DOM actions implemented in
// the bash CLI, each self-verifying by an attribute flip (aria-label / data-testid / aria-pressed)
// and staged until --confirm, exactly like a registry write.
const BUILTIN = [
  // [key, describe, cli, write]
  ['linkedin:like',   'like the post at <url> (or the first feed post) — verified by the reaction button flipping state', 'chrome-agent linkedin like <url> --confirm', true],
  ['x:like',          'like the tweet at <url> — verified by data-testid flipping like -> unlike',                        'chrome-agent x like <url> --confirm', true],
  ['x:repost',        'repost the tweet at <url> — verified by data-testid flipping retweet -> unretweet',                'chrome-agent x repost <url> --confirm', true],
  ['reddit:upvote',   'upvote the post at <url> — verified by aria-pressed becoming true',                                'chrome-agent reddit upvote <url> --confirm', true],
  // HN could write and not read, while its own traps file says "open the item and read it back".
  ['hackernews:top',  'read the HN front page: title, points, comments, item link',                                       'chrome-agent hackernews top [n]', false],
  ['hackernews:item', 'read a thread back by url or id — comments with depth, and the [flagged]/[dead] a 200 hides',      'chrome-agent hackernews item <url-or-id>', false],
];

async function load() {
  const out = [];
  let registry = null;
  try {
    registry = (await import(pathToFileURL(REGISTRY).href)).RECIPES;
  } catch (e) {
    if (process.argv.includes('--json')) {
      // A machine reader must be told the registry is missing, not handed a short list that
      // looks complete. The builtins are still real, so ship them and name what is absent.
      process.stderr.write(`recipes: registry not loadable at ${REGISTRY}: ${e.message}\n`);
    } else {
      process.stderr.write(`recipes: registry not loadable at ${REGISTRY}\n`);
    }
  }
  for (const key of Object.keys(registry || {}).sort()) {
    const r = registry[key];
    const [site, verb] = [key.slice(0, key.indexOf(':')), key.slice(key.indexOf(':') + 1)];
    out.push({
      key, site, verb,
      describe: r.describe || '',
      write: !!r.write,
      world: r.world || '',
      source: 'registry',
      cli: `chrome-agent recipe ${key}`,
    });
  }
  for (const [key, describe, cli, write] of BUILTIN) {
    const [site, verb] = [key.slice(0, key.indexOf(':')), key.slice(key.indexOf(':') + 1)];
    out.push({ key, site, verb, describe, write, world: 'main', source: 'chrome-agent', cli });
  }
  out.sort((a, b) => a.key.localeCompare(b.key));
  return out;
}

const all = await load();
if (process.argv.includes('--json')) {
  process.stdout.write(JSON.stringify(all, null, 2) + '\n');
} else {
  for (const r of all) {
    console.log(' ', r.key, '—', r.describe + (r.source === 'chrome-agent' ? '  [chrome-agent verb]' : ''));
  }
}
