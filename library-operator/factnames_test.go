package main

// The CLI shares no code with the operator, so its copy of the fact
// vocabulary drifts silently. Nothing compares the two at build time, so
// this test reads the CLI's source and holds the copies together.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"testing"
)

// The string literals one variable holds, read out of a Go source file,
// because the two packages cannot import each other.
func stringListInFile(t *testing.T, path, name string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		declaration, ok := decl.(*ast.GenDecl)
		if !ok || declaration.Tok != token.VAR {
			continue
		}
		for _, spec := range declaration.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != name {
				continue
			}
			literal, ok := value.Values[0].(*ast.CompositeLit)
			if !ok {
				t.Fatalf("%s in %s holds no list", name, path)
			}
			names := []string{}
			for _, element := range literal.Elts {
				text, ok := element.(*ast.BasicLit)
				if !ok || text.Kind != token.STRING {
					t.Fatalf("%s in %s holds an element that is no string", name, path)
				}
				unquoted, err := strconv.Unquote(text.Value)
				if err != nil {
					t.Fatal(err)
				}
				names = append(names, unquoted)
			}
			return names
		}
	}
	t.Fatalf("%s declares no variable named %s", path, name)
	return nil
}

// The CLI's --only list is the operator's fact list, name for name and in the
// same order, so a fact one of them gains is a fact the other has.
func TestTheCLIFactListIsTheOperators(t *testing.T) {
	cli := stringListInFile(t, "cli/facts.go", "refreshFactVocabulary")
	if !slices.Equal(cli, factVocabulary) {
		t.Errorf("cli/facts.go names %v, want the operator's %v", cli, factVocabulary)
	}
}

// Every fact is a refresh target and the walk is the one target that is not a
// fact, so the code that reads spec.refresh for enrichment can skip it.
func TestTheRefreshVocabularyIsTheFactsAndTheWalk(t *testing.T) {
	for _, fact := range factVocabulary {
		if !isContainerFact(fact) {
			t.Errorf("%s is no container fact", fact)
		}
		if !slices.Contains(refreshVocabulary, fact) {
			t.Errorf("%s is no refresh target", fact)
		}
	}
	if isContainerFact(refreshWalk) {
		t.Errorf("%s reads as a container fact", refreshWalk)
	}
	if got := len(refreshVocabulary); got != len(factVocabulary)+1 {
		t.Errorf("refreshVocabulary holds %d names, want one more than the %d facts",
			got, len(factVocabulary))
	}
}
