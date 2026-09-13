// research.js — generic, any-site research extractors (ISOLATED world).
// "article" = readability-lite: find the densest main content and return clean text.

async function extractArticle() {
  const clean = (s) => (s || "").replace(/\s+/g, " ").trim();
  // candidate containers, scored by text length minus link density (nav/boilerplate noise)
  const cands = [...document.querySelectorAll("article, main, [role=main], .post, .article, .content, #content, .entry-content")];
  if (document.body) cands.push(document.body);
  let best = null, bestScore = 0;
  for (const el of cands) {
    const text = el.innerText || "";
    const linkText = [...el.querySelectorAll("a")].reduce((n, a) => n + (a.innerText || "").length, 0);
    const score = text.length - linkText * 2; // penalise link-heavy (menus, related)
    if (score > bestScore) { bestScore = score; best = el; }
  }
  const root = best || document.body;
  const headings = [...root.querySelectorAll("h1,h2,h3")].map((h) => clean(h.innerText)).filter(Boolean).slice(0, 30);
  const paras = [...root.querySelectorAll("p, li")]
    .map((p) => clean(p.innerText))
    .filter((t) => t.length > 30);
  const meta = (name) => {
    const m = document.querySelector(`meta[property="${name}"], meta[name="${name}"]`);
    return m ? m.getAttribute("content") : null;
  };
  return {
    site: "generic",
    recipe: "article",
    url: location.href,
    title: clean(document.querySelector("h1")?.innerText) || document.title,
    byline: meta("article:author") || meta("author"),
    published: meta("article:published_time") || meta("date"),
    description: meta("og:description") || meta("description"),
    headings,
    text: paras.join("\n\n").slice(0, 40000),
    wordCount: paras.join(" ").split(/\s+/).filter(Boolean).length,
  };
}

async function extractMeta() {
  const all = {};
  for (const m of document.querySelectorAll("meta[property], meta[name]")) {
    const k = m.getAttribute("property") || m.getAttribute("name");
    const v = m.getAttribute("content");
    if (k && v && !all[k]) all[k] = v;
  }
  const ld = [...document.querySelectorAll('script[type="application/ld+json"]')]
    .map((s) => { try { return JSON.parse(s.textContent); } catch (e) { return null; } })
    .filter(Boolean);
  return { site: "generic", recipe: "meta", url: location.href, title: document.title, meta: all, jsonLd: ld };
}

export const research = {
  "generic:article": {
    world: "ISOLATED",
    match: "*://*/*",
    describe: "Readability-lite: extract the main article text, headings, byline, date from any page.",
    fn: extractArticle,
  },
  "generic:meta": {
    world: "ISOLATED",
    match: "*://*/*",
    describe: "All <meta> tags + JSON-LD structured data from the page.",
    fn: extractMeta,
  },
};
