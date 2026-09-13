# chrome-agent client (Go)

ADR 0010: this replaces the bash CLI. Slice 1 is here — the protocol, the instance registry,
`doctor`, and the exit-code contract. `auth`/`login`/`logout`/`read`/`sites` still live in
`../chrome-agent` until slice 2.

```
go build -o chrome-agent ./cmd/chrome-agent
go test ./...                       # the traps, as tests
bash ../scripts/parity.sh           # must agree with the bash CLI, verb for verb
bash ../scripts/selftest.sh         # the contract both implementations obey
```

Cross-compiles to every target from one machine, ~3.5 MB, no runtime dependencies — verified on a
bare Ubuntu container with neither node nor python3 installed:

| target | size |
|---|---|
| linux/amd64 | 3.5 MB |
| linux/arm64 | 3.3 MB |
| darwin/arm64 | 3.3 MB |
| darwin/amd64 | 3.5 MB |
| windows/amd64 | 3.6 MB |

## Why the tests look like that

Every test in here is a regression test for something that shipped. `paths_test.go` covers the two
spool-keying bugs — a trailing slash that forked one profile into two browsers, and a `basename`
collision that merged two identities onto one spool. `spool_test.go` runs the wire protocol against
a fake browser, because the real one is a shared singleton and a test must never steer the owner's
tabs; it also pins the rules that cost the most: the reader deletes result files, a silent spool is
a timeout and never a success, and a stale queued command is quarantined rather than replayed.

ADR 0010's rule: a trap the bash CLI encodes in a comment arrives here **as a test**, not as a
comment.
