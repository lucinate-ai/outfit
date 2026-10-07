package main

import (
	"github.com/spinloop-ai/spinloop/internal/cloud"
)

// sourceLocation is where the CDK project's sources live for a run: the
// version-matched ref, the directory they are downloaded to, and whether
// sources from other refs get pruned once the run succeeds.
type sourceLocation struct {
	ref   string
	dir   string
	prune bool
}

// resolveSourceLocation applies the ref and --dir rules bootstrap and bake
// share: a ref matched to the running binary (an explicit --ref wins), a
// ref-keyed default directory, and pruning of other refs — except that an
// explicit --dir is the user's own location, neither keyed by ref nor pruned.
func resolveSourceLocation(ref, dir string) (sourceLocation, error) {
	resolvedRef := cloud.ResolveRef(version, ref)
	locDir, err := cloud.SourceDir(resolvedRef)
	if err != nil {
		return sourceLocation{}, err
	}
	loc := sourceLocation{ref: resolvedRef, dir: locDir, prune: true}
	if dir != "" {
		loc.dir = dir
		loc.prune = false
	}
	return loc, nil
}

// pruneSourceCaches drops every ref-keyed source cache except the one this run
// used, so stale-version checkouts do not accumulate. An explicit --dir is
// never pruned.
func pruneSourceCaches(loc sourceLocation) error {
	if !loc.prune {
		return nil
	}
	sourceRoot, err := cloud.SourceRoot()
	if err != nil {
		return err
	}
	return cloud.PruneSources(sourceRoot, loc.ref)
}
