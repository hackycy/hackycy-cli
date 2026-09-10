package run

// ScriptChoice is one selectable package script.
type ScriptChoice struct {
	Value string
	Label string
	Hint  string
}

// ScriptPrompt describes the script selector.
type ScriptPrompt struct {
	Message string
	Options []ScriptChoice
}

// PackageManagerChoice is one selectable package manager.
type PackageManagerChoice struct {
	Value PackageManager
	Label string
}

// PackageManagerPrompt describes the package-manager selector.
type PackageManagerPrompt struct {
	Message string
	Options []PackageManagerChoice
}

// Prompter owns the two interactive selections required by run.
type Prompter interface {
	SelectScript(ScriptPrompt) (name string, cancelled bool, err error)
	SelectPackageManager(PackageManagerPrompt) (PackageManager, bool, error)
}

func selectScript(prompter Prompter, scripts []Script) (string, bool, error) {
	options := make([]ScriptChoice, 0, len(scripts))
	for _, script := range scripts {
		options = append(options, ScriptChoice{
			Value: script.Name,
			Label: script.Name,
			Hint:  script.Command,
		})
	}
	return prompter.SelectScript(ScriptPrompt{
		Message: "Select a script to run:",
		Options: options,
	})
}

func selectPackageManager(prompter Prompter, managers []PackageManager) (PackageManager, bool, error) {
	options := make([]PackageManagerChoice, 0, len(managers))
	for _, manager := range managers {
		options = append(options, PackageManagerChoice{Value: manager, Label: string(manager)})
	}
	return prompter.SelectPackageManager(PackageManagerPrompt{
		Message: "Select a package manager:",
		Options: options,
	})
}
