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
func stubLister(names ...string) (peripheralLister, *completionParse) {
	var seen completionParse
	return func(parse completionParse) []string {
		seen = parse
		return names
	}, &seen
}

func TestCompleteArgs(t *testing.T) {
	lister, _ := stubLister("a0-ab-51-33-b7-12", "b1-cd-99-44-22-11")
	cases := []struct {
		name          string
		args          []string
		wantCands     []string
		wantDirective int
	}{
		{"no words offers the verbs", nil, []string{"pair", "unpair"}, compDirectiveNoFileComp},
		{"empty word offers the verbs", []string{""}, []string{"pair", "unpair"}, compDirectiveNoFileComp},
		{"a verb prefix filters", []string{"un"}, []string{"unpair"}, compDirectiveNoFileComp},
		{"a wrong verb prefix offers nothing", []string{"xyz"}, nil, compDirectiveNoFileComp},
		{"the unpair positional lists peripherals", []string{"unpair", ""}, []string{"a0-ab-51-33-b7-12", "b1-cd-99-44-22-11"}, compDirectiveNoFileComp},
		{"a peripheral prefix filters", []string{"unpair", "a0"}, []string{"a0-ab-51-33-b7-12"}, compDirectiveNoFileComp},
		{"pair's positional offers nothing", []string{"pair", ""}, nil, compDirectiveNoFileComp},
		{"a dash offers flag names", []string{"unpair", "-"}, flagNames(), compDirectiveNoFileComp},
		{"a long-flag prefix filters", []string{"pair", "--co"}, []string{"--context"}, compDirectiveNoFileComp},
		{"kubeconfig completes a file path", []string{"pair", "--kubeconfig", ""}, nil, compDirectiveDefault},
		{"a second positional offers nothing", []string{"unpair", "a0-ab-51-33-b7-12", ""}, nil, compDirectiveNoFileComp},
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

func TestCompleteArgsPassesTheContextToTheLister(t *testing.T) {
	lister, seen := stubLister("a0-ab-51-33-b7-12")
	completeArgs([]string{"unpair", "--context", "liken-1", ""}, lister)
	if seen.context != "liken-1" {
		t.Fatalf("context = %q, want liken-1", seen.context)
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
			[]string{"unpair", "a0-ab-51-33-b7-12"},
			completionParse{positionals: []string{"unpair", "a0-ab-51-33-b7-12"}},
		},
		{
			"a flag and its value are not positionals",
			[]string{"pair", "--context", "liken-1"},
			completionParse{positionals: []string{"pair"}, context: "liken-1"},
		},
		{
			"an equals form binds the value",
			[]string{"--namespace=default", "unpair"},
			completionParse{positionals: []string{"unpair"}, namespace: "default"},
		},
		{
			"the short namespace flag binds",
			[]string{"unpair", "-n", "house"},
			completionParse{positionals: []string{"unpair"}, namespace: "house"},
		},
		{
			"the adapter flag consumes its value",
			[]string{"pair", "--adapter", "hci0", "extra"},
			completionParse{positionals: []string{"pair", "extra"}},
		},
		{
			"the window flag consumes its value",
			[]string{"pair", "--window", "30", "extra"},
			completionParse{positionals: []string{"pair", "extra"}},
		},
		{
			"a boolean flag consumes no value",
			[]string{"unpair", "--force", "a0-ab-51-33-b7-12"},
			completionParse{positionals: []string{"unpair", "a0-ab-51-33-b7-12"}},
		},
		{
			"an unknown flag is skipped",
			[]string{"unpair", "--mystery", "a0-ab-51-33-b7-12"},
			completionParse{positionals: []string{"unpair", "a0-ab-51-33-b7-12"}},
		},
		{
			"a value flag with no value waits for the cursor",
			[]string{"pair", "--context"},
			completionParse{positionals: []string{"pair"}, pendingValueFlag: "--context"},
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
	if flag, ok := lookupCompletionFlag("--adapter"); !ok || !flag.takesValue {
		t.Fatalf("lookupCompletionFlag(--adapter) = (%+v, %v), want a value flag", flag, ok)
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
	if !strings.Contains(got, "pair\n") || !strings.Contains(got, "unpair\n") {
		t.Fatalf("output %q does not offer both verbs", got)
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
	for _, want := range []string{"__complete", "complete -o default", "kubectl-liken-bluetooth"} {
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

// peripheral builds an unstructured Peripheral for the dynamic fake.
// Peripheral is cluster-scoped, so the object carries no namespace.
func peripheral(name string) *unstructured.Unstructured {
	object := &unstructured.Unstructured{}
	object.SetGroupVersionKind(schema.GroupVersionKind{
		Group: peripheralGVR.Group, Version: peripheralGVR.Version, Kind: "Peripheral",
	})
	object.SetName(name)
	return object
}

func newPeripheralClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{peripheralGVR: "PeripheralList"}, objects...)
}

func TestListPeripherals(t *testing.T) {
	client := newPeripheralClient(
		peripheral("b1-cd-99-44-22-11"),
		peripheral("a0-ab-51-33-b7-12"),
	)
	got, err := listPeripherals(context.Background(), client)
	if err != nil {
		t.Fatalf("listPeripherals: %v", err)
	}
	want := []string{"a0-ab-51-33-b7-12", "b1-cd-99-44-22-11"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listPeripherals = %v, want %v", got, want)
	}
}

func TestListPeripheralsWithNone(t *testing.T) {
	client := newPeripheralClient()
	got, err := listPeripherals(context.Background(), client)
	if err != nil {
		t.Fatalf("listPeripherals: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("listPeripherals with none = %v, want none", got)
	}
}

func TestNewPeripheralListerReturnsTheNames(t *testing.T) {
	factory := func(completionParse) (dynamic.Interface, string, error) {
		return newPeripheralClient(peripheral("a0-ab-51-33-b7-12")), "ignored", nil
	}
	lister := newPeripheralLister(context.Background(), factory)
	got := lister(completionParse{})
	if !reflect.DeepEqual(got, []string{"a0-ab-51-33-b7-12"}) {
		t.Fatalf("lister returned %v, want [a0-ab-51-33-b7-12]", got)
	}
}

func TestNewPeripheralListerOnAFactoryError(t *testing.T) {
	factory := func(completionParse) (dynamic.Interface, string, error) {
		return nil, "", errors.New("no reachable cluster")
	}
	lister := newPeripheralLister(context.Background(), factory)
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
