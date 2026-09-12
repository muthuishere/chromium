# playbooks — one folder per site

Generated from the browser-research recipe registry, so a documented verb is a **registered** verb.
Regenerate after any recipe change; do not hand-edit the verb lists.

```sh
python3 scripts/gen_playbooks.py \
  ~/muthu/deemwarworkspace/browser-research-workspace/browser-research/extensions/browser-research/src/recipes \
  playbooks
```

Each folder: `meta.md` (domain, aliases, staged) · `read.md` · `write.md` · `login.md` · `traps.md`.

**No playbook names an identity.** A site does not have one — a person chooses which profile to act
as, and may use two on the same site. The `browser:<label>` binding lives in apl
(`apl ADR-0009`); these files describe the site only.

Hand-written prose — traps, verification rules, the login procedure — survives regeneration only if
you add it to the generator's tables. Only the verb surface is generated, because only the verb
surface has a source of truth to check against.
