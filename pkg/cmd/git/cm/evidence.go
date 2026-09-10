package cm

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

const (
	targetLocalPromptTokens     = 3_000
	maxLocalPromptTokens        = 4_000
	directoryContextTokenBudget = 600
)

type changeCluster struct {
	Key   string
	Files []SnapshotFile
}

type directorySummary struct {
	Path        string
	Files       int
	Roles       map[FileRole]int
	Additions   int
	Deletions   int
	RenamedFrom int
	RenamedTo   int
}

type directoryContext struct {
	Lines     []string
	Compacted bool
}

var (
	declarationPattern       = regexp.MustCompile(`\bexport\s+(?:default\s+)?(?:async\s+)?(?:class|interface|type|enum|function|def|func|struct|const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	testNamePattern          = regexp.MustCompile("\\b(?:describe|test|it)\\s*\\(\\s*['\"`]+([^'\"`]+)['\"`]")
	documentationHeadPattern = regexp.MustCompile(`^#{1,6} (.+)$`)
	configurationKeyPattern  = regexp.MustCompile(`(?i)^\s*["']?([a-z_$][a-z0-9_$.-]*)["']?\s*[:=]`)
	meaningfulLinePattern    = regexp.MustCompile(`^[{});,]+$`)
	behaviorLinePattern      = regexp.MustCompile(`(?i)throw\s+new|\bError\(|--[\w-]+|\b(?:error|message|title|description|help)\b`)
	hunkContextPattern       = regexp.MustCompile(`\b(?:async\s+)?(class|interface|type|function|def|func|struct)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	lowInformationPattern    = regexp.MustCompile(`^(?:import|export\s*\{).*|^//|^\*|^[{});,]+$`)
	configOnlyPattern        = regexp.MustCompile(`(?i)(?:^|/)tsconfig(?:\.|$)|\.d\.ts$`)
	stylePathPattern         = regexp.MustCompile(`(?i)\.(?:css|scss|less)$`)
	functionLinePattern      = regexp.MustCompile(`^(?:added|removed)\s+(?:async\s+)?(?:function|class|interface|type|enum|const)\b`)
	returnThrowLinePattern   = regexp.MustCompile(`^(?:added|removed)\s+(?:return|throw)\b`)
)

// CompileEvidence creates the deterministic, budgeted model evidence for a snapshot.
func CompileEvidence(snapshot GitSnapshot, system string) CompiledEvidence {
	clusters := buildChangeClusters(snapshot)
	facts := extractEvidenceFacts(clusters)
	directories := buildDirectoryContext(snapshot)
	p0 := append([]string{renderScope(snapshot)}, directories.Lines...)
	renderEvidence := func(selected []EvidenceFact) string {
		return strings.Join(append(append([]string(nil), p0...), renderSelectedFacts(selected)...), "\n")
	}
	selected := make([]EvidenceFact, 0, len(facts))
	orderedClusters := sortClustersForFacts(clusters, facts)
	for priority := 1; priority <= 3; priority++ {
		pending := make(map[string][]EvidenceFact, len(orderedClusters))
		for _, cluster := range orderedClusters {
			clusterFacts := make([]EvidenceFact, 0)
			for _, fact := range facts {
				if fact.ClusterKey == cluster.Key && fact.Priority == priority {
					clusterFacts = append(clusterFacts, fact)
				}
			}
			clusterFacts = orderFactsForCluster(clusterFacts)
			if priority == 3 {
				seenPaths := make(map[string]struct{})
				limited := make([]EvidenceFact, 0, len(clusterFacts))
				for _, fact := range clusterFacts {
					if _, found := seenPaths[fact.FilePath]; found {
						continue
					}
					seenPaths[fact.FilePath] = struct{}{}
					limited = append(limited, fact)
				}
				clusterFacts = limited
			}
			pending[cluster.Key] = clusterFacts
		}
		for hasPendingFacts(pending) {
			for _, cluster := range orderedClusters {
				queue := pending[cluster.Key]
				if len(queue) == 0 {
					continue
				}
				item := queue[0]
				pending[cluster.Key] = queue[1:]
				if priority > 1 && EstimateLocalPromptTokens(system, renderEvidence(selected)) >= targetLocalPromptTokens {
					continue
				}
				candidate := append(append([]EvidenceFact(nil), selected...), item)
				if EstimateLocalPromptTokens(system, renderEvidence(candidate)) <= maxLocalPromptTokens {
					selected = append(selected, item)
				}
			}
		}
	}
	text := renderEvidence(selected)
	return CompiledEvidence{
		Text: text,
		Coverage: EvidenceCoverage{
			EstimatedLocalPromptTokens: EstimateLocalPromptTokens(system, text),
			RepresentedClusters:        len(clusters),
			TotalClusters:              len(clusters),
			IncludedFacts:              len(selected),
			OmittedFacts:               len(facts) - len(selected),
			ContentCompacted:           directories.Compacted || len(selected) < len(facts),
		},
		Facts: facts,
	}
}

// EstimateLocalPromptTokens is the deterministic local evidence estimate used for selection.
func EstimateLocalPromptTokens(system, evidence string) int {
	payload, _ := json.Marshal([]struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{
		{Role: "system", Content: system},
		{Role: "user", Content: evidence},
	})
	return estimateTextTokens(string(payload)) + 32
}

func estimateTextTokens(text string) int {
	ascii := 0
	nonASCII := 0
	for _, character := range text {
		if character <= 0x7f {
			ascii++
		} else {
			nonASCII++
		}
	}
	return (ascii+1)/2 + nonASCII
}

func hasPendingFacts(pending map[string][]EvidenceFact) bool {
	for _, facts := range pending {
		if len(facts) > 0 {
			return true
		}
	}
	return false
}

func directoryForPath(filePath string) string {
	normalized := normalizeGitPath(filePath)
	separator := strings.LastIndexByte(normalized, '/')
	if separator < 0 {
		return "."
	}
	return normalized[:separator]
}

func directoryParent(directoryPath string) string {
	if directoryPath == "." {
		return ""
	}
	separator := strings.LastIndexByte(directoryPath, '/')
	if separator < 0 {
		return "."
	}
	return directoryPath[:separator]
}

func directoryDepth(directoryPath string) int {
	if directoryPath == "." {
		return 0
	}
	return strings.Count(directoryPath, "/") + 1
}

func newDirectorySummary(directoryPath string) *directorySummary {
	return &directorySummary{Path: directoryPath, Roles: make(map[FileRole]int)}
}

func addDirectoryFile(summary *directorySummary, file SnapshotFile, rename string) {
	summary.Files++
	summary.Roles[file.Role]++
	summary.Additions += file.Stats.Additions
	summary.Deletions += file.Stats.Deletions
	if rename == "from" {
		summary.RenamedFrom++
	}
	if rename == "to" {
		summary.RenamedTo++
	}
}

func mergeDirectorySummary(target, source *directorySummary) {
	target.Files += source.Files
	for role, count := range source.Roles {
		target.Roles[role] += count
	}
	target.Additions += source.Additions
	target.Deletions += source.Deletions
	target.RenamedFrom += source.RenamedFrom
	target.RenamedTo += source.RenamedTo
}

func directoryWeight(summary *directorySummary) int {
	sourceFiles := summary.Roles[FileRoleSource] + summary.Roles[FileRoleTest]
	supportingFiles := summary.Files - sourceFiles
	return sourceFiles*4 + supportingFiles + (summary.Additions+summary.Deletions+99)/100
}

func directoryRoleSummary(summary *directorySummary) string {
	roles := make([]string, 0, len(summary.Roles))
	for role := range summary.Roles {
		roles = append(roles, string(role))
	}
	sort.Strings(roles)
	parts := make([]string, 0, len(roles))
	for _, role := range roles {
		parts = append(parts, fmt.Sprintf("%s:%d", role, summary.Roles[FileRole(role)]))
	}
	return strings.Join(parts, ",")
}

func renderDirectorySummary(summary *directorySummary) string {
	name := "./"
	if summary.Path != "." {
		name = summary.Path[strings.LastIndexByte(summary.Path, '/')+1:] + "/"
	}
	details := ""
	if summary.Files > 0 {
		details = fmt.Sprintf(" files=%d roles=%s +%d -%d", summary.Files, directoryRoleSummary(summary), summary.Additions, summary.Deletions)
	}
	renames := ""
	if summary.RenamedFrom != 0 || summary.RenamedTo != 0 {
		renames = fmt.Sprintf(" rename-from=%d rename-to=%d", summary.RenamedFrom, summary.RenamedTo)
	}
	return strings.TrimRight(name+details+renames, " ")
}

func renderDirectoryLines(summaries map[string]*directorySummary) []string {
	paths := make(map[string]struct{})
	for directoryPath := range summaries {
		for current := directoryPath; current != ""; current = directoryParent(current) {
			paths[current] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(paths))
	for directoryPath := range paths {
		ordered = append(ordered, directoryPath)
	}
	sort.Slice(ordered, func(left, right int) bool {
		leftDepth := directoryDepth(ordered[left])
		rightDepth := directoryDepth(ordered[right])
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return ordered[left] < ordered[right]
	})
	lines := make([]string, 0, len(ordered))
	for _, directoryPath := range ordered {
		indent := strings.Repeat("  ", directoryDepth(directoryPath))
		if summary := summaries[directoryPath]; summary != nil {
			lines = append(lines, indent+renderDirectorySummary(summary))
			continue
		}
		segment := "./"
		if directoryPath != "." {
			segment = directoryPath[strings.LastIndexByte(directoryPath, '/')+1:] + "/"
		}
		lines = append(lines, indent+segment)
	}
	return lines
}

func directoryContextTokens(lines []string) int {
	return estimateTextTokens(strings.Join(lines, "\n"))
}

func compactDirectorySummaries(summaries map[string]*directorySummary) bool {
	compacted := false
	for directoryContextTokens(append([]string{"DIRECTORY_CONTEXT"}, renderDirectoryLines(summaries)...)) > directoryContextTokenBudget {
		children := make([]string, 0, len(summaries))
		for directoryPath := range summaries {
			if directoryParent(directoryPath) != "" {
				children = append(children, directoryPath)
			}
		}
		if len(children) == 0 {
			break
		}
		sort.Slice(children, func(left, right int) bool {
			leftSummary := summaries[children[left]]
			rightSummary := summaries[children[right]]
			leftWeight := directoryWeight(leftSummary)
			rightWeight := directoryWeight(rightSummary)
			if leftWeight != rightWeight {
				return leftWeight < rightWeight
			}
			leftDepth := directoryDepth(directoryParent(children[left]))
			rightDepth := directoryDepth(directoryParent(children[right]))
			if leftDepth != rightDepth {
				return leftDepth > rightDepth
			}
			return children[left] < children[right]
		})
		child := children[0]
		parentPath := directoryParent(child)
		parent := summaries[parentPath]
		if parent == nil {
			parent = newDirectorySummary(parentPath)
			summaries[parentPath] = parent
		}
		mergeDirectorySummary(parent, summaries[child])
		delete(summaries, child)
		compacted = true
	}
	return compacted
}

func buildDirectoryContext(snapshot GitSnapshot) directoryContext {
	summaries := make(map[string]*directorySummary)
	for _, file := range snapshot.Files {
		if file.ContentPolicy != ContentInspect {
			continue
		}
		targetPath := directoryForPath(file.Path)
		target := summaries[targetPath]
		if target == nil {
			target = newDirectorySummary(targetPath)
			summaries[targetPath] = target
		}
		rename := ""
		if file.OriginalPath != "" {
			rename = "to"
		}
		addDirectoryFile(target, file, rename)
		if file.OriginalPath != "" {
			originalPath := directoryForPath(file.OriginalPath)
			original := summaries[originalPath]
			if original == nil {
				original = newDirectorySummary(originalPath)
				summaries[originalPath] = original
			}
			original.RenamedFrom++
		}
	}
	compacted := compactDirectorySummaries(summaries)
	lines := []string{"DIRECTORY_CONTEXT"}
	if len(summaries) == 0 {
		lines = append(lines, "(no inspectable changed directories)")
	} else {
		lines = append(lines, renderDirectoryLines(summaries)...)
	}
	return directoryContext{Lines: lines, Compacted: compacted}
}

func moduleRoot(filePath string) string {
	normalized := normalizeGitPath(filePath)
	parts := strings.Split(normalized, "/")
	if len(parts) >= 2 {
		switch parts[0] {
		case "packages", "apps", "services", "crates":
			return strings.Join(parts[:2], "/")
		}
	}
	if len(parts) >= 3 && parts[0] == "src" && parts[1] == "commands" {
		return strings.Join(parts[:3], "/")
	}
	return ""
}

func commonDirectoryDepth(left, right string) int {
	leftParts := []string{}
	rightParts := []string{}
	if left != "." {
		leftParts = strings.Split(left, "/")
	}
	if right != "." {
		rightParts = strings.Split(right, "/")
	}
	depth := 0
	for depth < len(leftParts) && depth < len(rightParts) && leftParts[depth] == rightParts[depth] {
		depth++
	}
	return depth
}

func initialClusterKey(file SnapshotFile) string {
	if root := moduleRoot(file.Path); root != "" {
		return root
	}
	return directoryForPath(file.Path)
}

func attachSupportingFile(file SnapshotFile, keys []string) string {
	fileDirectory := directoryForPath(file.Path)
	bestKey := ""
	bestDepth := 0
	for _, key := range keys {
		depth := commonDirectoryDepth(fileDirectory, key)
		if depth > bestDepth || (depth == bestDepth && depth > 0 && (bestKey == "" || key < bestKey)) {
			bestKey = key
			bestDepth = depth
		}
	}
	if bestKey != "" {
		return bestKey
	}
	return initialClusterKey(file)
}

func buildChangeClusters(snapshot GitSnapshot) []changeCluster {
	clusters := make(map[string][]SnapshotFile)
	primary := make([]SnapshotFile, 0, len(snapshot.Files))
	for _, file := range snapshot.Files {
		if file.Role != FileRoleConfig && file.Role != FileRoleDependency && file.Role != FileRoleDocs {
			primary = append(primary, file)
			key := initialClusterKey(file)
			clusters[key] = append(clusters[key], file)
		}
	}
	keys := make([]string, 0, len(clusters))
	for key := range clusters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, file := range snapshot.Files {
		isPrimary := false
		for _, primaryFile := range primary {
			if primaryFile.Path == file.Path && primaryFile.ID == file.ID {
				isPrimary = true
				break
			}
		}
		if isPrimary {
			continue
		}
		key := attachSupportingFile(file, keys)
		if _, found := clusters[key]; !found {
			keys = append(keys, key)
		}
		clusters[key] = append(clusters[key], file)
	}
	result := make([]changeCluster, 0, len(clusters))
	for key, files := range clusters {
		sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
		result = append(result, changeCluster{Key: key, Files: files})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Key < result[right].Key })
	return result
}

func roleRank(role FileRole) int {
	switch role {
	case FileRoleDependency, FileRoleConfig:
		return 2
	case FileRoleTest:
		return 3
	case FileRoleGenerated, FileRoleBinary, FileRoleSensitive:
		return 6
	case FileRoleSource:
		return 4
	default:
		return 5
	}
}

func newEvidenceFact(priority int, clusterKey, filePath, suffix, text, hunkID string) EvidenceFact {
	return EvidenceFact{ID: fmt.Sprintf("%d:%s:%s:%s", priority, clusterKey, filePath, suffix), Priority: priority, ClusterKey: clusterKey, FilePath: filePath, HunkID: hunkID, Text: text}
}

func declarationName(line string) string {
	match := declarationPattern.FindStringSubmatch(line)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func testName(line string) string {
	match := testNamePattern.FindStringSubmatch(line)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func documentationHeading(line string) string {
	match := documentationHeadPattern.FindStringSubmatch(strings.TrimSpace(line))
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func configurationKey(line string) string {
	match := configurationKeyPattern.FindStringSubmatch(line)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func meaningfulLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed != "" && !meaningfulLinePattern.MatchString(trimmed)
}

func isBehaviorLine(line string) bool {
	return behaviorLinePattern.MatchString(line)
}

func hunkContext(line string) string {
	match := hunkContextPattern.FindStringSubmatch(line)
	if len(match) < 3 {
		return ""
	}
	return "context " + match[1] + " " + match[2]
}

func isLowInformationLine(line string) bool {
	return lowInformationPattern.MatchString(strings.TrimSpace(line))
}

func changeKind(file SnapshotFile, line string) string {
	for _, hunk := range file.Hunks {
		for _, added := range hunk.AddedLines {
			if added == line {
				return "added"
			}
		}
	}
	return "removed"
}

func packageFacts(file SnapshotFile, clusterKey string) []EvidenceFact {
	if file.Manifest == nil {
		return nil
	}
	if file.Manifest.Before == nil {
		return []EvidenceFact{newEvidenceFact(1, clusterKey, file.Path, "package-added", "package manifest added", "")}
	}
	if file.Manifest.After == nil {
		return []EvidenceFact{newEvidenceFact(1, clusterKey, file.Path, "package-removed", "package manifest removed", "")}
	}
	before := map[string]any{}
	after := map[string]any{}
	if json.Unmarshal([]byte(*file.Manifest.Before), &before) != nil || json.Unmarshal([]byte(*file.Manifest.After), &after) != nil {
		return []EvidenceFact{newEvidenceFact(1, clusterKey, file.Path, "package-unparsed", "package manifest changed", "")}
	}
	facts := make([]EvidenceFact, 0)
	for _, field := range []string{"dependencies", "devDependencies"} {
		beforeDependencies := stringMap(before[field])
		afterDependencies := stringMap(after[field])
		names := make(map[string]struct{}, len(beforeDependencies)+len(afterDependencies))
		for name := range beforeDependencies {
			names[name] = struct{}{}
		}
		for name := range afterDependencies {
			names[name] = struct{}{}
		}
		orderedNames := mapKeys(names)
		added := make([]string, 0)
		removed := make([]string, 0)
		changed := make([]string, 0)
		for _, name := range orderedNames {
			beforeValue := beforeDependencies[name]
			afterValue := afterDependencies[name]
			if beforeValue == afterValue {
				continue
			}
			if beforeValue == "" {
				added = append(added, name+"@"+afterValue)
			} else if afterValue == "" {
				removed = append(removed, name+"@"+beforeValue)
			} else {
				changed = append(changed, name+" "+beforeValue+" -> "+afterValue)
			}
		}
		if len(added) > 0 && len(removed) > 0 {
			facts = append(facts, newEvidenceFact(1, clusterKey, file.Path, "dependency:"+field+":replacement", "dependency replacement add "+strings.Join(added, ", ")+"; remove "+strings.Join(removed, ", "), ""))
		} else {
			if len(added) > 0 {
				facts = append(facts, newEvidenceFact(1, clusterKey, file.Path, "dependency:"+field+":added", "dependency added "+strings.Join(added, ", "), ""))
			}
			if len(removed) > 0 {
				facts = append(facts, newEvidenceFact(1, clusterKey, file.Path, "dependency:"+field+":removed", "dependency removed "+strings.Join(removed, ", "), ""))
			}
		}
		if len(changed) > 0 {
			facts = append(facts, newEvidenceFact(1, clusterKey, file.Path, "dependency:"+field+":changed", "dependency updated "+strings.Join(changed, ", "), ""))
		}
	}
	for _, field := range []string{"scripts", "name", "version", "type", "exports", "bin", "engines"} {
		if jsonValuesEqual(before[field], after[field]) {
			continue
		}
		text := "package " + field + " changed"
		if field == "version" {
			text = "release chore " + jsonString(before[field]) + "->" + jsonString(after[field])
		}
		facts = append(facts, newEvidenceFact(1, clusterKey, file.Path, "manifest:"+field, text, ""))
	}
	if len(facts) == 0 {
		return []EvidenceFact{newEvidenceFact(1, clusterKey, file.Path, "package-generic", "package manifest changed", "")}
	}
	return facts
}

func stringMap(value any) map[string]string {
	result := make(map[string]string)
	values, ok := value.(map[string]any)
	if !ok {
		return result
	}
	for key, item := range values {
		stringValue, ok := item.(string)
		if ok {
			result[key] = stringValue
		}
	}
	return result
}

func mapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func jsonValuesEqual(left, right any) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftBytes) == string(rightBytes)
}

func jsonString(value any) string {
	if value == nil {
		return "undefined"
	}
	return fmt.Sprint(value)
}

func extractEvidenceFacts(clusters []changeCluster) []EvidenceFact {
	facts := make([]EvidenceFact, 0)
	for _, cluster := range clusters {
		for _, file := range cluster.Files {
			if file.ContentPolicy != ContentInspect {
				continue
			}
			factsBeforeFile := len(facts)
			if file.Role == FileRoleDependency {
				facts = append(facts, packageFacts(file, cluster.Key)...)
				continue
			}
			if file.OriginalPath != "" && (file.IndexStatus == 'R' || file.IndexStatus == 'C') {
				kind := "rename"
				if file.IndexStatus == 'C' {
					kind = "copy"
				}
				facts = append(facts, newEvidenceFact(1, cluster.Key, file.Path, "rename", kind+" from "+file.OriginalPath, ""))
			}
			includedHeading := false
			includedDeclaration := false
			includedTest := false
			includedDocumentationHeading := false
			for _, hunk := range file.Hunks {
				if context := hunkContext(hunk.Heading); context != "" && file.Role != FileRoleTest && !includedHeading {
					facts = append(facts, newEvidenceFact(1, cluster.Key, file.Path, "heading:"+hunk.ID, context, hunk.ID))
					includedHeading = true
				}
				lines := append(append([]string(nil), hunk.AddedLines...), hunk.DeletedLines...)
				for _, line := range lines {
					kind := changeKind(file, line)
					if declaration := declarationName(line); declaration != "" && file.Role != FileRoleTest && !includedDeclaration {
						facts = append(facts, newEvidenceFact(1, cluster.Key, file.Path, "declaration:"+hunk.ID+":"+declaration, "symbol "+kind+" "+declaration, hunk.ID))
						includedDeclaration = true
					}
					if test := testName(line); test != "" && !includedTest {
						facts = append(facts, newEvidenceFact(1, cluster.Key, file.Path, "test:"+hunk.ID+":"+test, "test "+kind+" "+quoteJSONString(test), hunk.ID))
						includedTest = true
					}
					if heading := documentationHeading(line); heading != "" && file.Role == FileRoleDocs && !includedDocumentationHeading {
						facts = append(facts, newEvidenceFact(1, cluster.Key, file.Path, "docs:"+hunk.ID+":"+heading, "docs "+kind+" "+quoteJSONString(heading), hunk.ID))
						includedDocumentationHeading = true
					}
					if key := configurationKey(line); key != "" && file.Role == FileRoleConfig {
						facts = append(facts, newEvidenceFact(1, cluster.Key, file.Path, "config:"+hunk.ID+":"+key, "config "+kind+" "+key, hunk.ID))
					}
				}
				if file.Role == FileRoleTest || file.Role == FileRoleDocs {
					continue
				}
				includedBehavior := false
				candidateIndex := 0
				for _, candidate := range append(lineCandidates("added", hunk.AddedLines), lineCandidates("removed", hunk.DeletedLines)...) {
					trimmed := strings.TrimSpace(candidate.line)
					if !meaningfulLine(candidate.line) || isLowInformationLine(candidate.line) || declarationName(trimmed) != "" || testName(trimmed) != "" {
						continue
					}
					priority := 3
					if isBehaviorLine(trimmed) {
						priority = 2
					}
					if priority == 2 && includedBehavior {
						continue
					}
					if priority == 2 {
						includedBehavior = true
					}
					facts = append(facts, newEvidenceFact(priority, cluster.Key, file.Path, fmt.Sprintf("line:%s:%d", hunk.ID, candidateIndex), candidate.kind+" "+trimmed, hunk.ID))
					candidateIndex++
				}
			}
			hasImportantFact := false
			for _, fact := range facts[factsBeforeFile:] {
				if fact.Priority < 3 {
					hasImportantFact = true
					break
				}
			}
			if !hasImportantFact {
				facts = append(facts, newEvidenceFact(1, cluster.Key, file.Path, "file", "file changed", ""))
			}
		}
	}
	unique := make(map[string]EvidenceFact, len(facts))
	for _, fact := range facts {
		unique[fact.ID] = fact
	}
	result := make([]EvidenceFact, 0, len(unique))
	for _, fact := range unique {
		result = append(result, fact)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Priority != result[right].Priority {
			return result[left].Priority < result[right].Priority
		}
		if result[left].ClusterKey != result[right].ClusterKey {
			return result[left].ClusterKey < result[right].ClusterKey
		}
		if result[left].FilePath != result[right].FilePath {
			return result[left].FilePath < result[right].FilePath
		}
		if result[left].HunkID != result[right].HunkID {
			return result[left].HunkID < result[right].HunkID
		}
		return result[left].ID < result[right].ID
	})
	return result
}

type lineCandidate struct {
	kind string
	line string
}

func lineCandidates(kind string, lines []string) []lineCandidate {
	result := make([]lineCandidate, 0, len(lines))
	for _, line := range lines {
		result = append(result, lineCandidate{kind: kind, line: line})
	}
	return result
}

func quoteJSONString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func protectionCounts(snapshot GitSnapshot) (metadataOnly, redacted int) {
	for _, file := range snapshot.Files {
		switch file.ContentPolicy {
		case ContentMetadataOnly:
			metadataOnly++
		case ContentRedacted:
			redacted++
		}
	}
	return metadataOnly, redacted
}

func packageVersionChanged(file SnapshotFile) bool {
	if path.Base(file.Path) != "package.json" || file.Manifest == nil || file.Manifest.Before == nil || file.Manifest.After == nil {
		return false
	}
	before := map[string]any{}
	after := map[string]any{}
	if json.Unmarshal([]byte(*file.Manifest.Before), &before) != nil || json.Unmarshal([]byte(*file.Manifest.After), &after) != nil {
		return false
	}
	return !jsonValuesEqual(before["version"], after["version"])
}

func commitTypeHint(snapshot GitSnapshot) string {
	if len(snapshot.Files) == 0 {
		return ""
	}
	all := func(predicate func(SnapshotFile) bool) bool {
		for _, file := range snapshot.Files {
			if !predicate(file) {
				return false
			}
		}
		return true
	}
	for _, file := range snapshot.Files {
		if strings.HasPrefix(file.Path, ".github/workflows/") {
			return "ci"
		}
	}
	for _, file := range snapshot.Files {
		if strings.EqualFold(path.Base(file.Path), "dockerfile") {
			return "build"
		}
	}
	if all(func(file SnapshotFile) bool {
		return file.Role == FileRoleConfig || configOnlyPattern.MatchString(file.Path)
	}) {
		return "build"
	}
	if all(func(file SnapshotFile) bool { return file.Role == FileRoleDocs }) {
		return "docs"
	}
	if all(func(file SnapshotFile) bool { return stylePathPattern.MatchString(file.Path) }) {
		return "style"
	}
	if all(func(file SnapshotFile) bool { return file.Role == FileRoleTest }) {
		return "test"
	}
	hasVersionChange := false
	for _, file := range snapshot.Files {
		if packageVersionChanged(file) {
			hasVersionChange = true
			break
		}
	}
	if hasVersionChange && all(func(file SnapshotFile) bool { return file.Role == FileRoleDependency || file.Role == FileRoleGenerated }) {
		return "chore"
	}
	if all(func(file SnapshotFile) bool { return file.Role == FileRoleDependency || file.Role == FileRoleGenerated }) {
		return "chore"
	}
	return ""
}

func renderScope(snapshot GitSnapshot) string {
	metadataOnly, redacted := protectionCounts(snapshot)
	text := fmt.Sprintf("CHANGE_SUMMARY files=%d +%d -%d protected=%d/%d", len(snapshot.Files), snapshot.Totals.Additions, snapshot.Totals.Deletions, metadataOnly, redacted)
	if hint := commitTypeHint(snapshot); hint != "" {
		return text + " type=" + hint
	}
	return text
}

func sortClustersForFacts(clusters []changeCluster, facts []EvidenceFact) []changeCluster {
	priorities := make(map[string]int, len(clusters))
	for _, cluster := range clusters {
		priority := 10
		for _, fact := range facts {
			if fact.ClusterKey == cluster.Key && fact.Priority < priority {
				priority = fact.Priority
			}
		}
		for _, file := range cluster.Files {
			if rank := roleRank(file.Role); rank < priority {
				priority = rank
			}
		}
		priorities[cluster.Key] = priority
	}
	result := append([]changeCluster(nil), clusters...)
	sort.Slice(result, func(left, right int) bool {
		if priorities[result[left].Key] != priorities[result[right].Key] {
			return priorities[result[left].Key] < priorities[result[right].Key]
		}
		return result[left].Key < result[right].Key
	})
	return result
}

func factRank(item EvidenceFact) int {
	if strings.HasPrefix(item.Text, "rename ") || strings.HasPrefix(item.Text, "copy ") || strings.HasPrefix(item.Text, "dependency ") || strings.HasPrefix(item.Text, "package ") || strings.HasPrefix(item.Text, "config ") {
		return 1
	}
	if strings.HasPrefix(item.Text, "symbol ") {
		return 2
	}
	if strings.HasPrefix(item.Text, "test ") {
		return 3
	}
	if strings.HasPrefix(item.Text, "docs ") {
		return 4
	}
	if strings.HasPrefix(item.Text, "context ") {
		return 5
	}
	if functionLinePattern.MatchString(item.Text) {
		return 6
	}
	if returnThrowLinePattern.MatchString(item.Text) {
		return 8
	}
	return 7
}

func orderFactsForCluster(facts []EvidenceFact) []EvidenceFact {
	byFile := make(map[string][]EvidenceFact)
	for _, fact := range facts {
		byFile[fact.FilePath] = append(byFile[fact.FilePath], fact)
	}
	paths := make([]string, 0, len(byFile))
	for filePath := range byFile {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	for _, filePath := range paths {
		fileFacts := byFile[filePath]
		sort.Slice(fileFacts, func(left, right int) bool {
			if factRank(fileFacts[left]) != factRank(fileFacts[right]) {
				return factRank(fileFacts[left]) < factRank(fileFacts[right])
			}
			if fileFacts[left].HunkID != fileFacts[right].HunkID {
				return fileFacts[left].HunkID < fileFacts[right].HunkID
			}
			return fileFacts[left].ID < fileFacts[right].ID
		})
		byFile[filePath] = fileFacts
	}
	result := make([]EvidenceFact, 0, len(facts))
	for {
		added := false
		for _, filePath := range paths {
			if len(byFile[filePath]) == 0 {
				continue
			}
			result = append(result, byFile[filePath][0])
			byFile[filePath] = byFile[filePath][1:]
			added = true
		}
		if !added {
			return result
		}
	}
}

func renderSelectedFacts(facts []EvidenceFact) []string {
	if len(facts) == 0 {
		return nil
	}
	directories := make(map[string]map[string][]EvidenceFact)
	for _, fact := range facts {
		directoryPath := directoryForPath(fact.FilePath)
		files := directories[directoryPath]
		if files == nil {
			files = make(map[string][]EvidenceFact)
			directories[directoryPath] = files
		}
		files[fact.FilePath] = append(files[fact.FilePath], fact)
	}
	directoryPaths := make([]string, 0, len(directories))
	for directoryPath := range directories {
		directoryPaths = append(directoryPaths, directoryPath)
	}
	sort.Strings(directoryPaths)
	lines := []string{"FACTS"}
	for _, directoryPath := range directoryPaths {
		if directoryPath == "." {
			lines = append(lines, "./")
		} else {
			lines = append(lines, directoryPath+"/")
		}
		files := directories[directoryPath]
		filePaths := make([]string, 0, len(files))
		for filePath := range files {
			filePaths = append(filePaths, filePath)
		}
		sort.Strings(filePaths)
		for _, filePath := range filePaths {
			fileName := filePath
			if directoryPath != "." {
				fileName = strings.TrimPrefix(filePath, directoryPath+"/")
			}
			fileFacts := files[filePath]
			if len(fileFacts) == 1 {
				lines = append(lines, "  "+fileName+": "+fileFacts[0].Text)
				continue
			}
			lines = append(lines, "  "+fileName+":")
			for _, fact := range fileFacts {
				lines = append(lines, "    "+fact.Text)
			}
		}
	}
	return lines
}
