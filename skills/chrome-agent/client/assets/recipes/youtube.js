// youtube.js — YouTube recipes (DOM scrape, ISOLATED world).

async function scrapeChannelVideos(opts) {
  const o = opts || {};
  const PASSES = o.passes || 8;
  // FLOOR, not a default (owner 2026-07-26: "if you scroll fast on x or reddit they will ban
  // atyleast a 1 second delay scrolling and liking"). The default was already >=1200ms, but
  // `o.delay` let a caller pass 100 and the ban risk is the caller's mistake to make, not ours.
  // Math.max makes 1000ms the minimum no caller can go under.
  const DELAY = Math.max(1000, o.delay || 1200);
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const clean = (el) => (el ? el.innerText.replace(/\s+/g, " ").trim() : null);

  const collect = () => {
    const items = [...document.querySelectorAll("ytd-rich-item-renderer, ytd-grid-video-renderer, ytd-video-renderer")];
    const out = new Map();
    for (const it of items) {
      const a = it.querySelector("a#video-title-link, a#video-title, a#thumbnail");
      const href = a ? a.getAttribute("href") : null;
      const id = href ? (href.match(/v=([^&]+)/) || href.match(/shorts\/([^/?]+)/) || [])[1] : null;
      const title = clean(it.querySelector("#video-title")) || (a ? a.getAttribute("title") : null);
      const meta = clean(it.querySelector("#metadata-line"));
      const key = id || title;
      if (!key || out.has(key) || !title) continue;
      out.set(key, { id, title, meta, url: href ? "https://www.youtube.com" + href : null });
    }
    return out;
  };

  const acc = new Map();
  for (let i = 0; i < PASSES; i++) {
    for (const [k, v] of collect()) if (!acc.has(k)) acc.set(k, v);
    window.scrollTo(0, document.body.scrollHeight);
    await sleep(DELAY);
  }
  const videos = [...acc.values()];
  return {
    site: "youtube",
    recipe: "channel-videos",
    url: location.href,
    capturedAt: new Date().toISOString(),
    count: videos.length,
    videos,
  };
}

export const youtube = {
  "youtube:channel-videos": {
    world: "ISOLATED",
    match: "*://*.youtube.com/*",
    describe: "Scrape video titles + links + metadata from the current channel / results page (auto-scrolls).",
    fn: scrapeChannelVideos,
  },
};
