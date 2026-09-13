// facebook.js — Facebook recipes (DOM scrape, ISOLATED world = CSP-immune).
// FB obfuscates class names, so we lean on role/aria landmarks and text density.

async function scrapeFeed(opts) {
  const o = opts || {};
  const PASSES = o.passes || 10;
  // FLOOR, not a default (owner 2026-07-26: "if you scroll fast on x or reddit they will ban
  // atyleast a 1 second delay scrolling and liking"). The default was already >=1200ms, but
  // `o.delay` let a caller pass 100 and the ban risk is the caller's mistake to make, not ours.
  // Math.max makes 1000ms the minimum no caller can go under.
  const DELAY = Math.max(1000, o.delay || 1400);
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const clean = (el) => (el ? el.innerText.replace(/\s+/g, " ").trim() : null);

  const collect = () => {
    const arts = [...document.querySelectorAll('div[role="article"]')];
    const out = new Map();
    for (const a of arts) {
      // expand "See more" within the post
      a.querySelectorAll('div[role="button"]').forEach((b) => {
        if (/^see more$/i.test((b.innerText || "").trim())) { try { b.click(); } catch (e) {} }
      });
      const text = clean(a.querySelector('div[data-ad-preview="message"], div[dir="auto"]'));
      const permalink = [...a.querySelectorAll('a[href*="/posts/"], a[href*="story_fbid"], a[href*="/permalink/"]')]
        .map((x) => x.href)[0] || null;
      const key = permalink || (text || "").slice(0, 60);
      if (!key || out.has(key) || !text || text.length < 8) continue;
      out.set(key, { text: text.slice(0, 3000), url: permalink });
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
    site: "facebook",
    recipe: "feed",
    url: location.href,
    capturedAt: new Date().toISOString(),
    postCount: posts.length,
    posts,
  };
}

export const facebook = {
  "facebook:feed": {
    world: "ISOLATED",
    match: "*://*.facebook.com/*",
    describe: "Scrape visible posts in the current Facebook feed/profile (auto-scrolls, expands 'See more').",
    fn: scrapeFeed,
  },
};
