package cm

import "testing"

func TestFileRoleForPathPreservesLegacyRolePriority(t *testing.T) {
	for _, testCase := range []struct {
		path   string
		binary bool
		want   FileRole
	}{
		{path: ".env", want: FileRoleSensitive},
		{path: "private/.env.production", want: FileRoleSensitive},
		{path: "keys/id_ed25519", want: FileRoleSensitive},
		{path: "keys/KEY.PEM", want: FileRoleSensitive},
		{path: "assets/logo.png", want: FileRoleBinary},
		{path: "src/unknown", binary: true, want: FileRoleBinary},
		{path: "dist/app.js", want: FileRoleGenerated},
		{path: "coverage/report.json", want: FileRoleGenerated},
		{path: "pnpm-lock.yaml", want: FileRoleGenerated},
		{path: "package.json", want: FileRoleDependency},
		{path: "src/feature.test.ts", want: FileRoleTest},
		{path: "__tests__/feature.ts", want: FileRoleTest},
		{path: "docs/guide.txt", want: FileRoleDocs},
		{path: "README.mdx", want: FileRoleDocs},
		{path: ".github/workflows/check.yml", want: FileRoleConfig},
		{path: "project.config.ts", want: FileRoleConfig},
		{path: "src/value.go", want: FileRoleSource},
		{path: "notes/change", want: FileRoleUnknown},
	} {
		if got := fileRoleForPath(testCase.path, testCase.binary); got != testCase.want {
			t.Fatalf("fileRoleForPath(%q, %t) = %q, want %q", testCase.path, testCase.binary, got, testCase.want)
		}
	}
}

func TestContentPolicyForPreservesDenyBasedLegacyBoundary(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		role           FileRole
		size           int64
		exists         bool
		oversizedPatch bool
		want           ContentPolicy
	}{
		{name: "sensitive content", role: FileRoleSensitive, want: ContentRedacted},
		{name: "binary content", role: FileRoleBinary, want: ContentMetadataOnly},
		{name: "generated content", role: FileRoleGenerated, want: ContentMetadataOnly},
		{name: "large present file", role: FileRoleSource, size: largeEvidenceFileBytes + 1, exists: true, want: ContentMetadataOnly},
		{name: "large patch", role: FileRoleSource, oversizedPatch: true, want: ContentMetadataOnly},
		{name: "deleted source", role: FileRoleSource, exists: false, want: ContentInspect},
		{name: "ordinary source", role: FileRoleSource, size: largeEvidenceFileBytes, exists: true, want: ContentInspect},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := contentPolicyFor(testCase.role, testCase.size, testCase.exists, testCase.oversizedPatch); got != testCase.want {
				t.Fatalf("contentPolicyFor() = %q, want %q", got, testCase.want)
			}
		})
	}
}
