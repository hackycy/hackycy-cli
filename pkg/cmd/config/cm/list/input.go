package list

// Input is the typed input for config cm list.
type Input struct{}

// Result is the safe CM profile projection returned by config cm list.
type Result struct {
	Profiles []Profile
}
