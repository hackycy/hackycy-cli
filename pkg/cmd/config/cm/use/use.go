package use

// UseRequest is the typed CLI request for selecting one CM profile.
type UseRequest struct {
	Profile string
}

// UseResult records a successful config cm use outcome.
type UseResult struct {
	Profile string
}

// UseWriter is the command-facing appconfig semantic selection boundary.
type UseWriter interface {
	SetDefaultCMProfile(name string) error
}

// SelectDefaultProfile delegates exact profile identity and result mapping to appconfig.
func SelectDefaultProfile(writer UseWriter, profile string) error {
	return writer.SetDefaultCMProfile(profile)
}
