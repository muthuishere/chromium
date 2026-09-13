package assets

import "embed"

// Recipes is the browser-research recipe registry, vendored and compiled in (ADR 0010).
//
// It lives beside the site definitions for the same reason they do: `go:embed` cannot reach above
// its own package directory, and there must be exactly ONE shipped copy. Vendoring is not a nicety
// — running the bash CLI in an Ubuntu container found that a box without the owner's
// browser-research checkout reported 6 verbs instead of 48 and failed every `recipe` call.
//
// package.json travels with the sources on purpose: the registry is ESM in .js files, and without
// a {"type":"module"} marker beside them node reads every file as CommonJS. The Go client does not
// need it — it never runs node — but the vendored tree is also what `recipes vendor` writes into
// ~/.config/chrome-agent/recipes, where node-based tooling still reads it.
//
// The user/ drop-ins from ~/.config/chrome-agent/recipes are deliberately NOT embedded: they are
// per-machine, git-ignored userscripts. They are still resolved at runtime from the installed copy.
//
//go:embed recipes
var Recipes embed.FS
