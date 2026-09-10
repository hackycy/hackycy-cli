package run

import "context"

// ChildRequest describes one package-manager child process.
type ChildRequest struct {
	Executable string
	Arguments  []string
	Directory  string
}

// ChildRunner owns package-manager child-process execution.
type ChildRunner interface {
	Run(context.Context, ChildRequest) (Result, error)
}

func childRequest(directory string, manager PackageManager, script string) ChildRequest {
	return ChildRequest{
		Executable: string(manager),
		Arguments:  []string{"run", script},
		Directory:  directory,
	}
}
