package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// stubLister records the parse it is asked with and returns a fixed set
// of names, so a test drives completeArgs without a cluster.
func stubLister(names ...string) (outputLister, *completionParse) {
	var seen completionParse
	return func(parse completionParse) []string {
		seen = parse
		return names
	}, &seen
}

func TestCompleteArgs(t *testing.T) {
	lister, _ := stubLister("boe-1080-display", "gsm-7716-lg-hdr-wqhd")
	cases := []struct {
		name          string
		args          []string
		wantCands     []string
		wantDirective int
	}{
		{"no words offers the verb", nil, []string{"capture"}, compDirectiveNoFileComp},
		{"empty word offers the verb", []string{""}, []string{"capture"}, compDirectiveNoFileComp},
		{"a verb prefix filters", []string{"cap"}, []string{"capture"}, compDirectiveNoFileComp},
		{"a wrong verb prefix offers nothing", []string{"xyz"}, nil, compDirectiveNoFileComp},
		{"the capture positional lists outputs", []string{"capture", ""}, []string{"boe-1080-display", "gsm-7716-lg-hdr-wqhd"}, compDirectiveNoFileComp},
		{"an output prefix filters", []string{"capture", "b"}, []string{"boe-1080-display"}, compDirectiveNoFileComp},
		{"a dash offers flag names", []string{"capture", "-"}, flagNames(), compDirectiveNoFileComp},
		{"a long-flag prefix filters", []string{"capture", "--co"}, []string{"--context"}, compDirectiveNoFileComp},
		{"the format value set", []string{"capture", "--format", ""}, []string{"mp4", "png"}, compDirectiveNoFileComp},
		{"a format value prefix filters", []string{"capture", "--format", "p"}, []string{"png"}, compDirectiveNoFileComp},
		{"kubeconfig completes a file path", []string{"capture", "--kubeconfig", ""}, nil, compDirectiveDefault},
		{"a second positional offers nothing", []string{"capture", "boe-1080-display", ""}, nil, compDirectiveNoFileComp},
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
	lister, seen := stubLister("boe-1080-display")
	completeArgs([]string{"capture", "--context", "liken-1", ""}, lister)
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
			[]string{"capture", "boe-1080-display"},
			completionParse{positionals: []string{"capture", "boe-1080-display"}},
		},
		{
			"a flag and its value are not positionals",
			[]string{"capture", "--context", "liken-1"},
			completionParse{positionals: []string{"capture"}, context: "liken-1"},
		},
		{
			"an equals form binds the value",
			[]string{"--kubeconfig=/tmp/kc", "capture"},
			completionParse{positionals: []string{"capture"}, kubeconfig: "/tmp/kc"},
		},
		{
			"a boolean flag consumes no value",
			[]string{"capture", "--force", "boe-1080-display"},
			completionParse{positionals: []string{"capture", "boe-1080-display"}},
		},
		{
			"an unknown flag is skipped",
			[]string{"capture", "--mystery", "boe-1080-display"},
			completionParse{positionals: []string{"capture", "boe-1080-display"}},
		},
		{
			"a value flag with no value waits for the cursor",
			[]string{"capture", "--context"},
			completionParse{positionals: []string{"capture"}, pendingValueFlag: "--context"},
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
		{"--kubeconfig=/a/b", "--kubeconfig", "/a/b", true},
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
	if flag, ok := lookupCompletionFlag("--format"); !ok || !flag.takesValue {
		t.Fatalf("lookupCompletionFlag(--format) = (%+v, %v), want a value flag", flag, ok)
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
	if !strings.Contains(got, "capture\n") {
		t.Fatalf("output %q does not offer the verb", got)
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
	for _, want := range []string{"__complete", "complete -o default", "kubectl-liken-display"} {
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

// display builds an unstructured Display for the dynamic fake. A Display
// is cluster-scoped, so it carries no namespace.
func display(name string) *unstructured.Unstructured {
	object := &unstructured.Unstructured{}
	object.SetGroupVersionKind(schema.GroupVersionKind{
		Group: displaysGVR.Group, Version: displaysGVR.Version, Kind: "Display",
	})
	object.SetName(name)
	return object
}

// newDisplayClient seeds a dynamic fake with cluster-scoped Display
// objects. The fake guesses a resource name from a kind and turns
// "Display" into "displaies", so the objects go in through Create on the
// real GVR rather than at construction, where the guess would file them
// under a name the list never reads.
func newDisplayClient(objects ...*unstructured.Unstructured) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{displaysGVR: "DisplayList"})
	for _, object := range objects {
		if _, err := client.Resource(displaysGVR).Create(context.Background(), object, metav1.CreateOptions{}); err != nil {
			panic(err)
		}
	}
	return client
}

func TestListDisplays(t *testing.T) {
	client := newDisplayClient(
		display("boe-1080-display"),
		display("gsm-7716-lg-hdr-wqhd"),
	)
	got, err := listDisplays(context.Background(), client)
	if err != nil {
		t.Fatalf("listDisplays: %v", err)
	}
	want := []string{"boe-1080-display", "gsm-7716-lg-hdr-wqhd"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listDisplays = %v, want %v", got, want)
	}
}

func TestListDisplaysEmptyCluster(t *testing.T) {
	client := newDisplayClient()
	got, err := listDisplays(context.Background(), client)
	if err != nil {
		t.Fatalf("listDisplays: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("listDisplays with no Display = %v, want none", got)
	}
}

func TestNewOutputListerReturnsTheNames(t *testing.T) {
	factory := func(parse completionParse) (dynamic.Interface, error) {
		if parse.context != "liken-1" {
			t.Fatalf("factory got context %q, want liken-1", parse.context)
		}
		return newDisplayClient(display("boe-1080-display")), nil
	}
	lister := newOutputLister(context.Background(), factory)
	got := lister(completionParse{context: "liken-1"})
	if !reflect.DeepEqual(got, []string{"boe-1080-display"}) {
		t.Fatalf("lister returned %v, want [boe-1080-display]", got)
	}
}

func TestNewOutputListerOnAFactoryError(t *testing.T) {
	factory := func(completionParse) (dynamic.Interface, error) {
		return nil, errors.New("no reachable cluster")
	}
	lister := newOutputLister(context.Background(), factory)
	if got := lister(completionParse{}); got != nil {
		t.Fatalf("lister returned %v on a factory error, want nil", got)
	}
}

func TestKubeClientFactoryReportsAnUnreachableConfig(t *testing.T) {
	file := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(file, []byte("not a kubeconfig"), 0o600); err != nil {
		t.Fatalf("writing the kubeconfig: %v", err)
	}
	if _, err := kubeClientFactory(completionParse{kubeconfig: file}); err == nil {
		t.Fatal("kubeClientFactory returned no error for a config with no cluster")
	}
}
