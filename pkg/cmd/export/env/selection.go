package env

import "fmt"

// SelectionOptions controls how discovered files are chosen.
type SelectionOptions struct {
	Environment string
	Merge       bool
}

// EnvironmentChoice is one interactive environment-file choice.
type EnvironmentChoice struct {
	Value string
	Label string
}

// EnvironmentSelector presents environment-file choices to a user.
type EnvironmentSelector interface {
	SelectEnvironment(message string, choices []EnvironmentChoice) (value string, cancelled bool, err error)
}

// Selection is the ordered set of files to parse or a user cancellation.
type Selection struct {
	Files     []string
	Cancelled bool
}

// Select chooses files from a discovery result without reading their contents.
func Select(discovery Discovery, options SelectionOptions, selector EnvironmentSelector) (Selection, error) {
	if options.Environment != "" {
		return selectNamedEnvironment(discovery, options)
	}

	selectable := discovery.EnvironmentFiles
	if !options.Merge && discovery.BaseFile != "" {
		selectable = append([]string{discovery.BaseFile}, selectable...)
	}
	if len(selectable) == 0 {
		return Selection{Files: []string{discovery.BaseFile}}, nil
	}
	if len(selectable) == 1 {
		files := make([]string, 0, 2)
		if options.Merge && discovery.BaseFile != "" && selectable[0] != discovery.BaseFile {
			files = append(files, discovery.BaseFile)
		}
		files = append(files, selectable[0])
		return Selection{Files: files}, nil
	}

	choices := make([]EnvironmentChoice, 0, len(selectable))
	for _, file := range selectable {
		choices = append(choices, EnvironmentChoice{Value: file, Label: environmentLabel(file)})
	}
	selected, cancelled, err := selector.SelectEnvironment("Select environment", choices)
	if err != nil {
		return Selection{}, err
	}
	if cancelled {
		return Selection{Files: []string{}, Cancelled: true}, nil
	}

	files := make([]string, 0, 2)
	if options.Merge && discovery.BaseFile != "" {
		files = append(files, discovery.BaseFile)
	}
	files = append(files, selected)
	return Selection{Files: files}, nil
}

func selectNamedEnvironment(discovery Discovery, options SelectionOptions) (Selection, error) {
	target := ".env." + options.Environment
	if !contains(discovery.EnvironmentFiles, target) {
		return Selection{}, fmt.Errorf("No %s file found in %s", target, discovery.Directory)
	}

	files := make([]string, 0, 2)
	if options.Merge && discovery.BaseFile != "" {
		files = append(files, discovery.BaseFile)
	}
	files = append(files, target)
	return Selection{Files: files}, nil
}

func environmentLabel(filename string) string {
	if filename == ".env" {
		return "default"
	}
	suffix, _ := environmentSuffix(filename)
	return suffix
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
