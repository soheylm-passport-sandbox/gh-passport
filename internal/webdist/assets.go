package webdist

import (
	"embed"
	"errors"
	"io/fs"
)

//go:embed all:bundle all:legacy
var Assets embed.FS

// Bundle selects a pinned lesson contract; unknown versions cannot fall back.
func Bundle(version string) (fs.FS, error) {
	switch version {
	case "2.1.2":
		return fs.Sub(Assets, "legacy/2.1.2")
	case "3.0.0":
		return fs.Sub(Assets, "legacy/3.0.0")
	case "4.0.0":
		return fs.Sub(Assets, "bundle")
	default:
		return nil, errors.New("unsupported lesson bundle")
	}
}
