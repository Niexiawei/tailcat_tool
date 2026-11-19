package main

import "github.com/egoist/mygo"

// versionText is the version mygo build links from mygo.json; "dev" for a
// plain go build, which links nothing.
//
// mygo.App.Version() reads the version from the package (the app bundle, or
// the -X github.com/egoist/mygo.packageVersion that mygo build passes) and
// falls back to the executable's own metadata, so it answers before Run.
func versionText() string {
	if v := mygo.App.Version(); v != "" {
		return v
	}
	return "dev"
}
