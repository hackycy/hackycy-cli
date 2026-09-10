package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: hookctl <install|doctor|uninstall>")
		os.Exit(2)
	}

	controller, err := New(".", os.Stdout)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}

	var actionErr error
	switch os.Args[1] {
	case "install":
		actionErr = controller.Install(context.Background())
	case "doctor":
		actionErr = controller.Doctor(context.Background())
	case "uninstall":
		actionErr = controller.Uninstall(context.Background())
	default:
		_, _ = fmt.Fprintln(os.Stderr, "usage: hookctl <install|doctor|uninstall>")
		os.Exit(2)
	}
	if actionErr != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %s\n", actionErr)
		os.Exit(1)
	}
}
