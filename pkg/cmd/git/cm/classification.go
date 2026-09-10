package cm

import (
	"path"
	"regexp"
	"strings"
)

const largeEvidenceFileBytes = 200_000

var binaryExtensions = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {}, ".ico": {}, ".pdf": {}, ".zip": {}, ".gz": {}, ".tgz": {}, ".7z": {}, ".rar": {}, ".sqlite": {}, ".db": {}, ".woff": {}, ".woff2": {}, ".ttf": {}, ".otf": {}, ".mp3": {}, ".mp4": {}, ".mov": {},
}

var lockfileBaseNames = map[string]struct{}{
	"b" + "un.lock": {}, "b" + "un.lockb": {}, "package-lock.json": {}, "pnpm-lock.yaml": {}, "yarn.lock": {},
}

var (
	testPathPattern   = regexp.MustCompile(`(?i)(?:^|/)(?:__tests__|test|tests|spec|specs)(?:/|$)|\.(?:test|spec)\.[^.]+$`)
	configNamePattern = regexp.MustCompile(`(?i)(?:^|/)(?:[^/]+\.)?(?:config|rc)\.[^.]+$`)
	configExtPattern  = regexp.MustCompile(`(?i)\.(?:json|ya?ml|toml|ini)$`)
	sourceExtPattern  = regexp.MustCompile(`(?i)\.(?:[cm]?[jt]sx?|py|go|rs|java|kt|rb|php|swift|cs|c|cc|cpp|h|hpp)$`)
)

func fileRoleForPath(filePath string, binary bool) FileRole {
	normalized := normalizeGitPath(filePath)
	baseName := strings.ToLower(path.Base(filePath))
	if isSensitiveGitPath(filePath) {
		return FileRoleSensitive
	}
	if binary || isBinaryGitPath(filePath) {
		return FileRoleBinary
	}
	if isGeneratedGitPath(filePath) {
		return FileRoleGenerated
	}
	if baseName == "package.json" || baseName == "composer.json" || baseName == "cargo.toml" {
		return FileRoleDependency
	}
	if testPathPattern.MatchString(normalized) {
		return FileRoleTest
	}
	if strings.HasSuffix(baseName, ".md") || strings.HasPrefix(baseName, "readme") || strings.HasPrefix(normalized, "docs/") {
		return FileRoleDocs
	}
	if strings.HasPrefix(normalized, ".github/") || configNamePattern.MatchString(normalized) || configExtPattern.MatchString(normalized) {
		return FileRoleConfig
	}
	if sourceExtPattern.MatchString(normalized) {
		return FileRoleSource
	}
	return FileRoleUnknown
}

func contentPolicyFor(role FileRole, size int64, exists bool, oversizedPatch bool) ContentPolicy {
	if role == FileRoleSensitive {
		return ContentRedacted
	}
	if role == FileRoleBinary || role == FileRoleGenerated || oversizedPatch || (exists && size > largeEvidenceFileBytes) {
		return ContentMetadataOnly
	}
	return ContentInspect
}

func normalizeGitPath(value string) string {
	return strings.ReplaceAll(value, "\\", "/")
}

func isGeneratedGitPath(filePath string) bool {
	normalized := normalizeGitPath(filePath)
	baseName := path.Base(filePath)
	if _, found := lockfileBaseNames[baseName]; found {
		return true
	}
	return strings.Contains(normalized, "/dist/") || strings.HasPrefix(normalized, "dist/") ||
		strings.Contains(normalized, "/build/") || strings.HasPrefix(normalized, "build/") ||
		strings.Contains(normalized, "/coverage/") || strings.HasPrefix(normalized, "coverage/") ||
		strings.HasSuffix(baseName, ".min.js") || strings.HasSuffix(baseName, ".map")
}

func isSensitiveGitPath(filePath string) bool {
	baseName := path.Base(filePath)
	extension := strings.ToLower(path.Ext(filePath))
	return baseName == ".env" || strings.HasPrefix(baseName, ".env.") || baseName == "id_rsa" || baseName == "id_ed25519" ||
		extension == ".pem" || extension == ".key" || extension == ".p12" || extension == ".pfx"
}

func isBinaryGitPath(filePath string) bool {
	_, found := binaryExtensions[strings.ToLower(path.Ext(filePath))]
	return found
}
