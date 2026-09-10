package cm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const untrackedCaptureConcurrency = 8

// GitInputRunner is required only for Git's byte-oriented cat-file protocol.
type GitInputRunner interface {
	GitRunner
	RunInput(context.Context, []string, []byte) (GitOutput, error)
}

// SnapshotFileSystem contains the local read operations used during snapshot capture.
type SnapshotFileSystem interface {
	Lstat(string) (fs.FileInfo, error)
	Open(string) (io.ReadCloser, error)
	ReadFile(string) ([]byte, error)
}

type numstat struct {
	Additions int
	Deletions int
	Binary    bool
}

type parsedPatchFile struct {
	Hunks     []DiffHunk
	RawLength int
}

type untrackedContent struct {
	Patch  string
	Hash   string
	Binary bool
}

// CaptureSnapshot captures the complete immutable Git scope used for one generation.
func CaptureSnapshot(ctx context.Context, runner GitRunner, fileSystem SnapshotFileSystem, scope Scope) (GitSnapshot, error) {
	if fileSystem == nil {
		return GitSnapshot{}, captureError(errors.New("Git snapshot filesystem is required"))
	}
	state, err := InspectRepository(ctx, runner, scope)
	if err != nil {
		return GitSnapshot{}, err
	}
	files := snapshotScopeFiles(state.Files, scope)

	type result struct {
		output GitOutput
		err    error
	}
	run := func(arguments []string) <-chan result {
		response := make(chan result, 1)
		go func() {
			output, err := runner.Run(ctx, arguments)
			response <- result{output: output, err: err}
		}()
		return response
	}
	cachedPatchResult := run([]string{"-C", state.Root, "diff", "--cached", "--no-ext-diff", "--find-renames", "--unified=0"})
	cachedNumstatResult := run([]string{"-C", state.Root, "diff", "--cached", "--numstat", "-z", "--find-renames"})
	var worktreePatchResult, worktreeNumstatResult <-chan result
	type untrackedResult struct {
		contents map[string]untrackedContent
		err      error
	}
	var untrackedResultChannel <-chan untrackedResult
	if scope == ScopeAllUncommitted {
		worktreePatchResult = run([]string{"-C", state.Root, "diff", "--no-ext-diff", "--find-renames", "--unified=0"})
		worktreeNumstatResult = run([]string{"-C", state.Root, "diff", "--numstat", "-z", "--find-renames"})
		response := make(chan untrackedResult, 1)
		go func() {
			contents, err := captureUntrackedContents(ctx, fileSystem, state.Root, files)
			response <- untrackedResult{contents: contents, err: err}
		}()
		untrackedResultChannel = response
	}

	cachedPatch, err := awaitGitResult(<-cachedPatchResult, "git diff --cached failed")
	if err != nil {
		return GitSnapshot{}, captureError(err)
	}
	cachedNumstat, err := awaitGitResult(<-cachedNumstatResult, "git diff --cached --numstat failed")
	if err != nil {
		return GitSnapshot{}, captureError(err)
	}
	worktreePatch := GitOutput{}
	worktreeNumstat := GitOutput{}
	if scope == ScopeAllUncommitted {
		worktreePatch, err = awaitGitResult(<-worktreePatchResult, "git diff failed")
		if err != nil {
			return GitSnapshot{}, captureError(err)
		}
		worktreeNumstat, err = awaitGitResult(<-worktreeNumstatResult, "git diff --numstat failed")
		if err != nil {
			return GitSnapshot{}, captureError(err)
		}
	}

	untracked := map[string]untrackedContent{}
	if scope == ScopeAllUncommitted {
		captured := <-untrackedResultChannel
		if captured.err != nil {
			return GitSnapshot{}, captureError(captured.err)
		}
		untracked = captured.contents
	}
	stagedPatches := parseUnifiedPatch(string(cachedPatch.Stdout), "staged")
	worktreePatches := parseUnifiedPatch(string(worktreePatch.Stdout), "worktree")
	untrackedPatches := make(map[string]parsedPatchFile)
	for filePath, content := range untracked {
		if content.Patch == "" {
			continue
		}
		parsed := parseUnifiedPatch(content.Patch, "untracked")
		if patch, found := parsed[filePath]; found {
			untrackedPatches[filePath] = patch
		} else {
			untrackedPatches[filePath] = parsedPatchFile{RawLength: len(content.Patch)}
		}
	}
	stats := mergeNumstats(parseNumstat(cachedNumstat.Stdout), parseNumstat(worktreeNumstat.Stdout))
	for filePath, content := range untracked {
		if patch, found := untrackedPatches[filePath]; found {
			additions := 0
			for _, hunk := range patch.Hunks {
				additions += len(hunk.AddedLines)
			}
			stats[filePath] = numstat{Additions: additions, Binary: content.Binary}
		} else if content.Binary {
			stats[filePath] = numstat{Binary: true}
		}
	}
	manifests, err := captureManifestStates(ctx, runner, fileSystem, state.Root, scope, files)
	if err != nil {
		return GitSnapshot{}, captureError(err)
	}

	snapshotFiles := make([]SnapshotFile, 0, len(files))
	for _, file := range files {
		fileStats := stats[file.Path]
		patches := []parsedPatchFile{}
		if patch, found := stagedPatches[file.Path]; found {
			patches = append(patches, patch)
		}
		if patch, found := worktreePatches[file.Path]; found {
			patches = append(patches, patch)
		}
		if patch, found := untrackedPatches[file.Path]; found {
			patches = append(patches, patch)
		}
		role := fileRoleForPath(file.Path, fileStats.Binary)
		size, exists, err := snapshotFileSize(fileSystem, filepath.Join(state.Root, file.Path))
		if err != nil {
			return GitSnapshot{}, captureError(err)
		}
		overSizedPatch := false
		for _, patch := range patches {
			if patch.RawLength > largeEvidenceFileBytes {
				overSizedPatch = true
				break
			}
		}
		policy := contentPolicyFor(role, size, exists, overSizedPatch)
		hunks := make([]DiffHunk, 0)
		if policy == ContentInspect {
			for _, patch := range patches {
				hunks = append(hunks, patch.Hunks...)
			}
		}
		snapshotFiles = append(snapshotFiles, SnapshotFile{
			ID:             snapshotFileID(file),
			Path:           file.Path,
			OriginalPath:   file.OriginalPath,
			Status:         file.Status,
			IndexStatus:    file.IndexStatus,
			WorktreeStatus: file.WorktreeStatus,
			Role:           role,
			ContentPolicy:  policy,
			Stats:          ChangeStats{Additions: fileStats.Additions, Deletions: fileStats.Deletions},
			Hunks:          hunks,
			Manifest:       manifests[file.Path],
		})
	}
	sort.Slice(snapshotFiles, func(left, right int) bool { return snapshotFiles[left].Path < snapshotFiles[right].Path })
	return GitSnapshot{
		RepositoryRoot: state.Root,
		Scope:          scope,
		ID:             snapshotID(scope, files, string(cachedPatch.Stdout), string(worktreePatch.Stdout), untracked),
		Files:          snapshotFiles,
		Totals:         snapshotTotals(snapshotFiles),
	}, nil
}

// AssertSnapshotCurrent rejects a changed scope immediately before commit.
func AssertSnapshotCurrent(ctx context.Context, runner GitRunner, fileSystem SnapshotFileSystem, scope Scope, expectedID string) error {
	current, err := CaptureSnapshot(ctx, runner, fileSystem, scope)
	if err != nil {
		return err
	}
	if current.ID == expectedID {
		return nil
	}
	return &CommandError{
		Code: ErrorStaleScope,
		Text: "Git changes changed after the commit message was generated. Generate a new message before committing.",
	}
}

func awaitGitResult(result struct {
	output GitOutput
	err    error
}, fallback string) (GitOutput, error) {
	if result.err != nil {
		return GitOutput{}, result.err
	}
	if result.output.ExitCode != 0 {
		return GitOutput{}, gitOutputError(result.output, fallback)
	}
	return result.output, nil
}

func snapshotScopeFiles(files []FileChange, scope Scope) []FileChange {
	result := append([]FileChange(nil), files...)
	if scope != ScopeStaged {
		return result
	}
	for index := range result {
		result[index].WorktreeStatus = ' '
		result[index].Status = formatGitStatus(result[index].IndexStatus, ' ', result[index].Path, result[index].OriginalPath)
	}
	return result
}

func snapshotFileSize(fileSystem SnapshotFileSystem, filePath string) (size int64, exists bool, err error) {
	info, err := fileSystem.Lstat(filePath)
	if err == nil {
		return info.Size(), true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}
	return 0, false, err
}

func captureUntrackedContents(ctx context.Context, fileSystem SnapshotFileSystem, root string, files []FileChange) (map[string]untrackedContent, error) {
	untracked := make([]FileChange, 0)
	for _, file := range files {
		if file.IndexStatus == '?' && file.WorktreeStatus == '?' {
			untracked = append(untracked, file)
		}
	}
	result := make(map[string]untrackedContent, len(untracked))
	if len(untracked) == 0 {
		return result, nil
	}
	type capture struct {
		path    string
		content untrackedContent
		err     error
	}
	jobs := make(chan FileChange)
	results := make(chan capture, len(untracked))
	workers := untrackedCaptureConcurrency
	if workers > len(untracked) {
		workers = len(untracked)
	}
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for file := range jobs {
				content, err := captureUntrackedFile(ctx, fileSystem, root, file)
				results <- capture{path: file.Path, content: content, err: err}
			}
		}()
	}
	go func() {
		for _, file := range untracked {
			jobs <- file
		}
		close(jobs)
		group.Wait()
		close(results)
	}()
	for response := range results {
		if response.err != nil {
			return nil, response.err
		}
		result[response.path] = response.content
	}
	return result, nil
}

func captureUntrackedFile(ctx context.Context, fileSystem SnapshotFileSystem, root string, file FileChange) (untrackedContent, error) {
	if err := ctx.Err(); err != nil {
		return untrackedContent{}, err
	}
	filePath := filepath.Join(root, file.Path)
	role := fileRoleForPath(file.Path, false)
	size, exists, err := snapshotFileSize(fileSystem, filePath)
	if err != nil {
		return untrackedContent{}, err
	}
	policy := contentPolicyFor(role, size, exists, false)
	hash, err := hashSnapshotFile(fileSystem, filePath)
	if err != nil {
		return untrackedContent{}, err
	}
	if policy != ContentInspect {
		return untrackedContent{Hash: hash, Binary: role == FileRoleBinary}, nil
	}
	contents, err := fileSystem.ReadFile(filePath)
	if err != nil {
		return untrackedContent{}, err
	}
	if strings.IndexByte(string(contents), 0) >= 0 {
		return untrackedContent{Hash: hash, Binary: true}, nil
	}
	return untrackedContent{Hash: hash, Patch: untrackedPatch(file.Path, string(contents))}, nil
}

func hashSnapshotFile(fileSystem SnapshotFileSystem, filePath string) (string, error) {
	reader, err := fileSystem.Open(filePath)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func untrackedPatch(filePath, text string) string {
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		lines = lines[:len(lines)-1]
	}
	result := []string{
		"diff --git a/" + filePath + " b/" + filePath,
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/" + filePath,
		fmt.Sprintf("@@ -0,0 +1,%d @@", len(lines)),
	}
	for _, line := range lines {
		result = append(result, "+"+line)
	}
	return strings.Join(result, "\n")
}

func parseUnifiedPatch(patch, source string) map[string]parsedPatchFile {
	parsed := make(map[string]parsedPatchFile)
	sections := strings.Split(patch, "diff --git ")
	for _, section := range sections[1:] {
		lines := strings.Split(section, "\n")
		oldPath := ""
		newPath := ""
		for _, line := range lines {
			if strings.HasPrefix(line, "--- ") {
				oldPath = stripDiffPath(strings.SplitN(line[4:], "\t", 2)[0])
			}
			if strings.HasPrefix(line, "+++ ") {
				newPath = stripDiffPath(strings.SplitN(line[4:], "\t", 2)[0])
				break
			}
		}
		filePath := newPath
		if filePath == "" {
			filePath = oldPath
		}
		if filePath == "" {
			continue
		}
		hunks := make([]DiffHunk, 0)
		current := -1
		for _, line := range lines {
			if hunk, ok := parseHunkHeader(line); ok {
				hunk.ID = fmt.Sprintf("%s:%s:%d", source, filePath, len(hunks))
				hunk.Source = source
				hunks = append(hunks, hunk)
				current = len(hunks) - 1
				continue
			}
			if current < 0 {
				continue
			}
			if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				hunks[current].AddedLines = append(hunks[current].AddedLines, line[1:])
			} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
				hunks[current].DeletedLines = append(hunks[current].DeletedLines, line[1:])
			}
		}
		parsed[filePath] = parsedPatchFile{Hunks: hunks, RawLength: len(section) + len("diff --git ")}
	}
	return parsed
}

func stripDiffPath(value string) string {
	if value == "/dev/null" {
		return ""
	}
	value = strings.TrimSuffix(strings.TrimPrefix(value, "\""), "\"")
	if strings.HasPrefix(value, "a/") || strings.HasPrefix(value, "b/") {
		return value[2:]
	}
	return value
}

func parseHunkHeader(line string) (DiffHunk, bool) {
	if !strings.HasPrefix(line, "@@ -") {
		return DiffHunk{}, false
	}
	parts := strings.SplitN(line[4:], " @@", 2)
	if len(parts) != 2 {
		return DiffHunk{}, false
	}
	ranges := strings.Split(parts[0], " +")
	if len(ranges) != 2 {
		return DiffHunk{}, false
	}
	oldStart, oldLines, ok := parseHunkRange(ranges[0])
	if !ok {
		return DiffHunk{}, false
	}
	newStart, newLines, ok := parseHunkRange(ranges[1])
	if !ok {
		return DiffHunk{}, false
	}
	heading := ""
	if len(parts[1]) > 0 {
		heading = strings.TrimSpace(strings.TrimPrefix(parts[1], " "))
	}
	return DiffHunk{OldStart: oldStart, OldLines: oldLines, NewStart: newStart, NewLines: newLines, Heading: heading}, true
}

func parseHunkRange(value string) (start, lines int, ok bool) {
	values := strings.SplitN(value, ",", 2)
	parsedStart, err := strconv.Atoi(values[0])
	if err != nil {
		return 0, 0, false
	}
	parsedLines := 1
	if len(values) == 2 {
		parsedLines, err = strconv.Atoi(values[1])
		if err != nil {
			return 0, 0, false
		}
	}
	return parsedStart, parsedLines, true
}

func parseNumstat(output []byte) map[string]numstat {
	stats := make(map[string]numstat)
	chunks := strings.Split(string(output), "\x00")
	for index := 0; index < len(chunks); index++ {
		chunk := chunks[index]
		if chunk == "" {
			continue
		}
		parts := strings.SplitN(chunk, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		filePath := parts[2]
		if filePath == "" {
			index++
			originalPath := ""
			if index < len(chunks) {
				originalPath = chunks[index]
			}
			index++
			if index < len(chunks) {
				filePath = chunks[index]
			}
			if filePath == "" {
				filePath = originalPath
			}
		}
		if filePath == "" {
			continue
		}
		additions, additionBinary := parseNumstatValue(parts[0])
		deletions, deletionBinary := parseNumstatValue(parts[1])
		current := stats[filePath]
		current.Additions += additions
		current.Deletions += deletions
		current.Binary = current.Binary || additionBinary || deletionBinary
		stats[filePath] = current
	}
	return stats
}

func parseNumstatValue(value string) (int, bool) {
	if value == "-" {
		return 0, true
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return parsed, false
}

func mergeNumstats(all ...map[string]numstat) map[string]numstat {
	merged := make(map[string]numstat)
	for _, stats := range all {
		for filePath, stat := range stats {
			current := merged[filePath]
			current.Additions += stat.Additions
			current.Deletions += stat.Deletions
			current.Binary = current.Binary || stat.Binary
			merged[filePath] = current
		}
	}
	return merged
}

func captureManifestStates(ctx context.Context, runner GitRunner, fileSystem SnapshotFileSystem, root string, scope Scope, files []FileChange) (map[string]*ManifestState, error) {
	manifests := make([]FileChange, 0)
	for _, file := range files {
		if path.Base(file.Path) == "package.json" {
			manifests = append(manifests, file)
		}
	}
	states := make(map[string]*ManifestState, len(manifests))
	if len(manifests) == 0 {
		return states, nil
	}
	inputRunner, ok := runner.(GitInputRunner)
	if !ok {
		return nil, errors.New("Git snapshot runner does not support cat-file input")
	}
	beforeSpecs := make([]string, 0, len(manifests))
	afterSpecs := make([]string, 0, len(manifests))
	for _, file := range manifests {
		beforePath := file.Path
		if file.OriginalPath != "" {
			beforePath = file.OriginalPath
		}
		beforeSpecs = append(beforeSpecs, "HEAD:"+beforePath)
		if scope == ScopeStaged {
			afterSpecs = append(afterSpecs, ":"+file.Path)
		}
	}
	specs := append(append([]string(nil), beforeSpecs...), afterSpecs...)
	blobs, err := readGitBlobs(ctx, inputRunner, root, specs)
	if err != nil {
		return nil, err
	}
	for _, file := range manifests {
		beforePath := file.Path
		if file.OriginalPath != "" {
			beforePath = file.OriginalPath
		}
		before := blobs["HEAD:"+beforePath]
		var after *string
		if scope == ScopeStaged {
			after = blobs[":"+file.Path]
		} else if contents, err := fileSystem.ReadFile(filepath.Join(root, file.Path)); err == nil {
			value := string(contents)
			after = &value
		}
		states[file.Path] = &ManifestState{Before: before, After: after}
	}
	return states, nil
}

func readGitBlobs(ctx context.Context, runner GitInputRunner, root string, specs []string) (map[string]*string, error) {
	result := make(map[string]*string, len(specs))
	if len(specs) == 0 {
		return result, nil
	}
	output, err := runner.RunInput(ctx, []string{"-C", root, "cat-file", "--batch"}, []byte(strings.Join(specs, "\n")+"\n"))
	if err != nil {
		return nil, err
	}
	if output.ExitCode != 0 {
		return nil, gitOutputError(output, "git cat-file failed")
	}
	offset := 0
	for _, spec := range specs {
		headerEnd := bytesIndex(output.Stdout, '\n', offset)
		if headerEnd < 0 {
			return nil, fmt.Errorf("git cat-file returned an incomplete response for %s", spec)
		}
		header := string(output.Stdout[offset:headerEnd])
		offset = headerEnd + 1
		if strings.HasSuffix(header, " missing") {
			result[spec] = nil
			continue
		}
		parts := strings.Split(header, " ")
		if len(parts) != 3 || parts[1] != "blob" {
			return nil, fmt.Errorf("git cat-file returned an unexpected response for %s: %s", spec, header)
		}
		length, err := strconv.Atoi(parts[2])
		if err != nil || length < 0 || offset+length >= len(output.Stdout) || output.Stdout[offset+length] != '\n' {
			return nil, fmt.Errorf("git cat-file returned an incomplete blob for %s", spec)
		}
		value := string(output.Stdout[offset : offset+length])
		result[spec] = &value
		offset += length + 1
	}
	return result, nil
}

func bytesIndex(value []byte, target byte, start int) int {
	for index := start; index < len(value); index++ {
		if value[index] == target {
			return index
		}
	}
	return -1
}

func snapshotFileID(file FileChange) string {
	return sha256Text(strings.Join([]string{file.Path, file.OriginalPath, string([]byte{file.IndexStatus}), string([]byte{file.WorktreeStatus})}, "\x00"))
}

func snapshotID(scope Scope, files []FileChange, cachedPatch, worktreePatch string, untracked map[string]untrackedContent) string {
	status := make([]string, 0, len(files))
	for _, file := range files {
		status = append(status, strings.Join([]string{string([]byte{file.IndexStatus}), string([]byte{file.WorktreeStatus}), file.Path, file.OriginalPath}, "\x00"))
	}
	paths := make([]string, 0, len(untracked))
	for filePath := range untracked {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	hashes := make([]string, 0, len(paths))
	for _, filePath := range paths {
		hashes = append(hashes, filePath+"\x00"+untracked[filePath].Hash)
	}
	return sha256Text(strings.Join([]string{string(scope), strings.Join(status, "\x00"), cachedPatch, worktreePatch, strings.Join(hashes, "\x00")}, "\x00"))
}

func sha256Text(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func snapshotTotals(files []SnapshotFile) ChangeStats {
	totals := ChangeStats{}
	for _, file := range files {
		totals.Additions += file.Stats.Additions
		totals.Deletions += file.Stats.Deletions
	}
	return totals
}
