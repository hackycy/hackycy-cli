package diff

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

const comparisonChunkSize = 64 * 1024

var ErrRefreshActive = errors.New("a comparison refresh is already active")

type Workspace struct {
	baseline   comparisonRoot
	target     comparisonRoot
	exclusions exclusionMatcher
	gitIgnore  bool

	mu            sync.RWMutex
	published     *Snapshot
	state         WorkspaceState
	active        *RefreshRun
	listeners     map[uint64]func(WorkspaceState)
	nextListener  uint64
	textDiffSlots chan struct{}
}

type comparisonRoot struct {
	label string
	path  string
	info  fs.FileInfo
}

type RefreshRun struct {
	cancel  context.CancelFunc
	done    chan struct{}
	attempt *diffRefreshAttempt

	mu       sync.RWMutex
	snapshot *Snapshot
	err      error
}

type Snapshot struct {
	summary SnapshotSummary

	entries         []snapshotEntry
	tree            map[string]snapshotTreeChildren
	directorySearch []TreeNode
	textDiffSlots   chan struct{}
}

type snapshotTreeChildren struct {
	directories []TreeNode
	entryIDs    []int
}

func (snapshot *Snapshot) Summary() SnapshotSummary {
	return snapshot.summary
}

func (snapshot *Snapshot) List(query EntryQuery) (EntryPage, error) {
	limit := query.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	startIndex, err := decodeEntryCursor(query.Cursor)
	if err != nil {
		return EntryPage{}, err
	}
	if query.Anchor != 0 {
		anchor := snapshot.entry(query.Anchor)
		if anchor == nil || !entryMatches(anchor.Entry, query) {
			return EntryPage{}, errors.New("Entry anchor does not match the current filters")
		}
		startIndex = anchor.ID - 1
	}

	page := EntryPage{Entries: make([]Entry, 0, limit)}
	for index := startIndex; index < len(snapshot.entries); index++ {
		entry := snapshot.entries[index]
		if !entryMatches(entry.Entry, query) {
			continue
		}
		if len(page.Entries) == limit {
			page.NextCursor = encodeEntryCursor(strconv.Itoa(index))
			break
		}
		page.Entries = append(page.Entries, cloneEntry(entry.Entry))
	}
	return page, nil
}

func (snapshot *Snapshot) Tree(path string) TreePage {
	children := snapshot.tree[path]
	result := TreePage{Children: make([]TreeNode, 0, len(children.directories)+len(children.entryIDs))}
	for _, directory := range children.directories {
		result.Children = append(result.Children, cloneTreeNode(directory))
	}
	for _, id := range children.entryIDs {
		if entry := snapshot.entry(id); entry != nil {
			result.Children = append(result.Children, entryTreeNode(entry.Entry))
		}
	}
	return result
}

func (snapshot *Snapshot) Search(query string, statuses []ComparisonStatus, limit int) SearchPage {
	if limit == 0 {
		limit = 200
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	pathSearch := strings.ToLower(strings.TrimSpace(query))
	results := make([]TreeNode, 0)
	for _, directory := range snapshot.directorySearch {
		if directoryMatchesStatuses(directory, statuses) && (pathSearch == "" || strings.Contains(strings.ToLower(directory.Path), pathSearch)) {
			results = append(results, cloneTreeNode(directory))
		}
	}
	for _, entry := range snapshot.entries {
		if entryMatchesStatuses(entry.Status, statuses) && (pathSearch == "" || strings.Contains(strings.ToLower(entry.Path), pathSearch)) {
			results = append(results, entryTreeNode(entry.Entry))
		}
	}
	sort.Slice(results, func(left, right int) bool {
		comparison := compareComparisonPath(results[left].Path, results[right].Path)
		if comparison != 0 {
			return comparison < 0
		}
		return results[left].Kind == TreeKindDirectory && results[right].Kind != TreeKindDirectory
	})
	page := SearchPage{Truncated: len(results) > limit}
	if len(results) > limit {
		results = results[:limit]
	}
	page.Results = results
	return page
}

func (snapshot *Snapshot) entry(id int) *snapshotEntry {
	if id < 1 || id > len(snapshot.entries) {
		return nil
	}
	entry := &snapshot.entries[id-1]
	if entry.ID != id {
		return nil
	}
	return entry
}

func entryMatches(entry Entry, query EntryQuery) bool {
	if len(query.Statuses) > 0 {
		found := false
		for _, status := range query.Statuses {
			if entry.Status == status {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	} else if !query.IncludeUnchanged && entry.Status == StatusUnchanged {
		return false
	}
	if len(query.Kinds) > 0 {
		found := false
		for _, kind := range query.Kinds {
			if entry.Status == StatusIssue {
				found = kind == ItemKindIssue
			} else if kind != ItemKindIssue && ((entry.Baseline != nil && entry.Baseline.Kind == EntryKind(kind)) || (entry.Target != nil && entry.Target.Kind == EntryKind(kind))) {
				found = true
			}
			if found {
				break
			}
		}
		if !found {
			return false
		}
	}
	path := strings.ToLower(strings.TrimSpace(query.Path))
	return path == "" || strings.Contains(strings.ToLower(entry.Path), path)
}

func cloneEntry(entry Entry) Entry {
	copy := entry
	if entry.Baseline != nil {
		state := *entry.Baseline
		copy.Baseline = &state
	}
	if entry.Target != nil {
		state := *entry.Target
		copy.Target = &state
	}
	return copy
}

func cloneTreeNode(node TreeNode) TreeNode {
	return node
}

func entryTreeNode(entry Entry) TreeNode {
	if entry.Status == StatusIssue {
		return TreeNode{Kind: TreeKindIssue, Name: comparisonBaseName(entry.Path), Path: entry.Path, ID: entry.ID, Status: entry.Status, Message: entry.Message}
	}
	state := entry.Target
	if state == nil {
		state = entry.Baseline
	}
	kind := TreeKindFile
	if state != nil && state.Kind == EntryKindSymlink {
		kind = TreeKindSymlink
	}
	return TreeNode{Kind: kind, Name: comparisonBaseName(entry.Path), Path: entry.Path, ID: entry.ID, Status: entry.Status}
}

func entryMatchesStatuses(status ComparisonStatus, statuses []ComparisonStatus) bool {
	if statuses == nil {
		return true
	}
	for _, expected := range statuses {
		if status == expected {
			return true
		}
	}
	return false
}

func directoryMatchesStatuses(directory TreeNode, statuses []ComparisonStatus) bool {
	if statuses == nil {
		return true
	}
	for _, status := range statuses {
		switch status {
		case StatusAdded:
			if directory.Counts.Added > 0 {
				return true
			}
		case StatusDeleted:
			if directory.Counts.Deleted > 0 {
				return true
			}
		case StatusModified:
			if directory.Counts.Modified > 0 {
				return true
			}
		case StatusUnchanged:
			if directory.Counts.Unchanged > 0 {
				return true
			}
		case StatusIssue:
			if directory.Issues > 0 {
				return true
			}
		}
	}
	return false
}

func encodeEntryCursor(index string) string {
	return base64.RawURLEncoding.EncodeToString([]byte("entry-index:" + index))
}

func decodeEntryCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		decoded, err = base64.URLEncoding.DecodeString(cursor)
		if err != nil {
			return 0, errors.New("Invalid entry cursor")
		}
	}
	value := strings.TrimPrefix(string(decoded), "entry-index:")
	if value == string(decoded) || value == "" {
		return 0, errors.New("Invalid entry cursor")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, errors.New("Invalid entry cursor")
		}
	}
	index, err := strconv.ParseUint(value, 10, 0)
	if err != nil || index > uint64(^uint(0)>>1) {
		return int(^uint(0) >> 1), nil
	}
	return int(index), nil
}

type snapshotEntry struct {
	Entry
	baseline *sourceEntry
	target   *sourceEntry
}

type sourceEntry struct {
	state EntryState
	info  fs.FileInfo
}

type discovery struct {
	entries map[string]*sourceEntry
	issues  map[string]string
}

type targetIgnoreDiscovery struct {
	matcher            targetIgnoreMatcher
	issues             map[string]string
	blockedDirectories map[string]struct{}
}

func NewWorkspace(options WorkspaceOptions) (*Workspace, error) {
	baseline, err := resolveComparisonRoot("Baseline Directory", options.BaselineDirectory)
	if err != nil {
		return nil, err
	}
	target, err := resolveComparisonRoot("Target Directory", options.TargetDirectory)
	if err != nil {
		return nil, err
	}
	if baseline.path == target.path || os.SameFile(baseline.info, target.info) {
		return nil, errors.New("Baseline Directory and Target Directory must be different")
	}

	return &Workspace{
		baseline:      baseline,
		target:        target,
		exclusions:    newExclusionMatcher(options.Exclusions),
		gitIgnore:     !options.NoGitIgnore,
		state:         WorkspaceState{Phase: PhaseIdle},
		listeners:     make(map[uint64]func(WorkspaceState)),
		textDiffSlots: make(chan struct{}, maxConcurrentTextDiffs),
	}, nil
}

func (workspace *Workspace) State() WorkspaceState {
	workspace.mu.RLock()
	defer workspace.mu.RUnlock()
	return cloneWorkspaceState(workspace.state)
}

func (workspace *Workspace) Snapshot(ids ...string) *Snapshot {
	workspace.mu.RLock()
	defer workspace.mu.RUnlock()
	if len(ids) > 0 && (workspace.published == nil || workspace.published.summary.ID != ids[0]) {
		return nil
	}
	return workspace.published
}

func (workspace *Workspace) publishedSnapshotID() string {
	workspace.mu.RLock()
	defer workspace.mu.RUnlock()
	if workspace.published == nil {
		return ""
	}
	return workspace.published.summary.ID
}

func (workspace *Workspace) Subscribe(listener func(WorkspaceState)) func() {
	workspace.mu.Lock()
	id := workspace.nextListener
	workspace.nextListener++
	workspace.listeners[id] = listener
	state := cloneWorkspaceState(workspace.state)
	workspace.mu.Unlock()
	listener(state)
	return func() {
		workspace.mu.Lock()
		delete(workspace.listeners, id)
		workspace.mu.Unlock()
	}
}

func (workspace *Workspace) StartRefresh(parent context.Context) (*RefreshRun, error) {
	return workspace.startRefresh(parent, nil)
}

func (workspace *Workspace) startRefresh(parent context.Context, attempt *diffRefreshAttempt) (*RefreshRun, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	run := &RefreshRun{cancel: cancel, done: make(chan struct{}), attempt: attempt}

	workspace.mu.Lock()
	if workspace.active != nil {
		workspace.mu.Unlock()
		cancel()
		return nil, ErrRefreshActive
	}
	workspace.active = run
	listeners, state := workspace.setStateLocked(WorkspaceState{
		Phase:    PhaseDiscovering,
		Progress: &WorkspaceProgress{},
	})
	workspace.mu.Unlock()
	workspace.notify(listeners, state)
	if run.attempt != nil && run.attempt.lifecycle != nil {
		run.attempt.lifecycle.state(run, state)
	}

	go workspace.runRefresh(ctx, run)
	return run, nil
}

func (run *RefreshRun) Cancel() {
	run.cancel()
}

func (run *RefreshRun) snapshotValue() *Snapshot {
	if run == nil {
		return nil
	}
	run.mu.RLock()
	defer run.mu.RUnlock()
	return run.snapshot
}

func (run *RefreshRun) errorValue() error {
	if run == nil {
		return nil
	}
	run.mu.RLock()
	defer run.mu.RUnlock()
	return run.err
}

func (run *RefreshRun) Wait(ctx context.Context) (*Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-run.done:
		run.mu.RLock()
		defer run.mu.RUnlock()
		return run.snapshot, run.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (workspace *Workspace) runRefresh(ctx context.Context, run *RefreshRun) {
	snapshot, err := workspace.buildSnapshot(ctx, run)
	if err == nil && ctx.Err() != nil {
		snapshot = nil
		err = ctx.Err()
	}

	workspace.mu.Lock()
	if workspace.active == run {
		workspace.active = nil
	}
	var state WorkspaceState
	if err == nil {
		workspace.published = snapshot
		state = WorkspaceState{Phase: PhaseReady, SnapshotID: snapshot.summary.ID}
	} else if errors.Is(err, context.Canceled) {
		state = WorkspaceState{Phase: PhaseCanceled}
		if workspace.published != nil {
			state.SnapshotID = workspace.published.summary.ID
		}
	} else {
		state = WorkspaceState{Phase: PhaseError, Error: err.Error()}
		if workspace.published != nil {
			state.SnapshotID = workspace.published.summary.ID
		}
	}
	listeners, state := workspace.setStateLocked(state)
	workspace.mu.Unlock()
	workspace.notify(listeners, state)

	run.mu.Lock()
	run.snapshot = snapshot
	run.err = err
	run.mu.Unlock()
	if run.attempt != nil && run.attempt.lifecycle != nil {
		run.attempt.lifecycle.state(run, state)
	}
	close(run.done)
}

func (workspace *Workspace) buildSnapshot(ctx context.Context, run *RefreshRun) (*Snapshot, error) {
	if err := workspace.assertFixedRoots(); err != nil {
		return nil, err
	}
	ignoreDiscovery := emptyTargetIgnoreDiscovery()
	if workspace.gitIgnore {
		ignoreDiscovery = collectTargetIgnoreRules(ctx, workspace.target, workspace.exclusions)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	workspace.publishStateForRun(run, WorkspaceState{Phase: PhaseDiscovering, Progress: &WorkspaceProgress{Issues: len(ignoreDiscovery.issues)}})
	baselineDiscovery, targetDiscovery := workspace.discover(ctx, ignoreDiscovery, run)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := workspace.assertFixedRoots(); err != nil {
		return nil, err
	}

	issues := make(map[string]string, len(ignoreDiscovery.issues)+len(baselineDiscovery.issues)+len(targetDiscovery.issues))
	mergeDiscoveryIssues(issues, ignoreDiscovery.issues)
	mergeDiscoveryIssues(issues, baselineDiscovery.issues)
	mergeDiscoveryIssues(issues, targetDiscovery.issues)
	paths := unionComparisonPaths(baselineDiscovery.entries, targetDiscovery.entries, issues)
	totalEntries := len(paths)
	progress := WorkspaceProgress{TotalEntries: &totalEntries, Issues: len(issues)}
	if discovered := workspace.State().Progress; discovered != nil {
		progress.DiscoveredEntries = discovered.DiscoveredEntries
	}
	for _, comparisonPath := range paths {
		baseline := baselineDiscovery.entries[comparisonPath]
		target := targetDiscovery.entries[comparisonPath]
		if baseline != nil && target != nil && baseline.state.Kind == EntryKindFile && target.state.Kind == EntryKindFile && baseline.state.Size == target.state.Size {
			progress.TotalBytes = int64Pointer(valueOrZero(progress.TotalBytes) + baseline.state.Size)
		}
	}
	workspace.publishStateForRun(run, WorkspaceState{Phase: PhaseComparing, Progress: &progress})

	entries := make([]snapshotEntry, 0, len(paths))
	for index, comparisonPath := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry := snapshotEntry{Entry: Entry{ID: index + 1, Path: comparisonPath}}
		if message, ok := issues[comparisonPath]; ok {
			entry.Status = StatusIssue
			entry.Message = message
			entries = append(entries, entry)
			progress.ComparedEntries++
			workspace.publishStateForRun(run, WorkspaceState{Phase: PhaseComparing, Progress: &progress})
			continue
		}

		baseline := baselineDiscovery.entries[comparisonPath]
		target := targetDiscovery.entries[comparisonPath]
		switch {
		case baseline == nil:
			entry.Status = StatusAdded
		case target == nil:
			entry.Status = StatusDeleted
		default:
			comparison, err := compareWithRetry(ctx, baseline, target, comparisonPath, workspace.baseline.path, workspace.target.path)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return nil, err
				}
				entry.Status = StatusIssue
				entry.Message = comparisonIssueMessage(err)
				entries = append(entries, entry)
				progress.ComparedEntries++
				progress.Issues++
				workspace.publishStateForRun(run, WorkspaceState{Phase: PhaseComparing, Progress: &progress})
				continue
			}
			baseline = comparison.baseline
			target = comparison.target
			if comparison.equal {
				entry.Status = StatusUnchanged
			} else {
				entry.Status = StatusModified
			}
			progress.ComparedBytes += comparison.comparedBytes
		}
		entry.Baseline = entryState(baseline)
		entry.Target = entryState(target)
		entry.baseline = baseline
		entry.target = target
		entries = append(entries, entry)
		progress.ComparedEntries++
		workspace.publishStateForRun(run, WorkspaceState{Phase: PhaseComparing, Progress: &progress})
	}

	if err := workspace.assertFixedRoots(); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Status == StatusIssue {
			continue
		}
		if entry.baseline != nil {
			if err := assertSourceStable(workspace.baseline.path, entry.Path, entry.baseline); err != nil {
				return nil, err
			}
		}
		if entry.target != nil {
			if err := assertSourceStable(workspace.target.path, entry.Path, entry.target); err != nil {
				return nil, err
			}
		}
	}
	if err := workspace.assertFixedRoots(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	counts := StatusCounts{}
	issueCount := 0
	for _, entry := range entries {
		switch entry.Status {
		case StatusAdded:
			counts.Added++
		case StatusDeleted:
			counts.Deleted++
		case StatusModified:
			counts.Modified++
		case StatusUnchanged:
			counts.Unchanged++
		case StatusIssue:
			issueCount++
		}
	}
	progress.Issues = issueCount
	progress.TotalBytes = int64Pointer(progress.ComparedBytes)
	workspace.publishStateForRun(run, WorkspaceState{Phase: PhasePublishing, Progress: &progress})
	tree, directorySearch := buildSnapshotTree(entries)
	return &Snapshot{
		summary: SnapshotSummary{
			ID:                newSnapshotID(),
			BaselineDirectory: workspace.baseline.path,
			TargetDirectory:   workspace.target.path,
			CreatedAt:         time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
			Counts:            counts,
			Issues:            issueCount,
		},
		entries:         entries,
		tree:            tree,
		directorySearch: directorySearch,
		textDiffSlots:   workspace.textDiffSlots,
	}, nil
}

func (workspace *Workspace) discover(ctx context.Context, ignoreDiscovery targetIgnoreDiscovery, run *RefreshRun) (discovery, discovery) {
	progress := workspace.State().Progress
	baseline := discoverRoot(ctx, workspace.baseline, workspace.exclusions, ignoreDiscovery, func(issue bool) {
		if progress == nil {
			progress = &WorkspaceProgress{}
		}
		progress.DiscoveredEntries++
		if issue {
			progress.Issues++
		}
		workspace.publishStateForRun(run, WorkspaceState{Phase: PhaseDiscovering, Progress: progress})
	})
	target := discoverRoot(ctx, workspace.target, workspace.exclusions, ignoreDiscovery, func(issue bool) {
		if progress == nil {
			progress = &WorkspaceProgress{}
		}
		progress.DiscoveredEntries++
		if issue {
			progress.Issues++
		}
		workspace.publishStateForRun(run, WorkspaceState{Phase: PhaseDiscovering, Progress: progress})
	})
	return baseline, target
}

func discoverRoot(ctx context.Context, root comparisonRoot, exclusions exclusionMatcher, ignoreDiscovery targetIgnoreDiscovery, discovered func(issue bool)) discovery {
	result := discovery{entries: make(map[string]*sourceEntry), issues: make(map[string]string)}
	directories := []string{""}
	for len(directories) > 0 {
		next := make([]string, 0)
		for _, relativeDirectory := range directories {
			if ctx.Err() != nil {
				return result
			}
			absoluteDirectory := absoluteComparisonPath(root.path, relativeDirectory)
			children, err := os.ReadDir(absoluteDirectory)
			if err != nil {
				comparisonPath := relativeDirectory
				if comparisonPath == "" {
					comparisonPath = "."
				}
				recordIssue(result.issues, comparisonPath, fmt.Sprintf("%s could not be read (%s)", root.label, errorCode(err)))
				discovered(true)
				continue
			}
			for _, child := range children {
				if ctx.Err() != nil {
					return result
				}
				comparisonPath, pathErr := comparisonPathForChild(relativeDirectory, child.Name())
				if pathErr != nil {
					recordIssue(result.issues, relativeDirectoryOrRoot(relativeDirectory), fmt.Sprintf("%s contains an invalid UTF-8 filename", root.label))
					discovered(true)
					continue
				}
				absolutePath := absoluteComparisonPath(root.path, comparisonPath)
				info, err := os.Lstat(absolutePath)
				if err != nil {
					recordIssue(result.issues, comparisonPath, fmt.Sprintf("%s entry could not be inspected (%s)", root.label, errorCode(err)))
					discovered(true)
					continue
				}
				directory := info.IsDir()
				if ignoreDiscovery.blocks(comparisonPath) || hardExcluded(comparisonPath, directory) || exclusions.excludes(comparisonPath, directory) || ignoreDiscovery.matcher.ignored(comparisonPath, directory) {
					continue
				}
				switch {
				case directory:
					next = append(next, comparisonPath)
				case info.Mode().IsRegular():
					result.entries[comparisonPath] = &sourceEntry{state: EntryState{Kind: EntryKindFile, Size: info.Size()}, info: info}
					discovered(false)
				case info.Mode()&os.ModeSymlink != 0:
					linkTarget, err := os.Readlink(absolutePath)
					if err != nil {
						recordIssue(result.issues, comparisonPath, fmt.Sprintf("%s symbolic link could not be read (%s)", root.label, errorCode(err)))
						discovered(true)
						continue
					}
					result.entries[comparisonPath] = &sourceEntry{state: EntryState{Kind: EntryKindSymlink, Size: info.Size(), LinkTarget: linkTarget}, info: info}
					discovered(false)
				default:
					recordIssue(result.issues, comparisonPath, fmt.Sprintf("%s entry has an unsupported filesystem kind", root.label))
					discovered(true)
				}
			}
		}
		sort.Slice(next, func(left, right int) bool { return compareComparisonPath(next[left], next[right]) < 0 })
		directories = next
	}
	return result
}

func emptyTargetIgnoreDiscovery() targetIgnoreDiscovery {
	return targetIgnoreDiscovery{
		matcher:            newTargetIgnoreMatcher(nil),
		issues:             make(map[string]string),
		blockedDirectories: make(map[string]struct{}),
	}
}

func collectTargetIgnoreRules(ctx context.Context, root comparisonRoot, exclusions exclusionMatcher) targetIgnoreDiscovery {
	result := emptyTargetIgnoreDiscovery()
	directories := []string{""}
	for len(directories) > 0 {
		next := make([]string, 0)
		for _, relativeDirectory := range directories {
			if ctx.Err() != nil {
				return result
			}
			absoluteDirectory := absoluteComparisonPath(root.path, relativeDirectory)
			ignorePath := filepath.Join(absoluteDirectory, ".gitignore")
			info, err := os.Stat(ignorePath)
			switch {
			case err == nil && !info.IsDir():
				contents, readErr := os.ReadFile(ignorePath)
				if readErr != nil {
					comparisonPath := joinComparisonPath(relativeDirectory, ".gitignore")
					recordIssue(result.issues, comparisonPath, fmt.Sprintf("Target Directory ignore rules could not be read (%s)", errorCode(readErr)))
					result.blockedDirectories[relativeDirectory] = struct{}{}
					continue
				}
				result.matcher.add(relativeDirectory, string(contents))
			case err != nil && !errors.Is(err, fs.ErrNotExist):
				comparisonPath := joinComparisonPath(relativeDirectory, ".gitignore")
				recordIssue(result.issues, comparisonPath, fmt.Sprintf("Target Directory ignore rules could not be read (%s)", errorCode(err)))
				result.blockedDirectories[relativeDirectory] = struct{}{}
				continue
			}

			children, err := os.ReadDir(absoluteDirectory)
			if err != nil {
				comparisonPath := relativeDirectoryOrRoot(relativeDirectory)
				recordIssue(result.issues, comparisonPath, fmt.Sprintf("Target Directory could not be read (%s)", errorCode(err)))
				result.blockedDirectories[relativeDirectory] = struct{}{}
				continue
			}
			for _, child := range children {
				if ctx.Err() != nil {
					return result
				}
				comparisonPath, pathErr := comparisonPathForChild(relativeDirectory, child.Name())
				if pathErr != nil {
					continue
				}
				info, err := os.Lstat(absoluteComparisonPath(root.path, comparisonPath))
				if err != nil || !info.IsDir() {
					continue
				}
				if hardExcluded(comparisonPath, true) || exclusions.excludes(comparisonPath, true) || result.matcher.ignored(comparisonPath, true) {
					continue
				}
				next = append(next, comparisonPath)
			}
		}
		sort.Slice(next, func(left, right int) bool { return compareComparisonPath(next[left], next[right]) < 0 })
		directories = next
	}
	return result
}

func (discovery targetIgnoreDiscovery) blocks(comparisonPath string) bool {
	if _, blocked := discovery.blockedDirectories[""]; blocked {
		return true
	}
	for current := comparisonPath; current != ""; current = parentComparisonPath(current) {
		if _, blocked := discovery.blockedDirectories[current]; blocked {
			return true
		}
	}
	return false
}

type sourceComparison struct {
	baseline      *sourceEntry
	target        *sourceEntry
	equal         bool
	comparedBytes int64
}

func compareWithRetry(ctx context.Context, baseline, target *sourceEntry, comparisonPath, baselineRoot, targetRoot string) (sourceComparison, error) {
	for attempt := 0; attempt < 2; attempt++ {
		comparison, err := compareSources(ctx, baseline, target, comparisonPath, baselineRoot, targetRoot)
		if err == nil {
			return comparison, nil
		}
		if errors.Is(err, context.Canceled) {
			return sourceComparison{}, err
		}
		if attempt == 1 {
			return sourceComparison{}, err
		}
		baseline, err = refreshSource(baselineRoot, comparisonPath)
		if err != nil {
			return sourceComparison{}, err
		}
		target, err = refreshSource(targetRoot, comparisonPath)
		if err != nil {
			return sourceComparison{}, err
		}
	}
	return sourceComparison{}, errors.New("Comparison Entry could not be compared")
}

func compareSources(ctx context.Context, baseline, target *sourceEntry, comparisonPath, baselineRoot, targetRoot string) (sourceComparison, error) {
	comparison := sourceComparison{baseline: baseline, target: target}
	if baseline.state.Kind != target.state.Kind || baseline.state.Size != target.state.Size {
		return comparison, nil
	}
	if baseline.state.Kind == EntryKindSymlink {
		comparison.equal = baseline.state.LinkTarget == target.state.LinkTarget
		return comparison, nil
	}

	baselinePath := absoluteComparisonPath(baselineRoot, comparisonPath)
	targetPath := absoluteComparisonPath(targetRoot, comparisonPath)
	baselineFile, err := openComparisonFile(baselinePath)
	if err != nil {
		return comparison, err
	}
	defer baselineFile.Close()
	targetFile, err := openComparisonFile(targetPath)
	if err != nil {
		return comparison, err
	}
	defer targetFile.Close()
	baselineInfo, err := baselineFile.Stat()
	if err != nil {
		return comparison, err
	}
	targetInfo, err := targetFile.Stat()
	if err != nil {
		return comparison, err
	}
	if !sameSourceInfo(baseline.info, baselineInfo) || !sameSourceInfo(target.info, targetInfo) {
		return comparison, errors.New("Comparison Entry changed while the snapshot was being built")
	}

	baselineBuffer := make([]byte, comparisonChunkSize)
	targetBuffer := make([]byte, comparisonChunkSize)
	comparison.equal = true
	for {
		if err := ctx.Err(); err != nil {
			return comparison, err
		}
		baselineRead, baselineError := baselineFile.Read(baselineBuffer)
		targetRead, targetError := targetFile.Read(targetBuffer)
		comparison.comparedBytes += int64(max(baselineRead, targetRead))
		if baselineRead != targetRead || !equalBytes(baselineBuffer[:baselineRead], targetBuffer[:targetRead]) {
			comparison.equal = false
			break
		}
		if baselineError == io.EOF && targetError == io.EOF {
			break
		}
		if baselineError != nil && baselineError != io.EOF {
			return comparison, baselineError
		}
		if targetError != nil && targetError != io.EOF {
			return comparison, targetError
		}
	}
	baselineInfo, err = baselineFile.Stat()
	if err != nil {
		return comparison, err
	}
	targetInfo, err = targetFile.Stat()
	if err != nil {
		return comparison, err
	}
	if !sameSourceInfo(baseline.info, baselineInfo) || !sameSourceInfo(target.info, targetInfo) {
		return comparison, errors.New("Comparison Entry changed while the snapshot was being built")
	}
	return comparison, nil
}

func refreshSource(root, comparisonPath string) (*sourceEntry, error) {
	info, err := os.Lstat(absoluteComparisonPath(root, comparisonPath))
	if err != nil {
		return nil, err
	}
	switch {
	case info.Mode().IsRegular():
		return &sourceEntry{state: EntryState{Kind: EntryKindFile, Size: info.Size()}, info: info}, nil
	case info.Mode()&os.ModeSymlink != 0:
		linkTarget, err := os.Readlink(absoluteComparisonPath(root, comparisonPath))
		if err != nil {
			return nil, err
		}
		return &sourceEntry{state: EntryState{Kind: EntryKindSymlink, Size: info.Size(), LinkTarget: linkTarget}, info: info}, nil
	default:
		return nil, errors.New("Comparison Entry changed to an unsupported filesystem kind")
	}
}

func assertSourceStable(root, comparisonPath string, source *sourceEntry) error {
	current, err := refreshSource(root, comparisonPath)
	if err != nil || !sameSource(source, current) {
		return errors.New("Comparison Entry changed before snapshot publication")
	}
	return nil
}

func sameSource(left, right *sourceEntry) bool {
	return left.state == right.state && sameSourceInfo(left.info, right.info)
}

func sameSourceInfo(left, right fs.FileInfo) bool {
	return os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

func (workspace *Workspace) assertFixedRoots() error {
	if err := assertFixedRoot(workspace.baseline); err != nil {
		return err
	}
	return assertFixedRoot(workspace.target)
}

func assertFixedRoot(root comparisonRoot) error {
	info, err := os.Lstat(root.path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(root.info, info) {
		return fmt.Errorf("%s changed after the Comparison Workspace was created", root.label)
	}
	return nil
}

func resolveComparisonRoot(label, directory string) (comparisonRoot, error) {
	abs, err := filepath.Abs(directory)
	if err != nil {
		return comparisonRoot{}, fmt.Errorf("resolve %s: %w", label, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return comparisonRoot{}, fmt.Errorf("resolve %s: %w", label, err)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return comparisonRoot{}, fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.IsDir() {
		return comparisonRoot{}, fmt.Errorf("%s must be a directory", label)
	}
	return comparisonRoot{label: label, path: resolved, info: info}, nil
}

func (workspace *Workspace) publishState(state WorkspaceState) {
	workspace.publishStateForRun(nil, state)
}

func (workspace *Workspace) publishStateForRun(run *RefreshRun, state WorkspaceState) {
	workspace.mu.Lock()
	listeners, published := workspace.setStateLocked(state)
	workspace.mu.Unlock()
	workspace.notify(listeners, published)
	if run != nil && run.attempt != nil && run.attempt.lifecycle != nil {
		run.attempt.lifecycle.state(run, published)
	}
}

func (workspace *Workspace) setStateLocked(state WorkspaceState) ([]func(WorkspaceState), WorkspaceState) {
	workspace.state = cloneWorkspaceState(state)
	listeners := make([]func(WorkspaceState), 0, len(workspace.listeners))
	for _, listener := range workspace.listeners {
		listeners = append(listeners, listener)
	}
	return listeners, cloneWorkspaceState(workspace.state)
}

func (workspace *Workspace) notify(listeners []func(WorkspaceState), state WorkspaceState) {
	for _, listener := range listeners {
		listener(cloneWorkspaceState(state))
	}
}

func cloneWorkspaceState(state WorkspaceState) WorkspaceState {
	if state.Progress != nil {
		progress := *state.Progress
		if state.Progress.TotalEntries != nil {
			value := *state.Progress.TotalEntries
			progress.TotalEntries = &value
		}
		if state.Progress.TotalBytes != nil {
			value := *state.Progress.TotalBytes
			progress.TotalBytes = &value
		}
		state.Progress = &progress
	}
	return state
}

func entryState(source *sourceEntry) *EntryState {
	if source == nil {
		return nil
	}
	state := source.state
	return &state
}

func unionComparisonPaths(baseline, target map[string]*sourceEntry, issues map[string]string) []string {
	paths := make(map[string]struct{}, len(baseline)+len(target)+len(issues))
	for path := range baseline {
		paths[path] = struct{}{}
	}
	for path := range target {
		paths[path] = struct{}{}
	}
	for path := range issues {
		paths[path] = struct{}{}
	}
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Slice(result, func(left, right int) bool { return compareComparisonPath(result[left], result[right]) < 0 })
	return result
}

func buildSnapshotTree(entries []snapshotEntry) (map[string]snapshotTreeChildren, []TreeNode) {
	type mutableChildren struct {
		directories map[string]TreeNode
		entryIDs    []int
	}
	childrenByDirectory := make(map[string]*mutableChildren)
	children := func(directory string) *mutableChildren {
		if current := childrenByDirectory[directory]; current != nil {
			return current
		}
		current := &mutableChildren{directories: make(map[string]TreeNode)}
		childrenByDirectory[directory] = current
		return current
	}

	for _, entry := range entries {
		parts := strings.Split(entry.Path, "/")
		for index := 0; index < len(parts)-1; index++ {
			parentPath := strings.Join(parts[:index], "/")
			directoryPath := strings.Join(parts[:index+1], "/")
			parent := children(parentPath)
			node, exists := parent.directories[directoryPath]
			if !exists {
				node = TreeNode{Kind: TreeKindDirectory, Name: parts[index], Path: directoryPath}
			}
			if entry.Status == StatusIssue {
				node.Issues++
			} else {
				addTreeStatus(&node.Counts, entry.Status)
			}
			parent.directories[directoryPath] = node
		}
		parentPath := strings.Join(parts[:len(parts)-1], "/")
		children(parentPath).entryIDs = append(children(parentPath).entryIDs, entry.ID)
	}

	tree := make(map[string]snapshotTreeChildren, len(childrenByDirectory))
	directorySearch := make([]TreeNode, 0)
	for path, indexedChildren := range childrenByDirectory {
		directories := make([]TreeNode, 0, len(indexedChildren.directories))
		for _, directory := range indexedChildren.directories {
			directories = append(directories, directory)
			directorySearch = append(directorySearch, directory)
		}
		sort.Slice(directories, func(left, right int) bool {
			return compareComparisonPath(directories[left].Path, directories[right].Path) < 0
		})
		tree[path] = snapshotTreeChildren{directories: directories, entryIDs: append([]int(nil), indexedChildren.entryIDs...)}
	}
	return tree, directorySearch
}

func addTreeStatus(counts *StatusCounts, status ComparisonStatus) {
	switch status {
	case StatusAdded:
		counts.Added++
	case StatusDeleted:
		counts.Deleted++
	case StatusModified:
		counts.Modified++
	case StatusUnchanged:
		counts.Unchanged++
	}
}

func comparisonBaseName(comparisonPath string) string {
	if slash := strings.LastIndexByte(comparisonPath, '/'); slash >= 0 {
		return comparisonPath[slash+1:]
	}
	return comparisonPath
}

func compareComparisonPath(left, right string) int {
	leftUnits := utf16.Encode([]rune(left))
	rightUnits := utf16.Encode([]rune(right))
	for index := 0; index < len(leftUnits) && index < len(rightUnits); index++ {
		if leftUnits[index] < rightUnits[index] {
			return -1
		}
		if leftUnits[index] > rightUnits[index] {
			return 1
		}
	}
	switch {
	case len(leftUnits) < len(rightUnits):
		return -1
	case len(leftUnits) > len(rightUnits):
		return 1
	default:
		return 0
	}
}

func absoluteComparisonPath(root, comparisonPath string) string {
	if comparisonPath == "" {
		return root
	}
	return filepath.Join(root, filepath.FromSlash(comparisonPath))
}

func joinComparisonPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "/" + name
}

func relativeDirectoryOrRoot(relativeDirectory string) string {
	if relativeDirectory == "" {
		return "."
	}
	return relativeDirectory
}

func recordIssue(issues map[string]string, comparisonPath, message string) {
	if current, ok := issues[comparisonPath]; ok {
		issues[comparisonPath] = current + "; " + message
		return
	}
	issues[comparisonPath] = message
}

func mergeDiscoveryIssues(target, source map[string]string) {
	for path, message := range source {
		recordIssue(target, path, message)
	}
}

func errorCode(err error) string {
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		if errno, ok := pathError.Err.(interface{ Error() string }); ok {
			return errno.Error()
		}
	}
	return "UNKNOWN"
}

func comparisonIssueMessage(err error) string {
	if strings.HasPrefix(err.Error(), "Comparison Entry") {
		return err.Error()
	}
	return fmt.Sprintf("Comparison could not be completed (%s)", errorCode(err))
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func int64Pointer(value int64) *int64 {
	return &value
}

func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func newSnapshotID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic(fmt.Sprintf("generate snapshot ID: %v", err))
	}
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
}
