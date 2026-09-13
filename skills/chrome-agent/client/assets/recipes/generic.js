// generic.js — site-agnostic recipes usable anywhere (ISOLATED world).

async function pageText() {
  const clean = (s) => (s || "").replace(/\s+/g, " ").trim();
  return {
    site: "generic",
    recipe: "page-text",
    url: location.href,
    title: document.title,
    capturedAt: new Date().toISOString(),
    text: clean(document.body ? document.body.innerText : "").slice(0, 20000),
  };
}

async function links() {
  const out = new Map();
  for (const a of document.querySelectorAll("a[href]")) {
    const href = a.href;
    if (!href || out.has(href)) continue;
    out.set(href, { href, text: (a.innerText || "").replace(/\s+/g, " ").trim().slice(0, 200) });
  }
  return { site: "generic", recipe: "links", url: location.href, count: out.size, links: [...out.values()] };
}

async function images() {
  const urls = new Set();
  for (const img of document.querySelectorAll("img")) {
    const u = img.currentSrc || img.src;
    if (u && /^https?:/.test(u)) urls.add(u);
  }
  for (const s of document.querySelectorAll("source[srcset]")) {
    (s.getAttribute("srcset") || "").split(",").forEach((p) => {
      const u = p.trim().split(" ")[0];
      if (u && /^https?:/.test(u)) urls.add(u);
    });
  }
  return { site: "generic", recipe: "images", url: location.href, count: urls.size, images: [...urls] };
}

// Detect a captcha/challenge widget and extract its sitekey, so the caller can solve it
// via captcha.mjs and inject the token back. Read-only.
async function detectCaptcha() {
  const grab = (sel, attr) => {
    const el = document.querySelector(sel);
    return el ? el.getAttribute(attr) : null;
  };
  const fromIframe = (re) => {
    const f = [...document.querySelectorAll("iframe")].find((i) => re.test(i.src || ""));
    if (!f) return null;
    const m = (f.src || "").match(/[?&](?:k|sitekey|render)=([^&]+)/);
    return { src: f.src, sitekey: m ? decodeURIComponent(m[1]) : null };
  };
  let type = null, sitekey = null, detail = null;
  if (document.querySelector(".cf-turnstile") || /challenges\.cloudflare\.com/.test(document.body.innerHTML)) {
    type = "turnstile"; sitekey = grab(".cf-turnstile", "data-sitekey") || (fromIframe(/challenges\.cloudflare\.com/) || {}).sitekey;
  } else if (document.querySelector(".g-recaptcha") || document.querySelector('iframe[src*="recaptcha"]')) {
    type = "recaptcha"; sitekey = grab(".g-recaptcha", "data-sitekey") || (fromIframe(/recaptcha/) || {}).sitekey;
  } else if (document.querySelector(".h-captcha") || document.querySelector('iframe[src*="hcaptcha"]')) {
    type = "hcaptcha"; sitekey = grab(".h-captcha", "data-sitekey") || (fromIframe(/hcaptcha/) || {}).sitekey;
  } else if (document.querySelector('iframe[src*="arkose"], iframe[src*="funcaptcha"]')) {
    type = "arkose"; detail = (fromIframe(/arkose|funcaptcha/) || {}).src;
  }
  return { site: "generic", recipe: "detect-captcha", present: !!type, type, sitekey, detail, url: location.href };
}

export const generic = {
  "generic:detect-captcha": {
    world: "ISOLATED",
    match: "*://*/*",
    describe: "Detect a captcha/challenge on the page and extract its sitekey (for captcha.mjs).",
    fn: detectCaptcha,
  },
  "generic:page-text": {
    world: "ISOLATED",
    match: "*://*/*",
    describe: "Full visible text of the current page (capped at 20k chars).",
    fn: pageText,
  },
  "generic:links": {
    world: "ISOLATED",
    match: "*://*/*",
    describe: "All hyperlinks on the page (href + anchor text).",
    fn: links,
  },
  "generic:images": {
    world: "ISOLATED",
    match: "*://*/*",
    describe: "All image URLs on the page (img + srcset).",
    fn: images,
  },
};
