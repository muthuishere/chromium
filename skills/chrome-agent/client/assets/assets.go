// Package assets is what ships INSIDE the binary (ADR 0010).
//
// The site definitions live here rather than beside the bash CLI because `go:embed` cannot reach
// above its own package directory — and because there must be exactly ONE shipped copy. Both
// implementations read this path; the Go client compiles it in, and scripts/sites.py points at it.
//
// Embedding is not the whole story: the INSTALLED copy in ~/.config/chrome-agent/sites still wins
// over what is compiled in (ADR 0004), so a re-skinned site is a one-file fix on a server with no
// redeploy. Embedding only removes "you must also ship a directory" from the install.
package assets

import "embed"

//go:embed sites
var Sites embed.FS
