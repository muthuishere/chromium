# chrome-agent engine — releases

The undetectable Chromium fork the `chrome-agent` client drives. Verify the checksum before you
run it — an unverified browser binary is not something to trust.

## Quick install (Linux x86_64)

```sh
curl -fsSL https://hel1.your-objectstorage.com/publicassets/chrome-agent/install.sh | bash
export CHROMIUM_SENDKEYS_OUT="$HOME/.local/share/chrome-agent/engine"
```

The installer downloads the tarball, checks it against `SHA256SUMS`, refuses to unpack on a
mismatch, and prints the env var to point the client at.

## Files

| file | what |
|---|---|
| `chrome-agent-engine-152.0.7948.0+fork.2-linux-x64.tar.gz` | the engine (489 MB unpacked, 186 MB gz) |
| `SHA256SUMS` | checksums — the install refuses a mismatch |
| `manifest.json` | the recipe: upstream base, fork patch, args.gn, gate results |
| `install.sh` | download + verify + unpack |
| `LICENSE` | Chromium's license (redistribution notice) |
| `BUILD.md` | full provenance: flags, codecs, capabilities |
| `agent-fork.patch` | the entire fork as one patch over upstream — read exactly what changed |

## Platforms

- **linux-x64** — available, all five release gates passed (relocation, webdriver, spool protocol, doctor, a real read).
- **darwin (macOS)** — not published yet; the build is in progress and will follow once it passes the same gates, signed and notarized so Gatekeeper does not quarantine it.

## Provenance

Chromium 152.0.7948.0 + a 298 KB agent patch over upstream `a9b5091aa0`. H.264/AAC/MP3 ARE enabled and runtime-verified (canPlayType 'probably') — this build plays everything. See BUILD.md.
This build was verified on its build host; a second-machine check is still recommended before you
depend on it. See `manifest.json` for the full recipe and gate results.
