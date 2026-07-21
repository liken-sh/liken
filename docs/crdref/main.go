// crdref generates the manual's CRD reference pages.
//
// The docs Makefile runs it once per CRD:
//
//	go run ./crdref <crd.yaml> <out.md>
//
// The output lands in the Hugo content tree and is gitignored: the
// schemas are the source of truth, and the pages are build products,
// regenerated whenever a schema or this program changes.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "crdref: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: crdref <crd.yaml> <out.md>")
	}
	crd, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	page, err := Generate(crd, args[0])
	if err != nil {
		return err
	}
	return os.WriteFile(args[1], page, 0o644)
}
