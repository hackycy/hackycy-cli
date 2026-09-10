package fs

import "strings"

var archiveSuffixes = []string{
	".tar.bzip2", ".tar.zstd", ".tar.bz2", ".tar.gz", ".tar.xz", ".tar.zst",
	".tbz2", ".tzst", ".gzip", ".bzip2", ".zstd", ".tgz", ".tbz", ".txz",
	".7z", ".zip", ".rar", ".tar", ".gz", ".bz2", ".xz", ".zst", ".cab", ".arj", ".lzh", ".lha", ".cpio",
}

var layeredTarSuffixes = map[string]bool{
	".tar.bzip2": true, ".tar.zstd": true, ".tar.bz2": true, ".tar.gz": true, ".tar.xz": true, ".tar.zst": true,
	".tbz2": true, ".tzst": true, ".tgz": true, ".tbz": true, ".txz": true,
}

func archiveSuffix(name string) string {
	lower := strings.ToLower(name)
	for _, suffix := range archiveSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return suffix
		}
	}
	return ""
}

func extractableArchiveName(name string) bool {
	return archiveSuffix(name) != ""
}

func layeredTarArchiveName(name string) bool {
	return layeredTarSuffixes[archiveSuffix(name)]
}

func archiveDestinationName(name string) string {
	suffix := archiveSuffix(name)
	if suffix == "" {
		return "Extracted"
	}
	base := strings.TrimSpace(name[:len(name)-len(suffix)])
	if base == "" || base == "." || base == ".." {
		return "Extracted"
	}
	return base
}
