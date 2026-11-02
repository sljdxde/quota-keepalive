// Command genicon writes QKA's source application icon for release packaging.
package main

import (
	"os"

	"qka/internal/brandicon"
)

var (
	writeFile      = os.WriteFile
	applicationPNG = brandicon.MasterPNG
	runCommand     = run
)

func main() {
	if err := runCommand(os.Args[1:]); err != nil {
		panic(err)
	}
}

func run(args []string) error {
	out := "packaging/appicon_1024.png"
	if len(args) > 0 {
		out = args[0]
	}
	if err := writeFile(out, applicationPNG(), 0o644); err != nil {
		return err
	}
	return nil
}
