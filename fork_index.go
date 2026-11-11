package main

// Quick Open finds gitignored env and local config files (.env, .env.local,
// .envrc, .npmrc, settings.local.json). px0 never indexes a gitignored file, so
// these could not be found by name although they are the files people most
// often look for. Only files in folders the walk already enters are added;
// ignored folders (node_modules, dist) are still never walked, so this adds a
// handful of entries, not a tree.

import (
	"os"
	"strings"
)

// pxiIndexIgnoredFile reports whether a gitignored file is still indexed.
func pxiIndexIgnoredFile(name string) bool {
	switch name {
	case ".DS_Store", ".localized":
		return false
	}
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, ".env") || strings.HasSuffix(lower, ".env") || strings.Contains(lower, ".env.") {
		return true
	}
	if strings.HasSuffix(lower, ".local") || strings.Contains(lower, ".local.") {
		return true
	}
	switch lower {
	case ".npmrc", ".yarnrc", ".yarnrc.yml", ".pypirc", ".netrc", ".secrets", ".dev.vars", ".tool-versions", ".python-version", ".node-version", ".nvmrc":
		return true
	}
	return false
}

// pxiIgnoredEntry is the index entry for an ignored file the walk met, if it
// is one pxiIndexIgnoredFile keeps.
func pxiIgnoredEntry(e os.DirEntry, rel string) (FileEntry, bool) {
	name := e.Name()
	if e.IsDir() || !pxiIndexIgnoredFile(name) {
		return FileEntry{}, false
	}
	info, err := e.Info()
	if err != nil || !info.Mode().IsRegular() {
		return FileEntry{}, false
	}
	return FileEntry{
		Path: rel, Name: name, Size: info.Size(),
		Lower: toLowerIfNeeded(rel), NameStart: len(rel) - len(name),
	}, true
}
