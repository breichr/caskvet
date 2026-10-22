package localscan

import (
	"os"
	"path/filepath"
	"sort"
)

// AppBundle is the on-disk location state of one app bundle belonging to
// an installed cask.
type AppBundle struct {
	// Name is the bundle name as declared by the cask (e.g.
	// "LinearMouse.app"); for casks without declared artifacts it is the
	// name of a bundle found in the Caskroom.
	Name string
	// Path is the found bundle location, empty when not found.
	Path string
	// Found reports whether the bundle exists at Path.
	Found bool
}

// LocateAppBundles finds where an installed cask's app bundles live.
//
// Search order: the configured application directories, the Caskroom
// version directory, then the Caskroom token directory (any version).
// Declared names come from the cask metadata; casks without app
// artifacts (fonts, binaries) yield an empty list unless a bundle is
// still present in their Caskroom directory. Read-only.
func (s *Scanner) LocateAppBundles(token, version string, declaredNames []string) ([]AppBundle, error) {
	caskroom, err := s.discoverCaskroom()
	if err != nil {
		return nil, err
	}
	tokenDir := filepath.Join(caskroom, token)
	searchDirs := append([]string{}, s.ApplicationsDirs...)
	if version != "" {
		searchDirs = append(searchDirs, filepath.Join(tokenDir, version))
	}
	searchDirs = append(searchDirs, tokenDir)

	if len(declaredNames) == 0 {
		// Casks without app artifacts: report leftovers only.
		return globAppBundles(searchDirs...), nil
	}

	bundles := make([]AppBundle, 0, len(declaredNames))
	seen := make(map[string]bool, len(declaredNames))
	for _, name := range declaredNames {
		if seen[name] {
			continue
		}
		seen[name] = true
		bundle := AppBundle{Name: name}
		for _, dir := range searchDirs {
			candidate := filepath.Join(dir, name)
			if dirExists(candidate) {
				bundle.Path = candidate
				bundle.Found = true
				break
			}
		}
		bundles = append(bundles, bundle)
	}
	return bundles, nil
}

// globAppBundles returns one AppBundle per "*.app" entry found in any of
// the directories, skipping duplicates by name.
func globAppBundles(dirs ...string) []AppBundle {
	var bundles []AppBundle
	seen := make(map[string]bool)
	for _, dir := range dirs {
		matches, err := filepath.Glob(filepath.Join(dir, "*.app"))
		if err != nil {
			continue
		}
		sort.Strings(matches)
		for _, match := range matches {
			name := filepath.Base(match)
			if seen[name] {
				continue
			}
			seen[name] = true
			bundles = append(bundles, AppBundle{Name: name, Path: match, Found: true})
		}
	}
	return bundles
}

// dirExists reports whether path is an existing directory.
func dirExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}
