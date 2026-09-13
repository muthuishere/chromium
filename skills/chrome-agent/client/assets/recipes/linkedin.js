// linkedin.js — LinkedIn recipes. Each exported value is { world, match, describe, fn }.
//
// fn runs via chrome.scripting.executeScript({ func: fn, world, args:[opts] }). It is
// serialized (Function.prototype.toString) and re-parsed in the target, so it MUST be
// self-contained: no references to module scope, no shared helpers — inline everything.
//
// world: "ISOLATED" = extension content-script world. It shares the page DOM but is NOT
// subject to the page CSP — this is what lets us read CSP-strict pages (e.g. LinkedIn's
// /recent-activity/) where injecting a <script> into MAIN world is blocked.
// world: "MAIN" = real page window (needed only to hook page globals like window.fetch).

// ---- DOM scrape: the logged-in member's own posts (recent-activity page) -------------
async function scrapeMyPosts(opts) {
  const o = opts || {};
  const PASSES = o.passes || 4; // gentle by default — don't hammer / risk a flag
  // FLOOR, not a default (owner 2026-07-26: "if you scroll fast on x or reddit they will ban
  // atyleast a 1 second delay scrolling and liking"). The default was already >=1200ms, but
  // `o.delay` let a caller pass 100 and the ban risk is the caller's mistake to make, not ours.
  // Math.max makes 1000ms the minimum no caller can go under.
  const DELAY = Math.max(1000, o.delay || 1200);
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const clean = (el) =>
    el ? el.innerText.replace(/ /g, " ").replace(/\s+/g, " ").trim() : null;

  const collect = () => {
    const wrappers = [
      ...document.querySelectorAll(
        'div.feed-shared-update-v2, div[data-urn^="urn:li:activity"], div[data-id^="urn:li:activity"]'
      ),
    ];
    const out = new Map();
    for (const w of wrappers) {
      const urn = w.getAttribute("data-urn") || w.getAttribute("data-id") || null;
      const key = urn || (clean(w) || "").slice(0, 40);
      if (!key || out.has(key)) continue;
      const text = clean(
        w.querySelector(
          ".update-components-text, .feed-shared-update-v2__description, .update-components-update-v2__commentary, .feed-shared-inline-show-more-text"
        )
      );
      const counts = w.querySelector(".social-details-social-counts");
      if (text) out.set(key, { urn, text: text.slice(0, 4000), social: clean(counts) });
    }
    return out;
  };

  // expand "see more", scroll, accumulate across passes (SPA lazy-loads on scroll)
  const acc = new Map();
  for (let i = 0; i < PASSES; i++) {
    document
      .querySelectorAll(
        'button.feed-shared-inline-show-more-text__see-more-less-toggle, .inline-show-more-text__button, button[aria-label*="see more" i]'
      )
      .forEach((b) => { try { b.click(); } catch (e) {} });
    for (const [k, v] of collect()) if (!acc.has(k)) acc.set(k, v);
    window.scrollTo(0, document.body.scrollHeight);
    await sleep(DELAY);
  }
  window.scrollTo(0, 0);
  const posts = [...acc.values()];
  return {
    site: "linkedin",
    recipe: "my-posts",
    url: location.href,
    capturedAt: new Date().toISOString(),
    visibility: document.visibilityState,
    postCount: posts.length,
    posts,
  };
}

// ---- Credentialed voyager fetch from the page world (home feed) ----------------------
// Runs in MAIN world so same-origin fetch carries cookies; not blocked by page CSP.
async function fetchHomeFeed(opts) {
  const COUNT = (opts && opts.count) || 20;
  const m = document.cookie.match(/JSESSIONID="?([^";]+)"?/);
  const csrf = m ? m[1] : null;
  if (!csrf) return { error: "no JSESSIONID — not logged in?" };
  const r = await fetch("/voyager/api/feed/updatesV2?count=" + COUNT + "&q=chronFeed", {
    headers: { "csrf-token": csrf, "x-restli-protocol-version": "2.0.0", accept: "application/json" },
    credentials: "include",
  });
  if (!r.ok) return { error: "voyager " + r.status };
  const j = await r.json();
  const posts = (j.elements || []).map((e) => {
    const c = e?.socialDetail?.totalSocialActivityCounts || {};
    return {
      urn: e?.updateMetadata?.urn || e?.entityUrn || null,
      author: e?.actor?.name?.text || null,
      text: (e?.commentary?.text?.text || "").replace(/\s+/g, " ").trim().slice(0, 800) || null,
      likes: c.numLikes ?? null,
      comments: c.numComments ?? null,
    };
  }).filter((p) => p.text || p.urn);
  return { site: "linkedin", recipe: "feed", capturedAt: new Date().toISOString(), postCount: posts.length, posts };
}

// ---- Credentialed voyager fetch: notifications (inbound) ----------------------------
// WHY THIS IS AN API RECIPE, NOT A DOM SCRAPE (learned 2026-07-20): every recipe eval —
// generic:page-text, linkedin:feed, linkedin:my-posts — TIMES OUT on /notifications/,
// /in/*/recent-activity/* and /feed/update/<urn>/. The same evals succeed on /feed/, and a
// control on a light page returns instantly, so it is those pages wedging the eval channel,
// not the fork. Run this FROM /feed/ (same origin, so cookies + csrf apply) to read
// notifications without ever loading the page that wedges.
async function fetchNotifications(opts) {
  const COUNT = (opts && opts.count) || 25;
  const m = document.cookie.match(/JSESSIONID="?([^";]+)"?/);
  const csrf = m ? m[1] : null;
  if (!csrf) return { error: "no JSESSIONID — not logged in? (must run on a linkedin.com page)" };
  const url =
    "/voyager/api/voyagerIdentityDashNotificationCards?decorationId=" +
    "com.linkedin.voyager.dash.deco.identity.notifications.CardsCollection-15" +
    "&count=" + COUNT + "&q=filterVanityName&filterVanityName=all";
  const r = await fetch(url, {
    headers: {
      "csrf-token": csrf,
      "x-restli-protocol-version": "2.0.0",
      accept: "application/vnd.linkedin.normalized+json+2.1",
    },
    credentials: "include",
  });
  if (!r.ok) return { error: "voyager " + r.status };
  const ct = r.headers.get("content-type") || "";
  // A 200 is not proof: assert the payload shape, since an auth wall also returns 200 HTML.
  if (!ct.includes("json")) return { error: "not json (auth wall?) ct=" + ct };
  const j = await r.json();
  const cards = (j.included || [])
    .filter((x) => x && (x.headline || x.subHeadline))
    .map((x) => ({
      headline: ((x.headline && x.headline.text) || "").replace(/\s+/g, " ").trim() || null,
      subHeadline: ((x.subHeadline && x.subHeadline.text) || "").replace(/\s+/g, " ").trim() || null,
      publishedAt: x.publishedAt || null,
      cardUrn: x.entityUrn || null,
    }))
    .filter((c) => c.headline || c.subHeadline);
  return {
    site: "linkedin",
    recipe: "notifications",
    capturedAt: new Date().toISOString(),
    numUnseen: (j.data && j.data.metadata && j.data.metadata.numUnseen) ?? null,
    cardCount: cards.length,
    cards,
  };
}

export const linkedin = {
  "linkedin:notifications": {
    world: "MAIN",
    match: "*://*.linkedin.com/*",
    describe:
      "Inbound notifications via credentialed voyager API (numUnseen + cards). Run it from /feed/ — the /notifications/ page itself wedges every recipe eval.",
    fn: fetchNotifications,
  },
  "linkedin:my-posts": {
    world: "ISOLATED",
    match: "*://*.linkedin.com/in/*/recent-activity/*",
    describe: "Own posts from your recent-activity page (DOM scrape, CSP-immune).",
    fn: scrapeMyPosts,
  },
  "linkedin:feed": {
    world: "MAIN",
    match: "*://*.linkedin.com/*",
    describe: "Home feed via credentialed voyager API.",
    fn: fetchHomeFeed,
  },
};
