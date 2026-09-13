// x.js — X / Twitter recipes (DOM scrape, ISOLATED world = CSP-immune).
// Self-contained fns (serialized into the tab). Inline all helpers.

async function scrapeTimeline(opts) {
  const o = opts || {};
  const PASSES = o.passes || 10;
  // FLOOR, not a default (owner 2026-07-26: "if you scroll fast on x or reddit they will ban
  // atyleast a 1 second delay scrolling and liking"). The default was already >=1200ms, but
  // `o.delay` let a caller pass 100 and the ban risk is the caller's mistake to make, not ours.
  // Math.max makes 1000ms the minimum no caller can go under.
  const DELAY = Math.max(1000, o.delay || 1200);
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const clean = (el) => (el ? el.innerText.replace(/\s+/g, " ").trim() : null);

  const collect = () => {
    const arts = [...document.querySelectorAll('article[data-testid="tweet"], article[role="article"]')];
    const out = new Map();
    for (const a of arts) {
      const textEl = a.querySelector('[data-testid="tweetText"]');
      const text = clean(textEl);
      const linkEl = a.querySelector('a[href*="/status/"]');
      const href = linkEl ? linkEl.getAttribute("href") : null;
      const id = href ? (href.match(/status\/(\d+)/) || [])[1] : null;
      const handle = clean(a.querySelector('div[dir="ltr"] span'));
      const time = a.querySelector("time");
      const key = id || (text || "").slice(0, 40);
      if (!key || out.has(key) || !text) continue;
      out.set(key, {
        id,
        text: text.slice(0, 2000),
        handle,
        time: time ? time.getAttribute("datetime") : null,
        url: href ? "https://x.com" + href.split("?")[0] : null,
      });
    }
    return out;
  };

  const acc = new Map();
  for (let i = 0; i < PASSES; i++) {
    for (const [k, v] of collect()) if (!acc.has(k)) acc.set(k, v);
    window.scrollTo(0, document.body.scrollHeight);
    await sleep(DELAY);
  }
  const posts = [...acc.values()];
  return {
    site: "x",
    recipe: "timeline",
    url: location.href,
    capturedAt: new Date().toISOString(),
    postCount: posts.length,
    posts,
  };
}

export const x = {
  "x:timeline": {
    world: "ISOLATED",
    match: "*://*.x.com/*",
    describe: "Scrape visible tweets on the current X timeline / profile (auto-scrolls).",
    fn: scrapeTimeline,
  },
  "x:timeline-twitter": {
    world: "ISOLATED",
    match: "*://*.twitter.com/*",
    describe: "Same as x:timeline but for the legacy twitter.com host.",
    fn: scrapeTimeline,
  },
};
