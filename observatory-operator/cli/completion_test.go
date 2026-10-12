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

// stubLister records the parse and the kind it is asked with and
// returns a fixed set of names, so a test drives completeArgs without a
// cluster.
func stubLister(names ...string) (resourceLister, *completionParse, *confirmable) {
	var seen completionParse
	var kind confirmable
	return func(parse completionParse, asked confirmable) []string {
		seen, kind = parse, asked
		return names
	}, &seen, &kind
}

func TestCompleteArgs(t *testing.T) {
	lister, _, _ := stubLister("east", "east-portable", "west")
	cases := []struct {
		name          string
		args          []string
		wantCands     []string
		wantDirective int
	}{
		{"no words offers both verbs", nil, []string{"set-up", "take-down"}, compDirectiveNoFileComp},
		{"empty word offers both verbs", []string{""}, []string{"set-up", "take-down"}, compDirectiveNoFileComp},
		{"a verb prefix filters", []string{"t"}, []string{"take-down"}, compDirectiveNoFileComp},
		{"a wrong verb prefix offers nothing", []string{"xyz"}, nil, compDirectiveNoFileComp},
		{"the first positional lists the kinds", []string{"set-up", ""}, kindWords(), compDirectiveNoFileComp},
		{"a kind prefix filters", []string{"take-down", "te"}, []string{"telescope"}, compDirectiveNoFileComp},
		{"the second positional lists the resources", []string{"set-up", "telescope", ""}, []string{"east", "east-portable", "west"}, compDirectiveNoFileComp},
		{"a name prefix filters", []string{"set-up", "telescope", "ea"}, []string{"east", "east-portable"}, compDirectiveNoFileComp},
		{"an unknown kind offers no names", []string{"set-up", "guider", ""}, nil, compDirectiveNoFileComp},
		{"a dash offers flag names", []string{"set-up", "-"}, flagNames(), compDirectiveNoFileComp},
		{"a long-flag prefix filters", []string{"set-up", "--co"}, []string{"--context"}, compDirectiveNoFileComp},
		{"the --until value is open", []string{"set-up", "--until", ""}, nil, compDirectiveNoFileComp},
		{"kubeconfig completes a file path", []string{"set-up", "--kubeconfig", ""}, nil, compDirectiveDefault},
		{"a third positional offers nothing", []string{"set-up", "telescope", "east", ""}, nil, compDirectiveNoFileComp},
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

func TestCompleteArgsPassesTheContextNamespaceAndKindToTheLister(t *testing.T) {
	lister, seen, kind := stubLister("east")
	completeArgs([]string{"set-up", "--context", "lab", "-n", "backyard", "mounts", ""}, lister)
	if seen.context != "lab" || seen.namespace != "backyard" {
		t.Fatalf("context, namespace = %q, %q, want lab, backyard", seen.context, seen.namespace)
	}
	if kind.kind != "Mount" {
		t.Fatalf("kind = %+v, want Mount", kind)
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
			[]string{"set-up", "telescope"},
			completionParse{positionals: []string{"set-up", "telescope"}},
		},
		{
			"a flag and its value are not positionals",
			[]string{"set-up", "--context", "lab"},
			completionParse{positionals: []string{"set-up"}, context: "lab"},
		},
		{
			"an equals form binds the value",
			[]string{"--namespace=backyard", "set-up"},
			completionParse{positionals: []string{"set-up"}, namespace: "backyard"},
		},
		{
			"the until flag binds a value",
			[]string{"set-up", "--until", "6h", "telescope"},
			completionParse{positionals: []string{"set-up", "telescope"}},
		},
		{
			"a boolean flag consumes no value",
			[]string{"set-up", "--force", "telescope"},
			completionParse{positionals: []string{"set-up", "telescope"}},
		},
		{
			"a value flag with no value waits for the cursor",
			[]string{"set-up", "--by"},
			completionParse{positionals: []string{"set-up"}, pendingValueFlag: "--by"},
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

func TestRunCompleteWritesTheProtocol(t *testing.T) {
	var stdout strings.Builder
	if err := runComplete(context.Background(), []string{""}, &stdout); err != nil {
		t.Fatalf("runComplete: %v", err)
	}
	if got := stdout.String(); got != "set-up\ntake-down\n:4\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestCompletionScript(t *testing.T) {
	var stdout strings.Builder
	if err := completionScript("bash", &stdout); err != nil {
		t.Fatalf("completionScript(bash): %v", err)
	}
	got := stdout.String()
	for _, want := range []string{"__complete", "complete -o default", "kubectl-liken-observatory"} {
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

var telescopes = confirmable{"Telescope", "telescopes"}

// telescope builds an unstructured Telescope for the dynamic fake.
func telescope(namespace, name string) *unstructured.Unstructured {
	object := &unstructured.Unstructured{}
	object.SetGroupVersionKind(schema.GroupVersionKind{Group: group, Version: "v1alpha1", Kind: "Telescope"})
	object.SetNamespace(namespace)
	object.SetName(name)
	return object
}

func TestNewResourceListerReturnsTheNames(t *testing.T) {
	factory := func(parse completionParse) (dynamic.Interface, string, error) {
		client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
			map[schema.GroupVersionResource]string{telescopes.resource(): "TelescopeList"},
			telescope("backyard", "east"), telescope("lab", "west"))
		return client, parse.namespace, nil
	}
	lister := newResourceLister(context.Background(), factory)
	if got := lister(completionParse{namespace: "backyard"}, telescopes); !reflect.DeepEqual(got, []string{"east"}) {
		t.Fatalf("lister returned %v, want [east]", got)
	}
}

func TestNewResourceListerOnAFactoryError(t *testing.T) {
	factory := func(completionParse) (dynamic.Interface, string, error) {
		return nil, "", errors.New("no reachable cluster")
	}
	lister := newResourceLister(context.Background(), factory)
	if got := lister(completionParse{}, telescopes); got != nil {
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
