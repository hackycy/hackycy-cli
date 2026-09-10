package main

import (
	"os"

	"github.com/hackycy/hackycy-cli/internal/ycycmd"
)

var version = "0.0.0-dev"

func main() {
	os.Exit(ycycmd.Main(version))
}
