// chatgpt.js — crawl the logged-in ChatGPT session's own conversation history via ChatGPT's
// internal backend API (/backend-api/*), same-origin from inside the tab. Read-only.
//
// Reverse-engineered from a working export run. How it works:
//   - Auth is a Bearer access token fetched at runtime: GET /api/auth/session -> accessToken.
//   - Session COOKIES are implicit — the fetch is same-origin (runs in the chatgpt.com tab),
//     so __Secure-next-auth.session-token / cf_clearance ride along; no explicit Cookie header,
//     and Cloudflare never triggers because the request comes from the real browser.
//   - credentials:"include" is set so the session is always sent even for the token fetch.
//
// Two composable recipes (this is exactly how the original driving loop worked):
//   chatgpt:list          -> {items:[{id,title,create_time,update_time}]}
//   chatgpt:conversation  -> one conversation rebuilt to markdown (opts.id required)
//
// Dump everything (driving loop, one job per conversation so nothing hits the 120s job cap):
//   browser-bridge recipe chatgpt:list "https://chatgpt.com/*" '{"max":500}' > /tmp/list.json
//   # for a project/"gizmo": '{"gizmo":"g-p-<id>"}'
//   for id in $(jq -r '.value.items[].id' /tmp/list.json); do
//     browser-bridge recipe chatgpt:conversation "https://chatgpt.com/*" "{\"id\":\"$id\"}" \
//       | jq -r '.value.markdown' > "/tmp/chatgpt/$id.md"
//   done
// Only the CURRENT branch of each conversation is reconstructed (current_node -> parent).

async function listConversations(opts) {
  const o = opts || {};
  // fn is serialized into the tab — everything must be inline (no shared module helpers).
  const sess = await (await fetch("/api/auth/session", { credentials: "include" })).json();
  const token = sess && sess.accessToken;
  if (!token) return { error: "no accessToken from /api/auth/session — log in to chatgpt.com in this tab first" };
  const H = { Authorization: "Bearer " + token };
  const limit = o.limit || 40;
  const max = o.max || 500;
  const out = [];

  if (o.gizmo) {
    // project/"gizmo"-scoped list: cursor pagination
    let cursor = 0, guard = 0;
    while (guard++ < 50 && out.length < max) {
      const r = await fetch(`/backend-api/gizmos/${o.gizmo}/conversations?cursor=${cursor}&limit=${limit}`, { headers: H, credentials: "include" });
      if (!r.ok) return { error: `list failed: status ${r.status}`, got: out.length, items: out };
      const j = await r.json();
      const items = j.items || [];
      for (const it of items) out.push({ id: it.id, title: it.title, create_time: it.create_time, update_time: it.update_time });
      if (j.cursor === null || j.cursor === undefined || items.length === 0) break;
      cursor = j.cursor;
    }
  } else {
    // all conversations: offset pagination
    let offset = 0, total = Infinity;
    while (offset < total && out.length < max) {
      const r = await fetch(`/backend-api/conversations?offset=${offset}&limit=${limit}&order=updated`, { headers: H, credentials: "include" });
      if (!r.ok) return { error: `list failed: status ${r.status}`, got: out.length, items: out };
      const j = await r.json();
      const items = j.items || [];
      total = typeof j.total === "number" ? j.total : offset + items.length;
      for (const it of items) out.push({ id: it.id, title: it.title, create_time: it.create_time, update_time: it.update_time });
      if (items.length === 0) break;
      offset += items.length;
    }
  }
  return { site: "chatgpt", recipe: "list", gizmo: o.gizmo || null, count: out.length, items: out.slice(0, max) };
}

async function getConversation(opts) {
  const o = opts || {};
  if (!o.id) return { error: "opts.id (conversation id) is required" };
  // fn is serialized into the tab — inline everything (no shared module helpers).
  const sess = await (await fetch("/api/auth/session", { credentials: "include" })).json();
  const token = sess && sess.accessToken;
  if (!token) return { error: "no accessToken — log in to chatgpt.com in this tab first" };
  const r = await fetch(`/backend-api/conversation/${o.id}`, { headers: { Authorization: "Bearer " + token }, credentials: "include" });
  if (!r.ok) return { error: `conversation ${o.id}: status ${r.status}` };
  const c = await r.json();
  const mapping = c.mapping || {};

  // Reconstruct the linear message path: current_node walked up via parents.
  const chain = [];
  let node = c.current_node;
  const seen = new Set();
  while (node && mapping[node] && !seen.has(node)) { seen.add(node); chain.push(mapping[node]); node = mapping[node].parent; }
  chain.reverse();

  const strip = (t) => (t || "")
    .replace(/￼/g, "")                 // object-replacement char
    .replace(/[-]/g, "")        // private-use area
    .replace(/【[^】]*?†[^】]*?】/g, "")       // ChatGPT citation markers
    .trim();
  const SKIP = new Set(["thoughts", "reasoning_recap", "system_error"]);

  const parts = [];
  for (const n of chain) {
    const m = n.message; if (!m) continue;
    const role = m.author && m.author.role;
    if (role === "system" || role === "tool") continue;
    const ct = m.content && m.content.content_type;
    if (SKIP.has(ct)) continue;
    let text = "";
    if (ct === "text") text = (m.content.parts || []).filter((p) => typeof p === "string").join("\n\n");
    else if (ct === "multimodal_text") text = (m.content.parts || []).map((p) => typeof p === "string" ? p : (p && p.text) || (p && p.asset_pointer ? "[image]" : "")).filter(Boolean).join("\n\n");
    else if (ct === "code") text = "```\n" + (m.content.text || "") + "\n```";
    else text = ((m.content && m.content.parts) || []).filter((p) => typeof p === "string").join("\n\n");
    text = strip(text);
    if (!text) continue;
    const who = role === "user" ? "User" : role === "assistant" ? "Assistant" : role;
    parts.push(`## ${who}\n\n${text}`);
  }

  return { site: "chatgpt", recipe: "conversation", id: o.id, title: c.title || "Untitled", create_time: c.create_time, update_time: c.update_time, turns: parts.length, markdown: parts.join("\n\n---\n\n") };
}

export const chatgpt = {
  "chatgpt:list": {
    world: "ISOLATED",
    match: "*://chatgpt.com/*",
    describe: "List the logged-in account's conversations (opts: {gizmo?, limit?, max?}). Read-only.",
    fn: listConversations,
  },
  "chatgpt:conversation": {
    world: "ISOLATED",
    match: "*://chatgpt.com/*",
    describe: "One conversation rebuilt to markdown (opts.id required). Read-only.",
    fn: getConversation,
  },
};
