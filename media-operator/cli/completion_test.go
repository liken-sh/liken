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
func stubLister(names ...string) (playerLister, *completionParse) {
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
		{"the capture positional lists players", []string{"capture", ""}, []string{"living-room", "kitchen", "loft"}, compDirectiveNoFileComp},
		{"a player prefix filters", []string{"capture", "l"}, []string{"living-room", "loft"}, compDirectiveNoFileComp},
		{"a dash offers flag names", []string{"capture", "-"}, flagNames(), compDirectiveNoFileComp},
		{"a long-flag prefix filters", []string{"capture", "--co"}, []string{"--context"}, compDirectiveNoFileComp},
		{"the format value set", []string{"capture", "--format", ""}, []string{"mkv", "mp4"}, compDirectiveNoFileComp},
		{"a format value prefix filters", []string{"capture", "--format", "mp"}, []string{"mp4"}, compDirectiveNoFileComp},
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

func TestCompleteArgsPassesTheContextAndNamespaceToTheLister(t *testing.T) {
	lister, seen := stubLister("living-room")
	completeArgs([]string{"capture", "--context", "liken-1", "-n", "default", ""}, lister)
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
			[]string{"--namespace=default", "capture"},
			completionParse{positionals: []string{"capture"}, namespace: "default"},
		},
		{
			"the short namespace flag binds",
			[]string{"capture", "-n", "house"},
			completionParse{positionals: []string{"capture"}, namespace: "house"},
		},
		{
			"a boolean flag consumes no value",
			[]string{"capture", "--force", "living-room"},
			completionParse{positionals: []string{"capture", "living-room"}},
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
	for _, want := range []string{"__complete", "complete -o default", "kubectl-liken-media"} {
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

// player builds an unstructured Player for the dynamic fake.
func player(namespace, name string) *unstructured.Unstructured {
	object := &unstructured.Unstructured{}
	object.SetGroupVersionKind(schema.GroupVersionKind{
		Group: playerGVR.Group, Version: playerGVR.Version, Kind: "Player",
	})
	object.SetNamespace(namespace)
	object.SetName(name)
	return object
}

func newPlayerClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{playerGVR: "PlayerList"}, objects...)
}

func TestListPlayers(t *testing.T) {
	client := newPlayerClient(
		player("default", "living-room"),
		player("default", "kitchen"),
		player("house", "loft"),
	)
	got, err := listPlayers(context.Background(), client, "default")
	if err != nil {
		t.Fatalf("listPlayers: %v", err)
	}
	want := []string{"kitchen", "living-room"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listPlayers(default) = %v, want %v", got, want)
	}
}

func TestListPlayersEmptyNamespace(t *testing.T) {
	client := newPlayerClient()
	got, err := listPlayers(context.Background(), client, "default")
	if err != nil {
		t.Fatalf("listPlayers: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("listPlayers with no players = %v, want none", got)
	}
}

func TestNewPlayerListerReturnsTheNames(t *testing.T) {
	factory := func(parse completionParse) (dynamic.Interface, string, error) {
		if parse.namespace != "default" {
			t.Fatalf("factory got namespace %q, want default", parse.namespace)
		}
		return newPlayerClient(player("default", "living-room")), parse.namespace, nil
	}
	lister := newPlayerLister(context.Background(), factory)
	got := lister(completionParse{namespace: "default"})
	if !reflect.DeepEqual(got, []string{"living-room"}) {
		t.Fatalf("lister returned %v, want [living-room]", got)
	}
}

func TestNewPlayerListerOnAFactoryError(t *testing.T) {
	factory := func(completionParse) (dynamic.Interface, string, error) {
		return nil, "", errors.New("no reachable cluster")
	}
	lister := newPlayerLister(context.Background(), factory)
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
