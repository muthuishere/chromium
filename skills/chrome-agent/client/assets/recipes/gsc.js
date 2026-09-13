// gsc.js — Google Search Console drilldowns (ISOLATED world, CSP-immune).

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const clean = (s) => (s || "").replace(/\s+/g, " ").trim();

// Scrape the example-URL table currently shown on a GSC drilldown view.
function scrapeExampleUrls() {
  const urls = new Set();
  for (const a of document.querySelectorAll('a[href^="http"]')) {
    const t = clean(a.innerText);
    if (/^https?:\/\/(www\.)?deemwar\.com/.test(t)) urls.add(t);
  }
  // Fallback: table cells that look like URLs.
  for (const td of document.querySelectorAll("td,div,span")) {
    const t = clean(td.innerText);
    if (/^https?:\/\/(www\.)?deemwar\.com\/\S*$/.test(t) && t.length < 200) urls.add(t);
  }
  return [...urls];
}

// Click the summary row whose text contains opts.reason, wait, scrape example URLs.
async function drilldown(opts) {
  const o = opts || {};
  const reason = o.reason || "";
  if (!reason) return { error: "opts.reason required (substring of the row label)" };
  const rows = [...document.querySelectorAll("tr")];
  const row = rows.find((r) => clean(r.innerText).toLowerCase().includes(reason.toLowerCase()));
  if (!row) return { error: "row not found for reason: " + reason, available: rows.map((r) => clean(r.innerText)).filter(Boolean) };
  const clickable = row.querySelector("td") || row;
  clickable.click();
  await sleep(3500);
  const examples = scrapeExampleUrls();
  return {
    site: "gsc",
    recipe: "drilldown",
    reason,
    url: location.href,
    heading: clean((document.querySelector("h1,h2") || {}).innerText),
    count: examples.length,
    examples,
  };
}

// Just scrape whatever example URLs are on the current view (after a manual/earlier click).
async function currentUrls() {
  const examples = scrapeExampleUrls();
  return { site: "gsc", recipe: "current-urls", url: location.href, count: examples.length, examples };
}

export const gsc = {
  "gsc:drilldown": {
    world: "ISOLATED",
    match: "*://search.google.com/*",
    describe: "Click a 'why not indexed' reason row and scrape example deemwar URLs. opts:{reason}.",
    fn: drilldown,
  },
  "gsc:current-urls": {
    world: "ISOLATED",
    match: "*://search.google.com/*",
    describe: "Scrape example deemwar URLs from the current GSC drilldown view.",
    fn: currentUrls,
  },
};
