// search.js — scrape the results page of a search engine (ISOLATED world).
// Open/navigate the engine's results URL in a tab, then run the matching recipe.
// Self-contained fns (serialized into the tab) — inline helpers.

async function scrapeGoogle(opts) {
  const max = (opts && opts.max) || 20;
  const clean = (s) => (s || "").replace(/\s+/g, " ").trim();
  const out = [];
  for (const block of document.querySelectorAll("div.g, div[data-hveid] div.tF2Cxc, div.MjjYud")) {
    const a = block.querySelector('a[href^="http"]');
    const h = block.querySelector("h3");
    if (!a || !h) continue;
    const url = a.href;
    if (out.some((r) => r.url === url)) continue;
    const snip = block.querySelector('div[data-sncf], .VwiC3b, .lEBKkf');
    out.push({ title: clean(h.innerText), url, snippet: clean(snip ? snip.innerText : "") });
    if (out.length >= max) break;
  }
  return { site: "google", recipe: "search", query: new URLSearchParams(location.search).get("q"), count: out.length, results: out };
}

async function scrapeBing(opts) {
  const max = (opts && opts.max) || 20;
  const clean = (s) => (s || "").replace(/\s+/g, " ").trim();
  const out = [];
  for (const li of document.querySelectorAll("#b_results > li.b_algo")) {
    const a = li.querySelector("h2 a");
    if (!a) continue;
    const snip = li.querySelector(".b_caption p, .b_algoSlug");
    out.push({ title: clean(a.innerText), url: a.href, snippet: clean(snip ? snip.innerText : "") });
    if (out.length >= max) break;
  }
  return { site: "bing", recipe: "search", query: new URLSearchParams(location.search).get("q"), count: out.length, results: out };
}

async function scrapeDuckDuckGo(opts) {
  const max = (opts && opts.max) || 20;
  const clean = (s) => (s || "").replace(/\s+/g, " ").trim();
  const out = [];
  const nodes = document.querySelectorAll('article[data-testid="result"], .result, li[data-layout="organic"]');
  for (const n of nodes) {
    const a = n.querySelector('a[data-testid="result-title-a"], a.result__a, h2 a');
    if (!a) continue;
    const snip = n.querySelector('[data-result="snippet"], .result__snippet');
    out.push({ title: clean(a.innerText), url: a.href, snippet: clean(snip ? snip.innerText : "") });
    if (out.length >= max) break;
  }
  return { site: "duckduckgo", recipe: "search", query: new URLSearchParams(location.search).get("q"), count: out.length, results: out };
}

export const search = {
  "search:google": {
    world: "ISOLATED",
    match: "*://www.google.com/search*",
    describe: "Scrape organic results (title/url/snippet) from a Google results page. opts:{max}.",
    fn: scrapeGoogle,
  },
  "search:bing": {
    world: "ISOLATED",
    match: "*://www.bing.com/search*",
    describe: "Scrape results from a Bing results page. opts:{max}.",
    fn: scrapeBing,
  },
  "search:duckduckgo": {
    world: "ISOLATED",
    match: "*://*.duckduckgo.com/*",
    describe: "Scrape results from a DuckDuckGo results page. opts:{max}.",
    fn: scrapeDuckDuckGo,
  },
};
