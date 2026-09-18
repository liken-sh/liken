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
func stubLister(names ...string) (endpointLister, *completionParse) {
	var seen completionParse
	return func(parse completionParse) []string {
		seen = parse
		return names
	}, &seen
}

func TestCompleteArgs(t *testing.T) {
	lister, _ := stubLister("living-room", "kitchen", "loft")
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
		{"the capture positional lists sinks", []string{"capture", ""}, []string{"living-room", "kitchen", "loft"}, compDirectiveNoFileComp},
		{"a sink prefix filters", []string{"capture", "l"}, []string{"living-room", "loft"}, compDirectiveNoFileComp},
		{"--source before the positional still lists names", []string{"capture", "--source", ""}, []string{"living-room", "kitchen", "loft"}, compDirectiveNoFileComp},
		{"a dash offers flag names", []string{"capture", "-"}, flagNames(), compDirectiveNoFileComp},
		{"a long-flag prefix filters", []string{"capture", "--co"}, []string{"--context"}, compDirectiveNoFileComp},
		{"the format value set", []string{"capture", "--format", ""}, []string{"flac", "opus", "wav"}, compDirectiveNoFileComp},
		{"a format value prefix filters", []string{"capture", "--format", "fl"}, []string{"flac"}, compDirectiveNoFileComp},
		{"kubeconfig completes a file path", []string{"capture", "--kubeconfig", ""}, nil, compDirectiveDefault},
		{"a second positional offers nothing", []string{"capture", "living-room", ""}, nil, compDirectiveNoFileComp},
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

func TestCompleteArgsSwitchesToSourceNames(t *testing.T) {
	lister, seen := stubLister("mic-1")
	completeArgs([]string{"capture", "--source", ""}, lister)
	if !seen.source {
		t.Fatal("completeArgs did not mark the parse as --source")
	}
}

func TestCompleteArgsDefaultsToSinkNames(t *testing.T) {
	lister, seen := stubLister("living-room")
	completeArgs([]string{"capture", ""}, lister)
	if seen.source {
		t.Fatal("completeArgs marked the parse as --source with no --source given")
	}
}

func TestCompleteArgsPassesTheContextToTheLister(t *testing.T) {
	lister, seen := stubLister("living-room")
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
			[]string{"capture", "living-room"},
			completionParse{positionals: []string{"capture", "living-room"}},
		},
		{
			"a flag and its value are not positionals",
			[]string{"capture", "--context", "liken-1"},
			completionParse{positionals: []string{"capture"}, context: "liken-1"},
		},
		{
			"an equals form binds the value",
			[]string{"--context=liken-1", "capture"},
			completionParse{positionals: []string{"capture"}, context: "liken-1"},
		},
		{
			"a boolean flag consumes no value",
			[]string{"capture", "--force", "living-room"},
			completionParse{positionals: []string{"capture", "living-room"}},
		},
		{
			"--source sets the field and is not a positional",
			[]string{"capture", "--source", "mic-1"},
			completionParse{positionals: []string{"capture", "mic-1"}, source: true},
		},
		{
			"an unknown flag is skipped",
			[]string{"capture", "--mystery", "living-room"},
			completionParse{positionals: []string{"capture", "living-room"}},
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
		{"--source", "--source", "", false},
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
	if flag, ok := lookupCompletionFlag("--source"); !ok || flag.takesValue {
		t.Fatalf("lookupCompletionFlag(--source) = (%+v, %v), want a boolean flag", flag, ok)
	}
	if _, ok := lookupCompletionFlag("--nope"); ok {
		t.Fatal("lookupCompletionFlag(--nope) found an unknown flag")
	}
	if _, ok := lookupCompletionFlag("--namespace"); ok {
		t.Fatal("lookupCompletionFlag(--namespace) found a flag audio never binds")
	}
	if _, ok := lookupCompletionFlag("-n"); ok {
		t.Fatal("lookupCompletionFlag(-n) found a flag audio never binds")
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
	for _, want := range []string{"__complete", "complete -o default", "kubectl-liken-audio"} {
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

// endpoint builds an unstructured Sink or Source object for the dynamic
// fake. Both kinds are cluster-scoped, so it sets no namespace.
func endpoint(gvk schema.GroupVersionKind, name string) *unstructured.Unstructured {
	object := &unstructured.Unstructured{}
	object.SetGroupVersionKind(gvk)
	object.SetName(name)
	return object
}

func newEndpointClient(listKinds map[schema.GroupVersionResource]string, objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objects...)
}

func TestListEndpoints(t *testing.T) {
	sinkKind := schema.GroupVersionKind{Group: sinkGVR.Group, Version: sinkGVR.Version, Kind: "Sink"}
	sourceKind := schema.GroupVersionKind{Group: sourceGVR.Group, Version: sourceGVR.Version, Kind: "Source"}

	client := newEndpointClient(
		map[schema.GroupVersionResource]string{
			sinkGVR:   "SinkList",
			sourceGVR: "SourceList",
		},
		endpoint(sinkKind, "living-room"),
		endpoint(sinkKind, "kitchen"),
		endpoint(sourceKind, "mic-1"),
	)

	sinks, err := listEndpoints(context.Background(), client, sinkGVR)
	if err != nil {
		t.Fatalf("listEndpoints(sinkGVR): %v", err)
	}
	if want := []string{"kitchen", "living-room"}; !reflect.DeepEqual(sinks, want) {
		t.Fatalf("listEndpoints(sinkGVR) = %v, want %v", sinks, want)
	}

	sources, err := listEndpoints(context.Background(), client, sourceGVR)
	if err != nil {
		t.Fatalf("listEndpoints(sourceGVR): %v", err)
	}
	if want := []string{"mic-1"}; !reflect.DeepEqual(sources, want) {
		t.Fatalf("listEndpoints(sourceGVR) = %v, want %v", sources, want)
	}
}

func TestListEndpointsEmpty(t *testing.T) {
	client := newEndpointClient(map[schema.GroupVersionResource]string{sinkGVR: "SinkList"})
	got, err := listEndpoints(context.Background(), client, sinkGVR)
	if err != nil {
		t.Fatalf("listEndpoints: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("listEndpoints with no sinks = %v, want none", got)
	}
}

func TestNewEndpointListerReturnsTheNames(t *testing.T) {
	sinkKind := schema.GroupVersionKind{Group: sinkGVR.Group, Version: sinkGVR.Version, Kind: "Sink"}
	factory := func(completionParse) (dynamic.Interface, error) {
		return newEndpointClient(
			map[schema.GroupVersionResource]string{sinkGVR: "SinkList"},
			endpoint(sinkKind, "living-room"),
		), nil
	}
	lister := newEndpointLister(context.Background(), factory)
	got := lister(completionParse{})
	if !reflect.DeepEqual(got, []string{"living-room"}) {
		t.Fatalf("lister returned %v, want [living-room]", got)
	}
}

func TestNewEndpointListerSwitchesToSourcesOnSource(t *testing.T) {
	sourceKind := schema.GroupVersionKind{Group: sourceGVR.Group, Version: sourceGVR.Version, Kind: "Source"}
	factory := func(completionParse) (dynamic.Interface, error) {
		return newEndpointClient(
			map[schema.GroupVersionResource]string{sourceGVR: "SourceList"},
			endpoint(sourceKind, "mic-1"),
		), nil
	}
	lister := newEndpointLister(context.Background(), factory)
	got := lister(completionParse{source: true})
	if !reflect.DeepEqual(got, []string{"mic-1"}) {
		t.Fatalf("lister returned %v, want [mic-1]", got)
	}
}

func TestNewEndpointListerOnAFactoryError(t *testing.T) {
	factory := func(completionParse) (dynamic.Interface, error) {
		return nil, errors.New("no reachable cluster")
	}
	lister := newEndpointLister(context.Background(), factory)
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
