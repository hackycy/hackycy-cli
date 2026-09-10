package diff

import "strings"

var (
	windowsSystemFiles       = map[string]struct{}{"thumbs.db": {}, "ehthumbs.db": {}, "desktop.ini": {}}
	windowsSystemDirectories = map[string]struct{}{"$recycle.bin": {}, "system volume information": {}}
	macOSSystemDirectories   = map[string]struct{}{".Spotlight-V100": {}, ".Trashes": {}}
)

// hardExcluded captures the inventory's fixed, cross-platform exclusions.
func hardExcluded(comparisonPath string, directory bool) bool {
	name := comparisonPath
	if slash := strings.LastIndexByte(comparisonPath, '/'); slash >= 0 {
		name = comparisonPath[slash+1:]
	}

	if name == ".git" || name == ".DS_Store" || strings.HasPrefix(name, "._") {
		return true
	}
	if directory {
		if _, ok := macOSSystemDirectories[name]; ok {
			return true
		}
		_, ok := windowsSystemDirectories[strings.ToLower(name)]
		return ok
	}
	_, ok := windowsSystemFiles[strings.ToLower(name)]
	return ok
}
