package remove

// RemoveWriter is the command-facing appconfig semantic deletion boundary.
type RemoveWriter interface {
	RemoveCMProfile(name string) (bool, error)
}

// RemoveProfile delegates one confirmed profile deletion to appconfig.
func RemoveProfile(writer RemoveWriter, name string) (bool, error) {
	return writer.RemoveCMProfile(name)
}
