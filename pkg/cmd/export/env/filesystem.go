package env

import "os"

type osExportEnvReader struct{}

func (osExportEnvReader) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

type osExportEnvWriter struct{}

func (osExportEnvWriter) WriteFile(path string, content []byte) error {
	return os.WriteFile(path, content, 0o666)
}
