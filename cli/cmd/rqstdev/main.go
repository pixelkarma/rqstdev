package main

import (
	"fmt"
	"os"

	"rqstdev/cli/internal/app"
)

func main() {
	if err := app.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "rqstdev: %v\n", err)
		os.Exit(1)
	}
}
