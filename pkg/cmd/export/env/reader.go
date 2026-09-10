package env

import "path/filepath"

// FileReader reads selected dotenv files.
type FileReader interface {
	ReadFile(path string) ([]byte, error)
}

// Read returns selected file contents in selection order.
func Read(discovery Discovery, selection Selection, reader FileReader) ([]string, error) {
	contents := make([]string, 0, len(selection.Files))
	for _, file := range selection.Files {
		content, err := reader.ReadFile(filepath.Join(discovery.Directory, file))
		if err != nil {
			return nil, err
		}
		contents = append(contents, string(content))
	}
	return contents, nil
}
