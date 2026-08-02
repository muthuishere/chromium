#!/usr/bin/env bash
# sync-upstream.sh — pull new upstream Chromium under our agent-browser patches,
# keeping OUR code unchanged.
#
# Model: our modifications are a small commit stack (the "chromium-agent" layer)
# that sits ON TOP of one upstream base commit. Syncing = move the base forward
# and REPLAY our stack onto it. Our diff stays byte-identical except where
# upstream edited the very same lines — and there the rebase stops and asks you.
#
#   ./sync-upstream.sh                 # sync to upstream/main tip
#   ./sync-upstream.sh <ref|tag|sha>   # sync to a specific upstream revision (recommended: a release tag)
#   ./sync-upstream.sh --no-deps       # skip the heavy `gclient sync` (source rebase only)
#   ./sync-upstream.sh --push          # after success, regenerate + push the patch series to the private repo
#
# Safe by construction: tags a rollback point before touching anything, works on
# a scratch branch, and never force-moves your work until the replay succeeds.
set -euo pipefail

# ---- config (override via env or .deemwar-sync.conf) --------------------------
UPSTREAM_URL="${UPSTREAM_URL:-https://chromium.googlesource.com/chromium/src.git}"
DEPOT_TOOLS="${DEPOT_TOOLS:-$HOME/depot_tools}"
PRIVATE_PATCH_REPO="${PRIVATE_PATCH_REPO:-git@github.com:deemwar-products/chromium-agent.git}"
CONF="$(dirname "$0")/.deemwar-sync.conf"
[ -f "$CONF" ] && . "$CONF"      # may set UPSTREAM_BASE, UPSTREAM_URL, DEPOT_TOOLS
# The upstream commit our stack currently sits on. Seeded to the original fork
# point; the script rewrites this line on every successful sync.
UPSTREAM_BASE="${UPSTREAM_BASE:-a9b5091aa0fa2f7aa769c1c6b2ceea8038e891dc}"

SYNC_DEPS=1; PUSH_PATCHES=0; TARGET=""
for a in "$@"; do case "$a" in
  --no-deps) SYNC_DEPS=0 ;;
  --push)    PUSH_PATCHES=1 ;;
  -*)        echo "unknown flag: $a" >&2; exit 2 ;;
  *)         TARGET="$a" ;;
esac; done

say(){ printf '\n\033[1m▸ %s\033[0m\n' "$*"; }
die(){ printf '\033[31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

cd "$(dirname "$0")"
[ -d .git ] || die "run this inside the chromium checkout"
[ -x "$DEPOT_TOOLS/gclient" ] || [ "$SYNC_DEPS" = 0 ] || die "depot_tools not found at $DEPOT_TOOLS (set DEPOT_TOOLS=, or pass --no-deps)"

# A dirty tree would be silently swallowed by a rebase — refuse instead.
git diff --quiet && git diff --cached --quiet || die "working tree is dirty — commit or stash first"

# ---- 0. rollback point --------------------------------------------------------
STAMP="$(date +%Y%m%d-%H%M%S)"
ROLLBACK="presync-$STAMP"
OURTIP="$(git rev-parse HEAD)"
git tag "$ROLLBACK" "$OURTIP"
say "rollback tag: $ROLLBACK  (git reset --hard $ROLLBACK to undo everything)"

# Verify our stack really sits on the recorded base; if not, bail loudly rather
# than replay the wrong range (a silent wrong-base = a garbage rebase).
git merge-base --is-ancestor "$UPSTREAM_BASE" HEAD 2>/dev/null \
  || die "recorded UPSTREAM_BASE $UPSTREAM_BASE is not an ancestor of HEAD — fix .deemwar-sync.conf"
OUR_COMMITS=$(git rev-list --count "$UPSTREAM_BASE"..HEAD)
say "our stack = $OUR_COMMITS commits on top of ${UPSTREAM_BASE:0:12}"

# ---- 1. fetch upstream --------------------------------------------------------
git remote get-url upstream >/dev/null 2>&1 || git remote add upstream "$UPSTREAM_URL"
say "fetching upstream ($UPSTREAM_URL) — this can be large the first time"
git fetch --tags upstream

NEW_BASE="$(git rev-parse "${TARGET:-upstream/main}^{commit}")" \
  || die "cannot resolve target '${TARGET:-upstream/main}'"
if [ "$NEW_BASE" = "$UPSTREAM_BASE" ]; then
  say "already on the latest requested upstream (${NEW_BASE:0:12}) — nothing to sync"; exit 0
fi
say "moving base ${UPSTREAM_BASE:0:12} → ${NEW_BASE:0:12}  ($(git rev-list --count "$UPSTREAM_BASE".."$NEW_BASE") upstream commits)"

# ---- 2. replay OUR stack onto the new base ------------------------------------
# --onto replays exactly UPSTREAM_BASE..HEAD (our commits) onto NEW_BASE.
say "replaying our $OUR_COMMITS commits onto the new base…"
if ! git rebase --onto "$NEW_BASE" "$UPSTREAM_BASE" HEAD; then
  cat >&2 <<MSG

\033[31m✗ CONFLICT — upstream changed lines we also patched.\033[0m
Conflicting files:
$(git diff --name-only --diff-filter=U | sed 's/^/    /')

Resolve them (edit, then \`git add <file>\`), and run:  git rebase --continue
When the rebase finishes, re-run:  ./sync-upstream.sh ${TARGET:+$TARGET }--no-deps   # to record the new base + (optionally) deps
To abandon and restore everything:  git rebase --abort && git reset --hard $ROLLBACK
MSG
  exit 1
fi

# rebase succeeded → record the new base for next time
{ echo "# written by sync-upstream.sh on $STAMP"
  echo "UPSTREAM_BASE=$NEW_BASE"
  echo "UPSTREAM_URL=$UPSTREAM_URL"; } > "$CONF"
say "our stack now sits on ${NEW_BASE:0:12} — our diff is unchanged except any lines you just merged"

# ---- 3. update DEPS to match the new revision ---------------------------------
if [ "$SYNC_DEPS" = 1 ]; then
  say "gclient sync (updating third-party DEPS/submodules to the new revision)…"
  PATH="$DEPOT_TOOLS:$PATH" gclient sync -D --nohooks
  PATH="$DEPOT_TOOLS:$PATH" gclient runhooks
else
  say "skipped gclient sync (--no-deps) — run it before building"
fi

# ---- 4. refresh the private patch series --------------------------------------
if [ "$PUSH_PATCHES" = 1 ]; then
  say "regenerating patch series and pushing to the private repo…"
  TMP="$(mktemp -d)"; git clone -q "$PRIVATE_PATCH_REPO" "$TMP/repo"
  rm -rf "$TMP/repo/patches"; mkdir -p "$TMP/repo/patches"
  git format-patch "$NEW_BASE"..HEAD -o "$TMP/repo/patches" >/dev/null
  ( cd "$TMP/repo"
    sed -i.bak "s/^   \`[0-9a-f]\{40\}\`/   \`$NEW_BASE\`/" README.md 2>/dev/null || true
    git add -A
    git -c user.email=io@deemwar.com -c user.name=deemwar commit -q \
      -m "sync: rebased onto chromium ${NEW_BASE:0:12} ($(git -C "$OLDPWD" rev-list --count "$NEW_BASE"..HEAD) patches)" || echo "  (no patch changes to commit)"
    git push -q origin HEAD )
  rm -rf "$TMP"
  say "private patch series updated at $PRIVATE_PATCH_REPO"
fi

cat <<DONE

\033[32m✓ upstream sync complete.\033[0m
  base:   ${NEW_BASE:0:12}
  ours:   $OUR_COMMITS commits replayed, unchanged
  next:   task build            # (Taskfile.yml) rebuild the agent browser
  undo:   git reset --hard $ROLLBACK
DONE
