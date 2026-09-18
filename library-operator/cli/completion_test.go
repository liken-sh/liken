package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// stubLister records the parse it is asked with and returns a fixed set
// of names, so a test drives completeArgs without a cluster.
func stubLister(names ...string) (libraryLister, *completionParse) {
	var seen completionParse
	return func(parse completionParse) []string {
		seen = parse
		return names
	}, &seen
}

func TestCompleteArgs(t *testing.T) {
	lister, _ := stubLister("movies", "kids-movies", "music")
	cases := []struct {
		name          string
		args          []string
		wantCands     []string
		wantDirective int
	}{
		{"no words offers both verbs", nil, []string{"reenrich", "rescan"}, compDirectiveNoFileComp},
		{"empty word offers both verbs", []string{""}, []string{"reenrich", "rescan"}, compDirectiveNoFileComp},
		{"a shared verb prefix offers both", []string{"re"}, []string{"reenrich", "rescan"}, compDirectiveNoFileComp},
		{"a reenrich-only prefix filters", []string{"ree"}, []string{"reenrich"}, compDirectiveNoFileComp},
		{"a rescan-only prefix filters", []string{"res"}, []string{"rescan"}, compDirectiveNoFileComp},
		{"a wrong verb prefix offers nothing", []string{"xyz"}, nil, compDirectiveNoFileComp},
		{"the reenrich positional lists libraries", []string{"reenrich", ""}, []string{"movies", "kids-movies", "music"}, compDirectiveNoFileComp},
		{"the rescan positional lists libraries", []string{"rescan", ""}, []string{"movies", "kids-movies", "music"}, compDirectiveNoFileComp},
		{"a library prefix filters", []string{"reenrich", "m"}, []string{"movies", "music"}, compDirectiveNoFileComp},
		{"a dash offers flag names", []string{"reenrich", "-"}, flagNames(), compDirectiveNoFileComp},
		{"a long-flag prefix filters", []string{"reenrich", "--co"}, []string{"--context"}, compDirectiveNoFileComp},
		{"the --only value set is unsorted", []string{"reenrich", "--only", ""}, refreshFactVocabulary, compDirectiveNoFileComp},
		{"a --only value prefix filters",
			[]string{"reenrich", "--only", "rating."},
			[]string{"rating.tmdb", "rating.imdb", "rating.rottentomatoes", "rating.metacritic"},
			compDirectiveNoFileComp},
		{"kubeconfig completes a file path", []string{"reenrich", "--kubeconfig", ""}, nil, compDirectiveDefault},
		{"a second positional offers nothing", []string{"reenrich", "movies", ""}, nil, compDirectiveNoFileComp},
		{"an unknown verb's positional offers nothing", []string{"frob", ""}, nil, compDirectiveNoFileComp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cands, directive := completeArgs(tc.args, lister)
			if !reflect.DeepEqual(cands, tc.wantCands) {
				t.Fatalf("candidates = %v, want %v", cands, tc.wantCands)
			}
			if directive != tc.wantDirective {
				t.Fatalf("directive = %d, want %d", directive, tc.wantDirective)
			}
		})
	}
}

func TestCompleteArgsPassesTheContextAndNamespaceToTheLister(t *testing.T) {
	lister, seen := stubLister("movies")
	completeArgs([]string{"reenrich", "--context", "liken-1", "-n", "default", ""}, lister)
	if seen.context != "liken-1" {
		t.Fatalf("context = %q, want liken-1", seen.context)
	}
	if seen.namespace != "default" {
		t.Fatalf("namespace = %q, want default", seen.namespace)
	}
}

func TestParseCompletionArgs(t *testing.T) {
	cases := []struct {
		name  string
		prior []string
		want  completionParse
	}{
		{
			"positionals only",
			[]string{"reenrich", "movies"},
			completionParse{positionals: []string{"reenrich", "movies"}},
		},
		{
			"a flag and its value are not positionals",
			[]string{"reenrich", "--context", "liken-1"},
			completionParse{positionals: []string{"reenrich"}, context: "liken-1"},
		},
		{
			"an equals form binds the value",
			[]string{"--namespace=default", "reenrich"},
			completionParse{positionals: []string{"reenrich"}, namespace: "default"},
		},
		{
			"the short namespace flag binds",
			[]string{"reenrich", "-n", "house"},
			completionParse{positionals: []string{"reenrich"}, namespace: "house"},
		},
		{
			"the only flag binds a value",
			[]string{"reenrich", "--only", "poster"},
			completionParse{positionals: []string{"reenrich"}},
		},
		{
			"a boolean flag consumes no value",
			[]string{"reenrich", "--force", "movies"},
			completionParse{positionals: []string{"reenrich", "movies"}},
		},
		{
			"an unknown flag is skipped",
			[]string{"reenrich", "--mystery", "movies"},
			completionParse{positionals: []string{"reenrich", "movies"}},
		},
		{
			"a value flag with no value waits for the cursor",
			[]string{"reenrich", "--context"},
			completionParse{positionals: []string{"reenrich"}, pendingValueFlag: "--context"},
		},
		{
			"a bare dash is a positional",
			[]string{"-"},
			completionParse{positionals: []string{"-"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCompletionArgs(tc.prior)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseCompletionArgs(%v) = %+v, want %+v", tc.prior, got, tc.want)
			}
		})
	}
}

func TestSplitFlag(t *testing.T) {
	cases := []struct {
		token    string
		name     string
		value    string
		hasEqual bool
	}{
		{"--context=liken-1", "--context", "liken-1", true},
		{"--force", "--force", "", false},
		{"-n", "-n", "", false},
		{"--label=a=b", "--label", "a=b", true},
	}
	for _, tc := range cases {
		t.Run(tc.token, func(t *testing.T) {
			name, value, hasEqual := splitFlag(tc.token)
			if name != tc.name || value != tc.value || hasEqual != tc.hasEqual {
				t.Fatalf("splitFlag(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.token, name, value, hasEqual, tc.name, tc.value, tc.hasEqual)
			}
		})
	}
}

func TestLookupCompletionFlag(t *testing.T) {
	if flag, ok := lookupCompletionFlag("--only"); !ok || !flag.takesValue {
		t.Fatalf("lookupCompletionFlag(--only) = (%+v, %v), want a value flag", flag, ok)
	}
	if _, ok := lookupCompletionFlag("--nope"); ok {
		t.Fatal("lookupCompletionFlag(--nope) found an unknown flag")
	}
}

func TestRunCompleteWritesTheProtocol(t *testing.T) {
	var stdout strings.Builder
	if err := runComplete(context.Background(), []string{""}, &stdout); err != nil {
		t.Fatalf("runComplete: %v", err)
	}
	got := stdout.String()
	if !strings.Contains(got, "reenrich\n") {
		t.Fatalf("output %q does not offer the reenrich verb", got)
	}
	if !strings.Contains(got, "rescan\n") {
		t.Fatalf("output %q does not offer the rescan verb", got)
	}
	if !strings.HasSuffix(got, ":4\n") {
		t.Fatalf("output %q does not end with the directive line", got)
	}
}

func TestCompletionScript(t *testing.T) {
	var stdout strings.Builder
	if err := completionScript("bash", &stdout); err != nil {
		t.Fatalf("completionScript(bash): %v", err)
	}
	got := stdout.String()
	for _, want := range []string{"__complete", "complete -o default", "kubectl-liken-library"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the script does not contain %q", want)
		}
	}
}

func TestCompletionScriptRejectsAnOtherShell(t *testing.T) {
	var stdout strings.Builder
	if err := completionScript("zsh", &stdout); err == nil {
		t.Fatal("completionScript(zsh) returned no error")
	}
}

// library builds an unstructured Library for the dynamic fake.
func library(namespace, name string) *unstructured.Unstructured {
	object := &unstructured.Unstructured{}
	object.SetGroupVersionKind(schema.GroupVersionKind{
		Group: libraryResource.Group, Version: libraryResource.Version, Kind: "Library",
	})
	object.SetNamespace(namespace)
	object.SetName(name)
	return object
}

func newLibraryClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{libraryResource: "LibraryList"}, objects...)
}

func TestListLibraries(t *testing.T) {
	client := newLibraryClient(
		library("default", "movies"),
		library("default", "kids-movies"),
		library("house", "music"),
	)
	got, err := listLibraries(context.Background(), client, "default")
	if err != nil {
		t.Fatalf("listLibraries: %v", err)
	}
	want := []string{"kids-movies", "movies"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listLibraries(default) = %v, want %v", got, want)
	}
}

func TestListLibrariesEmptyNamespace(t *testing.T) {
	client := newLibraryClient()
	got, err := listLibraries(context.Background(), client, "default")
	if err != nil {
		t.Fatalf("listLibraries: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("listLibraries with no libraries = %v, want none", got)
	}
}

func TestNewLibraryListerReturnsTheNames(t *testing.T) {
	factory := func(parse completionParse) (dynamic.Interface, string, error) {
		if parse.namespace != "default" {
			t.Fatalf("factory got namespace %q, want default", parse.namespace)
		}
		return newLibraryClient(library("default", "movies")), parse.namespace, nil
	}
	lister := newLibraryLister(context.Background(), factory)
	got := lister(completionParse{namespace: "default"})
	if !reflect.DeepEqual(got, []string{"movies"}) {
		t.Fatalf("lister returned %v, want [movies]", got)
	}
}

func TestNewLibraryListerOnAFactoryError(t *testing.T) {
	factory := func(completionParse) (dynamic.Interface, string, error) {
		return nil, "", errors.New("no reachable cluster")
	}
	lister := newLibraryLister(context.Background(), factory)
	if got := lister(completionParse{}); got != nil {
		t.Fatalf("lister returned %v on a factory error, want nil", got)
	}
}

func TestKubeClientFactoryReportsAnUnreachableConfig(t *testing.T) {
	file := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(file, []byte("not a kubeconfig"), 0o600); err != nil {
		t.Fatalf("writing the kubeconfig: %v", err)
	}
	if _, _, err := kubeClientFactory(completionParse{kubeconfig: file, namespace: "default"}); err == nil {
		t.Fatal("kubeClientFactory returned no error for a config with no cluster")
	}
}
