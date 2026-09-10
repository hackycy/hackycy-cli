package cm

import (
	"fmt"
	"strings"
	"testing"
)

const evidenceSystem = "Return a concise Angular commit message."

func TestCompileEvidenceIsDeterministicAndKeepsDirectoryHierarchy(t *testing.T) {
	files := []SnapshotFile{
		evidenceFile("src/commands/cm/engine.ts"),
		withRole(evidenceFile("src/commands/cm/engine.test.ts"), FileRoleTest),
		evidenceFile("packages/web/src/view.ts"),
		withRole(evidenceFile("docs/cm.md"), FileRoleDocs),
		withRole(evidenceFile("package.json"), FileRoleDependency),
	}
	first := CompileEvidence(evidenceSnapshot(files), evidenceSystem)
	reversed := append([]SnapshotFile(nil), files...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	second := CompileEvidence(evidenceSnapshot(reversed), evidenceSystem)
	if first.Text != second.Text || first.Coverage != second.Coverage {
		t.Fatalf("evidence is not deterministic\nfirst: %s\nsecond: %s", first.Text, second.Text)
	}
	for _, expected := range []string{
		"DIRECTORY_CONTEXT",
		"CHANGE_SUMMARY files=5 +5 -0 protected=0/0",
		"./ files=1 roles=dependency:1 +1 -0",
		"      cm/ files=2 roles=source:1,test:1 +2 -0",
		"      src/ files=1 roles=source:1 +1 -0",
	} {
		if !strings.Contains(first.Text, expected) {
			t.Fatalf("evidence missing %q: %s", expected, first.Text)
		}
	}
	if strings.Contains(first.Text, "cluster=") || strings.Contains(first.Text, "COMMIT_EVIDENCE") {
		t.Fatalf("evidence has retired format: %s", first.Text)
	}
}

func TestCompileEvidenceExcludesProtectedContentAndRecordsInspectableRename(t *testing.T) {
	renamed := evidenceFile("src/new/location.ts")
	renamed.OriginalPath = "src/old/location.ts"
	renamed.IndexStatus = 'R'
	renamed.Stats = ChangeStats{Additions: 3, Deletions: 1}
	sensitive := withPolicy(withRole(evidenceFile("private/.env.production"), FileRoleSensitive), ContentRedacted)
	sensitive.Hunks = []DiffHunk{evidenceHunk([]string{"API_KEY=never-send-this"})}
	generated := withPolicy(withRole(evidenceFile("dist/app.js"), FileRoleGenerated), ContentMetadataOnly)
	compiled := CompileEvidence(evidenceSnapshot([]SnapshotFile{renamed, sensitive, generated}), evidenceSystem)
	for _, expected := range []string{
		"old/ rename-from=1 rename-to=0",
		"new/ files=1 roles=source:1 +3 -1 rename-from=0 rename-to=1",
		"location.ts:\n    rename from src/old/location.ts",
	} {
		if !strings.Contains(compiled.Text, expected) {
			t.Fatalf("evidence missing %q: %s", expected, compiled.Text)
		}
	}
	for _, forbidden := range []string{"private/", "dist/", "never-send-this"} {
		if strings.Contains(compiled.Text, forbidden) {
			t.Fatalf("evidence exposes %q: %s", forbidden, compiled.Text)
		}
	}
}

func TestExtractEvidenceFactsPrioritizesManifestDeclarationTestAndBehavior(t *testing.T) {
	before := `{"dependencies":{"obsolete":"1.0.0","zod":"4.0.0"},"scripts":{"test":"legacy-test"}}`
	after := `{"dependencies":{"zod":"4.4.3","vitest":"3.0.0"},"scripts":{"test":"legacy-test","lint":"eslint ."}}`
	packageFile := withRole(evidenceFile("package.json"), FileRoleDependency)
	packageFile.Manifest = &ManifestState{Before: &before, After: &after}
	packageFile.Hunks = []DiffHunk{evidenceHunk(nil)}
	sourceFile := evidenceFile("src/feature.ts")
	sourceFile.Hunks = []DiffHunk{evidenceHunk([]string{
		"import { value } from './value'",
		"export function enableFeature(): void {}",
		"throw new Error('Feature is unavailable')",
		"const internal = value",
	})}
	testFile := withRole(evidenceFile("src/feature.test.ts"), FileRoleTest)
	testFile.Hunks = []DiffHunk{evidenceHunk([]string{"test('enables the feature', () => expect(true).toBe(true))"})}
	facts := extractEvidenceFacts(buildChangeClusters(evidenceSnapshot([]SnapshotFile{packageFile, sourceFile, testFile})))
	assertEvidenceFact(t, facts, 1, "dependency replacement add vitest@3.0.0; remove obsolete@1.0.0")
	assertEvidenceFact(t, facts, 1, "symbol added enableFeature")
	assertEvidenceFact(t, facts, 1, `test added "enables the feature"`)
	assertEvidenceFact(t, facts, 2, "Feature is unavailable")
	assertEvidenceFact(t, facts, 3, "added const internal")
}

func TestCompileEvidenceKeepsWithinBudgetWithoutTruncatingFacts(t *testing.T) {
	oversizedLine := "throw new Error('" + strings.Repeat("x", 8_000) + "')"
	publicFile := evidenceFile("src/public.ts")
	publicFile.Hunks = []DiffHunk{evidenceHunk([]string{oversizedLine, "export const enabled = true"})}
	sensitiveFile := withPolicy(withRole(evidenceFile(".env"), FileRoleSensitive), ContentRedacted)
	sensitiveFile.Hunks = []DiffHunk{evidenceHunk([]string{"API_KEY=never-send-this"})}
	compiled := CompileEvidence(evidenceSnapshot([]SnapshotFile{publicFile, sensitiveFile}), evidenceSystem)
	if tokens := EstimateLocalPromptTokens(evidenceSystem, compiled.Text); tokens > maxLocalPromptTokens {
		t.Fatalf("tokens = %d, want <= %d", tokens, maxLocalPromptTokens)
	}
	if strings.Contains(compiled.Text, "never-send-this") || strings.Contains(compiled.Text, strings.Repeat("x", 100)) {
		t.Fatalf("evidence retained protected or oversized text: %s", compiled.Text)
	}
	if compiled.Coverage.OmittedFacts == 0 || !strings.Contains(compiled.Text, "symbol added enabled") {
		t.Fatalf("coverage = %#v, text = %s", compiled.Coverage, compiled.Text)
	}
}

func TestCompileEvidenceCompactsLowWeightDirectoriesAndAddsTypeHints(t *testing.T) {
	sourceFiles := make([]SnapshotFile, 0, 20)
	for index := 0; index < 20; index++ {
		file := evidenceFile("src/payments/core/handler-" + strconvItoa(index) + ".ts")
		file.Stats = ChangeStats{Additions: 20}
		sourceFiles = append(sourceFiles, file)
	}
	documentationFiles := make([]SnapshotFile, 0, 120)
	for index := 0; index < 120; index++ {
		documentationFiles = append(documentationFiles, withRole(evidenceFile("docs/guides/topic-"+strconvItoa(index)+"/README.md"), FileRoleDocs))
	}
	compiled := CompileEvidence(evidenceSnapshot(append(sourceFiles, documentationFiles...)), evidenceSystem)
	if !compiled.Coverage.ContentCompacted || !strings.Contains(compiled.Text, "      core/ files=20 roles=source:20 +400 -0") {
		t.Fatalf("coverage = %#v, text = %s", compiled.Coverage, compiled.Text)
	}
	for _, testCase := range []struct {
		file SnapshotFile
		hint string
	}{
		{file: withRole(evidenceFile(".github/workflows/release.yml"), FileRoleConfig), hint: "type=ci"},
		{file: withRole(evidenceFile("README.md"), FileRoleDocs), hint: "type=docs"},
		{file: evidenceFile("styles.css"), hint: "type=style"},
	} {
		if text := CompileEvidence(evidenceSnapshot([]SnapshotFile{testCase.file}), evidenceSystem).Text; !strings.Contains(text, testCase.hint) {
			t.Fatalf("evidence %q missing %q", text, testCase.hint)
		}
	}
}

func evidenceHunk(added []string) DiffHunk {
	return DiffHunk{ID: "worktree:example:0", Source: "worktree", OldStart: 1, OldLines: 0, NewStart: 1, NewLines: len(added), AddedLines: added}
}

func evidenceFile(filePath string) SnapshotFile {
	return SnapshotFile{
		ID:             filePath,
		Path:           filePath,
		Status:         "M " + filePath,
		IndexStatus:    ' ',
		WorktreeStatus: 'M',
		Role:           FileRoleSource,
		ContentPolicy:  ContentInspect,
		Stats:          ChangeStats{Additions: 1},
		Hunks:          []DiffHunk{evidenceHunk([]string{"export const value = true"})},
	}
}

func evidenceSnapshot(files []SnapshotFile) GitSnapshot {
	totals := ChangeStats{}
	for _, file := range files {
		totals.Additions += file.Stats.Additions
		totals.Deletions += file.Stats.Deletions
	}
	return GitSnapshot{RepositoryRoot: "/repo", Scope: ScopeAllUncommitted, ID: "snapshot", Files: files, Totals: totals}
}

func withRole(file SnapshotFile, role FileRole) SnapshotFile {
	file.Role = role
	return file
}

func withPolicy(file SnapshotFile, policy ContentPolicy) SnapshotFile {
	file.ContentPolicy = policy
	return file
}

func assertEvidenceFact(t *testing.T, facts []EvidenceFact, priority int, contains string) {
	t.Helper()
	for _, fact := range facts {
		if fact.Priority == priority && strings.Contains(fact.Text, contains) {
			return
		}
	}
	t.Fatalf("facts do not contain priority %d text %q: %#v", priority, contains, facts)
}

func strconvItoa(value int) string {
	return fmt.Sprintf("%d", value)
}
