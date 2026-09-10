package fs

import "testing"

func TestArchiveNamesUseTheCompleteLongestSuffixSet(t *testing.T) {
	for _, name := range []string{
		"archive.7z", "archive.ZIP", "archive.rar", "archive.tar", "archive.gz", "archive.gzip", "archive.bz2", "archive.bzip2", "archive.xz", "archive.zst", "archive.zstd",
		"archive.cab", "archive.arj", "archive.lzh", "archive.lha", "archive.cpio", "archive.tar.gz", "archive.tar.bz2", "archive.tar.bzip2", "archive.tar.xz", "archive.tar.zst", "archive.tar.zstd", "archive.tgz", "archive.tbz", "archive.tbz2", "archive.txz", "archive.tzst",
	} {
		if !extractableArchiveName(name) {
			t.Fatalf("%q is not extractable", name)
		}
	}
	for _, name := range []string{"archive.iso", "archive.zip.txt", "archive"} {
		if extractableArchiveName(name) {
			t.Fatalf("%q is extractable", name)
		}
	}
	if suffix := archiveSuffix("backup.TAR.BZIP2"); suffix != ".tar.bzip2" {
		t.Fatalf("archiveSuffix() = %q", suffix)
	}
	if !layeredTarArchiveName("backup.TAR.ZST") || layeredTarArchiveName("backup.gz") {
		t.Fatalf("layered TAR recognition is incorrect")
	}
}

func TestArchiveDestinationNameUsesTheLongestSuffix(t *testing.T) {
	for _, test := range []struct {
		name string
		want string
	}{
		{name: "backup.tar.gz", want: "backup"},
		{name: "project.release.2026.tgz", want: "project.release.2026"},
		{name: " .tar.zst", want: "Extracted"},
		{name: "...tar", want: "Extracted"},
		{name: "..tar", want: "Extracted"},
	} {
		if got := archiveDestinationName(test.name); got != test.want {
			t.Fatalf("archiveDestinationName(%q) = %q, want %q", test.name, got, test.want)
		}
	}
}
