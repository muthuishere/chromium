// instagram.js — Instagram recipes (DOM scrape, ISOLATED world).
// Captions + image/video src from the current profile grid or a post.

async function scrapeProfile(opts) {
  const o = opts || {};
  const PASSES = o.passes || 8;
  // FLOOR, not a default (owner 2026-07-26: "if you scroll fast on x or reddit they will ban
  // atyleast a 1 second delay scrolling and liking"). The default was already >=1200ms, but
  // `o.delay` let a caller pass 100 and the ban risk is the caller's mistake to make, not ours.
  // Math.max makes 1000ms the minimum no caller can go under.
  const DELAY = Math.max(1000, o.delay || 1400);
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

  const collect = () => {
    const links = [...document.querySelectorAll('a[href*="/p/"], a[href*="/reel/"]')];
    const out = new Map();
    for (const a of links) {
      const href = a.getAttribute("href");
      if (!href || out.has(href)) continue;
      const img = a.querySelector("img");
      out.set(href, {
        url: "https://www.instagram.com" + href,
        alt: img ? img.getAttribute("alt") : null, // IG puts caption-ish text in alt
        thumb: img ? img.getAttribute("src") : null,
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
    site: "instagram",
    recipe: "profile",
    url: location.href,
    capturedAt: new Date().toISOString(),
    postCount: posts.length,
    posts,
  };
}

async function scrapePost() {
  const clean = (el) => (el ? el.innerText.replace(/\s+/g, " ").trim() : null);
  const caption = clean(document.querySelector("h1, ul li div span")) || null;
  const media = [...document.querySelectorAll("article img, article video")]
    .map((m) => m.currentSrc || m.src)
    .filter(Boolean);
  return {
    site: "instagram",
    recipe: "post",
    url: location.href,
    capturedAt: new Date().toISOString(),
    caption,
    media: [...new Set(media)],
  };
}

export const instagram = {
  "instagram:profile": {
    world: "ISOLATED",
    match: "*://*.instagram.com/*",
    describe: "Scrape post/reel links + thumbnails + alt-captions from the current IG profile grid.",
    fn: scrapeProfile,
  },
  "instagram:post": {
    world: "ISOLATED",
    match: "*://*.instagram.com/p/*",
    describe: "Caption + media URLs from a single open Instagram post.",
    fn: scrapePost,
  },
};
