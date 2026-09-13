// reddit.js — Reddit recipes. Reddit has a clean public JSON API: append .json to any
// listing URL. We fetch it same-origin (credentialed) from MAIN world so it also covers
// logged-in / over-18 views.

async function fetchListing(opts) {
  const limit = (opts && opts.limit) || 50;
  // TRAP (2026-09-12): on a LISTING ROOT the path is just "/", so stripping the trailing slash
  // leaves the bare origin and `base + ".json"` builds the HOST "www.reddit.com.json" — a DNS
  // failure that surfaces as `TypeError: Failed to fetch`, never an HTTP status, so it reads like
  // the browser is broken rather than the URL. Keep a path segment: "/.json" is what reddit wants.
  const here = new URL(location.href.split("?")[0]);
  const path = here.pathname.replace(/\/$/, "");
  const u = here.origin + (path === "" ? "/" : path) + ".json?limit=" + limit + "&raw_json=1";
  const r = await fetch(u, { credentials: "include", headers: { accept: "application/json" } });
  if (!r.ok) return { error: "reddit " + r.status, url: u };
  const j = await r.json();
  const listing = Array.isArray(j) ? j[0] : j;
  const children = listing?.data?.children || [];
  const posts = children.map((c) => {
    const d = c.data || {};
    return {
      id: d.id,
      title: d.title || null,
      author: d.author || null,
      subreddit: d.subreddit || null,
      selftext: (d.selftext || "").slice(0, 4000) || null,
      score: d.score ?? null,
      comments: d.num_comments ?? null,
      url: d.permalink ? "https://www.reddit.com" + d.permalink : d.url || null,
      media: d.url_overridden_by_dest || null,
    };
  });
  return {
    site: "reddit",
    recipe: "listing",
    url: location.href,
    capturedAt: new Date().toISOString(),
    postCount: posts.length,
    posts,
  };
}

export const reddit = {
  "reddit:listing": {
    world: "MAIN",
    match: "*://*.reddit.com/*",
    describe: "Fetch the current subreddit/profile/post as JSON (append .json), credentialed.",
    fn: fetchListing,
  },
};
