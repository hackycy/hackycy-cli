package rm

import (
	"os"
	"path/filepath"
)

type explicitPlan struct {
	existing []string
	missing  []string
}

func planExplicit(workingDirectory string, operands []string) (explicitPlan, error) {
	plan := explicitPlan{
		existing: []string{},
		missing:  []string{},
	}
	for _, operand := range operands {
		path, err := resolveExplicitPath(workingDirectory, operand)
		if err != nil {
			return explicitPlan{}, err
		}
		if _, err := os.Stat(path); err != nil {
			plan.missing = append(plan.missing, path)
			continue
		}
		plan.existing = append(plan.existing, path)
	}
	return plan, nil
}

func resolveExplicitPath(workingDirectory, operand string) (string, error) {
	if filepath.IsAbs(operand) {
		return filepath.Abs(operand)
	}
	return filepath.Abs(filepath.Join(workingDirectory, operand))
}
