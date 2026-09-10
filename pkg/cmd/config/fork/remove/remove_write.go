package remove

// RemoveWriter is the command-facing appconfig semantic deletion boundary.
type RemoveWriter interface {
	RemoveForkInstance(name string) (bool, error)
}

// RemoveSelected deletes the selected Fork through appconfig and reports whether it still existed.
func RemoveSelected(writer RemoveWriter, name string) (bool, error) {
	return writer.RemoveForkInstance(name)
}
