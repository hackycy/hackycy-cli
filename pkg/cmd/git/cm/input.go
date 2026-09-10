// Package cm owns Git CM's commit-message generation behavior.
package cm

import "github.com/hackycy/hackycy-cli/internal/appconfig"

// Input is the typed CLI request for git cm.
type Input struct {
	Profile   string
	TimeoutMS *float64
	Language  string
	Staged    bool
	Stage     bool
	StageAll  bool
	Push      *string
	StagePush *string
	DryRun    bool
	Body      bool
}

// ProfileDiagnostic is the profile projection safe for command presentation.
type ProfileDiagnostic struct {
	Name    string
	BaseURL string
	Model   string
}

// Result records the command-owned result before terminal presentation.
type Result struct {
	RepositoryRoot  string
	Scope           Scope
	Cancelled       bool
	NothingSelected bool
	NoChanges       bool
	NoChangeScope   Scope
	Committed       bool
	PromptedCommit  bool
	Pushed          bool
	PushRemote      string
	Profile         ProfileDiagnostic
	Generated       *GeneratedMessage
}

func profileDiagnostic(profile appconfig.ResolvedCMProfile) ProfileDiagnostic {
	return ProfileDiagnostic{Name: profile.Name, BaseURL: profile.BaseURL, Model: profile.Model}
}
