package webdist

import "embed"

// Assets are replaced with the deterministic Astro build by the release script.
// The committed fallback keeps source tests buildable and fails honestly.
//
//go:embed all:bundle
var Assets embed.FS
