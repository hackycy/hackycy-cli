package diff

// ComparisonStatus is the directional result for one fixed Comparison Path.
type ComparisonStatus string

const (
	StatusAdded     ComparisonStatus = "added"
	StatusDeleted   ComparisonStatus = "deleted"
	StatusModified  ComparisonStatus = "modified"
	StatusUnchanged ComparisonStatus = "unchanged"
	StatusIssue     ComparisonStatus = "issue"
)

type EntryKind string

const (
	EntryKindFile    EntryKind = "file"
	EntryKindSymlink EntryKind = "symlink"
)

type EntryItemKind string

const (
	ItemKindFile    EntryItemKind = "file"
	ItemKindSymlink EntryItemKind = "symlink"
	ItemKindIssue   EntryItemKind = "issue"
)

type WorkspacePhase string

const (
	PhaseIdle        WorkspacePhase = "idle"
	PhaseDiscovering WorkspacePhase = "discovering"
	PhaseComparing   WorkspacePhase = "comparing"
	PhasePublishing  WorkspacePhase = "publishing"
	PhaseReady       WorkspacePhase = "ready"
	PhaseCanceled    WorkspacePhase = "canceled"
	PhaseError       WorkspacePhase = "error"
)

type StatusCounts struct {
	Added     int
	Deleted   int
	Modified  int
	Unchanged int
}

type WorkspaceProgress struct {
	DiscoveredEntries int
	ComparedEntries   int
	TotalEntries      *int
	ComparedBytes     int64
	TotalBytes        *int64
	Issues            int
}

type WorkspaceState struct {
	Phase      WorkspacePhase
	SnapshotID string
	Error      string
	Progress   *WorkspaceProgress
}

type WorkspaceOptions struct {
	BaselineDirectory string
	TargetDirectory   string
	NoGitIgnore       bool
	Exclusions        []string
}

type EntryState struct {
	Kind       EntryKind
	Size       int64
	LinkTarget string
}

type Entry struct {
	ID       int
	Path     string
	Status   ComparisonStatus
	Baseline *EntryState
	Target   *EntryState
	Message  string
}

// ComparisonSide identifies one fixed root in a Comparison Workspace.
type ComparisonSide string

const (
	SideBaseline ComparisonSide = "baseline"
	SideTarget   ComparisonSide = "target"
)

type EntryPresentation string

const (
	PresentationText      EntryPresentation = "text"
	PresentationImage     EntryPresentation = "image"
	PresentationBinary    EntryPresentation = "binary"
	PresentationSymlink   EntryPresentation = "symlink"
	PresentationOversized EntryPresentation = "oversized"
	PresentationStale     EntryPresentation = "stale"
	PresentationIssue     EntryPresentation = "issue"
)

type EntryDetail struct {
	Entry
	Presentation EntryPresentation
}

type TextEncoding string

const (
	EncodingUTF8    TextEncoding = "utf-8"
	EncodingUTF16LE TextEncoding = "utf-16le"
	EncodingUTF16BE TextEncoding = "utf-16be"
)

type ContentStatus string

const (
	ContentReady   ContentStatus = "ready"
	ContentGuarded ContentStatus = "guarded"
	ContentBlocked ContentStatus = "blocked"
	ContentBinary  ContentStatus = "binary"
	ContentMissing ContentStatus = "missing"
	ContentStale   ContentStatus = "stale"
)

type TextContent struct {
	Status    ContentStatus
	Text      string
	Encoding  TextEncoding
	Size      int64
	LineCount int
}

type TextDiffOptions struct {
	ContextLines *int
}

type TextDiffStatus string

const (
	TextDiffReady            TextDiffStatus = "ready"
	TextDiffNoTextualChanges TextDiffStatus = "no_textual_changes"
	TextDiffUnavailable      TextDiffStatus = "unavailable"
)

type TextDiffReason string

const (
	TextDiffEncodingOrBOMOnly TextDiffReason = "encoding_or_bom_only"
	TextDiffNonText           TextDiffReason = "non_text"
	TextDiffMixedEntryKinds   TextDiffReason = "mixed_entry_kinds"
	TextDiffSourceTooLarge    TextDiffReason = "source_too_large"
	TextDiffStale             TextDiffReason = "stale"
	TextDiffComplexityLimit   TextDiffReason = "complexity_limit"
	TextDiffOutputTooLarge    TextDiffReason = "output_too_large"
	TextDiffServerBusy        TextDiffReason = "server_busy"
)

type TextDiffResult struct {
	Status            TextDiffStatus
	Path              string
	ComparisonStatus  ComparisonStatus
	Reason            TextDiffReason
	ContextLines      int
	BaselineEncoding  *TextEncoding
	TargetEncoding    *TextEncoding
	AddedLines        int
	DeletedLines      int
	Patch             string
	BaselineSize      *int64
	BaselineLineCount *int
	TargetSize        *int64
	TargetLineCount   *int
	OutputBytes       int
}

type BlobStatus string

const (
	BlobReady       BlobStatus = "ready"
	BlobMissing     BlobStatus = "missing"
	BlobStale       BlobStatus = "stale"
	BlobUnavailable BlobStatus = "unavailable"
)

type BlobContent struct {
	Status   BlobStatus
	Bytes    []byte
	MIMEType string
	Filename string
}

type EntryQuery struct {
	IncludeUnchanged bool
	Cursor           string
	Anchor           int
	Limit            int
	Statuses         []ComparisonStatus
	Kinds            []EntryItemKind
	Path             string
}

type EntryPage struct {
	Entries    []Entry
	NextCursor string
}

type TreeKind string

const (
	TreeKindDirectory TreeKind = "directory"
	TreeKindFile      TreeKind = "file"
	TreeKindSymlink   TreeKind = "symlink"
	TreeKindIssue     TreeKind = "issue"
)

type TreeNode struct {
	Kind    TreeKind
	Name    string
	Path    string
	Counts  StatusCounts
	Issues  int
	ID      int
	Status  ComparisonStatus
	Message string
}

type TreePage struct {
	Children []TreeNode
}

type SearchPage struct {
	Results   []TreeNode
	Truncated bool
}

type SnapshotSummary struct {
	ID                string
	BaselineDirectory string
	TargetDirectory   string
	CreatedAt         string
	Counts            StatusCounts
	Issues            int
}
