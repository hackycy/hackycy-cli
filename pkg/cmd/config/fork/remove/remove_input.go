package remove

import "github.com/hackycy/hackycy-cli/internal/appconfig"

// Choice is one terminal-selectable removal option.
type Choice struct {
	Value       string
	Label       string
	Description string
}

// SelectPrompt describes the removal selection question.
type SelectPrompt struct {
	Message string
	Choices []Choice
}

// RemoveReader supplies the secret-safe Fork projection needed for removal selection.
type RemoveReader interface {
	ListForkInstances() ([]appconfig.ForkInstance, error)
}

// RemovePrompter selects one configured Fork instance for removal.
type RemovePrompter interface {
	Select(SelectPrompt) (value string, cancelled bool, err error)
}

// RemoveSelection records the read-only outcome before confirmation or mutation.
type RemoveSelection struct {
	Name      string
	Empty     bool
	Cancelled bool
}

// SelectRemove loads configured instances and selects one in persisted order.
func SelectRemove(reader RemoveReader, prompter RemovePrompter) (RemoveSelection, error) {
	instances, err := reader.ListForkInstances()
	if err != nil {
		return RemoveSelection{}, err
	}
	if len(instances) == 0 {
		return RemoveSelection{Empty: true}, nil
	}

	choices := make([]Choice, len(instances))
	for index, instance := range instances {
		choices[index] = Choice{
			Value: instance.Name,
			Label: instance.Name + " (" + instance.Host + ")",
		}
	}
	name, cancelled, err := prompter.Select(SelectPrompt{
		Message: "Select instance to remove",
		Choices: choices,
	})
	if err != nil {
		return RemoveSelection{}, err
	}
	if cancelled {
		return RemoveSelection{Cancelled: true}, nil
	}
	return RemoveSelection{Name: name}, nil
}
