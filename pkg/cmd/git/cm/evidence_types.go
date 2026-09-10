package cm

// FileRole classifies one modeled path for semantic evidence selection.
type FileRole string

const (
	FileRoleSource     FileRole = "source"
	FileRoleTest       FileRole = "test"
	FileRoleDocs       FileRole = "docs"
	FileRoleConfig     FileRole = "config"
	FileRoleDependency FileRole = "dependency"
	FileRoleGenerated  FileRole = "generated"
	FileRoleBinary     FileRole = "binary"
	FileRoleSensitive  FileRole = "sensitive"
	FileRoleUnknown    FileRole = "unknown"
)

// ContentPolicy determines whether a file can contribute semantic evidence.
type ContentPolicy string

const (
	ContentInspect      ContentPolicy = "inspect"
	ContentMetadataOnly ContentPolicy = "metadata-only"
	ContentRedacted     ContentPolicy = "redacted"
)

// ChangeStats records additions and deletions for one snapshot file.
type ChangeStats struct {
	Additions int
	Deletions int
}

// DiffHunk is one parsed staged, worktree, or untracked patch hunk.
type DiffHunk struct {
	ID           string
	Source       string
	OldStart     int
	OldLines     int
	NewStart     int
	NewLines     int
	Heading      string
	AddedLines   []string
	DeletedLines []string
}

// ManifestState retains package-manifest before and after JSON for structured facts.
type ManifestState struct {
	Before *string
	After  *string
}

// SnapshotFile is one complete modeled entry in an immutable Git snapshot.
type SnapshotFile struct {
	ID             string
	Path           string
	OriginalPath   string
	Status         string
	IndexStatus    byte
	WorktreeStatus byte
	Role           FileRole
	ContentPolicy  ContentPolicy
	Stats          ChangeStats
	Hunks          []DiffHunk
	Manifest       *ManifestState
}

// GitSnapshot is the immutable source of one model request and commit recheck.
type GitSnapshot struct {
	RepositoryRoot string
	Scope          Scope
	ID             string
	Files          []SnapshotFile
	Totals         ChangeStats
}

// EvidenceFact is one atomic, non-truncated semantic fact supplied to the provider.
type EvidenceFact struct {
	ID         string
	Priority   int
	ClusterKey string
	FilePath   string
	HunkID     string
	Text       string
}

// EvidenceCoverage describes the selected local semantic evidence.
type EvidenceCoverage struct {
	EstimatedLocalPromptTokens int
	RepresentedClusters        int
	TotalClusters              int
	IncludedFacts              int
	OmittedFacts               int
	ContentCompacted           bool
}

// CompiledEvidence is the model-ready text plus deterministic coverage information.
type CompiledEvidence struct {
	Text     string
	Coverage EvidenceCoverage
	Facts    []EvidenceFact
}
