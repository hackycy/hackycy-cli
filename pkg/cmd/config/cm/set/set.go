package set

// SetRequest is the typed CLI request for updating one CM profile field.
type SetRequest struct {
	Profile string
	Key     string
	Value   string
}

// SetResult records a successful config cm set outcome.
type SetResult struct {
	Profile string
}

// SetWriter is the command-facing appconfig semantic update boundary.
type SetWriter interface {
	SetCMProfileValue(name, key, value string) error
}

// SetProfileValue delegates the exact field-update request to appconfig.
func SetProfileValue(writer SetWriter, request SetRequest) error {
	return writer.SetCMProfileValue(request.Profile, request.Key, request.Value)
}
