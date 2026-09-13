# dist — built engine artifacts

Naming and contents follow ADR 0007. Each artifact ships with `manifest.json` (upstream base, the
patch that IS the fork, `args.gn`, measured sizes, and which gates passed) and `SHA256SUMS`.

**These are binaries, not source — they are NOT committed.** `.gitignore` excludes the tarballs;
the manifest is committed because it is the recipe, and a build is reproducible from three fields:
upstream revision + fork patch + args.

## chrome-agent-engine-152.0.7948.0+fork.1-linux-x64

| | |
|---|---|
| `chrome` binary | 486 MB (509,529,432 bytes), ELF x86-64, non-component |
| staged tree | 531 MB (en-US only; all locales would add ~120 MB) |
| tarball | 185 MB |

Built on Ubuntu 24.04 from upstream `a9b5091aa0` plus the 298 KB fork patch. H.264/AAC enabled
(`proprietary_codecs`) — note the patent-licensing obligation that attaches to *distributing*
binaries with those decoders (ADR 0007).

Unpack and point the client at it:

```sh
tar xzf chrome-agent-engine-152.0.7948.0+fork.1-linux-x64.tar.gz
export CHROMIUM_SENDKEYS_OUT=$PWD/chrome-agent-engine-152.0.7948.0+fork.1-linux-x64
CHROMIUM_SENDKEYS_DIR=$(chrome-agent spool) "$CHROMIUM_SENDKEYS_OUT/chrome" \
  --user-data-dir=~/chrome-agent-profile --headless=new --no-first-run --no-sandbox about:blank &
chrome-agent status     # expect webdriver:false
```
