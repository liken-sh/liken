package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestCompleteArgs(t *testing.T) {
	cases := []struct {
		name          string
		args          []string
		wantCands     []string
		wantDirective int
	}{
		{"no words offers the verbs and domains", nil, topLevelCandidates(), compDirectiveNoFileComp},
		{"empty word offers the verbs and domains", []string{""}, topLevelCandidates(), compDirectiveNoFileComp},
		{"a verb prefix filters", []string{"ver"}, []string{"version"}, compDirectiveNoFileComp},
		{"a domain prefix filters", []string{"au"}, []string{"audio"}, compDirectiveNoFileComp},
		{"a prefix over both filters both", []string{"m"}, []string{"media", "mint"}, compDirectiveNoFileComp},
		{"a wrong prefix offers nothing", []string{"zzz"}, nil, compDirectiveNoFileComp},
		{"the plugins subcommands", []string{"plugins", ""}, []string{"list", "remove", "sync"}, compDirectiveNoFileComp},
		{"a plugins subcommand prefix filters", []string{"plugins", "s"}, []string{"sync"}, compDirectiveNoFileComp},
		{"a dash offers flag names", []string{"plugins", "sync", "-"}, flagNames(), compDirectiveNoFileComp},
		{"a flag prefix filters", []string{"fetch", "-dig"}, []string{"-digest"}, compDirectiveNoFileComp},
		{"a verb's argument falls to file completion", []string{"new", ""}, nil, compDirectiveDefault},
		{"a server value offers nothing", []string{"plugins", "sync", "-server", ""}, nil, compDirectiveNoFileComp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cands, directive := completeArgs(tc.args)
			if !reflect.DeepEqual(cands, tc.wantCands) {
				t.Fatalf("candidates = %v, want %v", cands, tc.wantCands)
			}
			if directive != tc.wantDirective {
				t.Fatalf("directive = %d, want %d", directive, tc.wantDirective)
			}
		})
	}
}

func TestTopLevelCandidatesHoldTheVerbsAndDomains(t *testing.T) {
	got := topLevelCandidates()
	set := map[string]bool{}
	for _, item := range got {
		if set[item] {
			t.Fatalf("candidate %q appears twice", item)
		}
		set[item] = true
	}
	for _, want := range append(append([]string{}, topLevelVerbs...), pluginDomains...) {
		if !set[want] {
			t.Fatalf("candidates miss %q", want)
		}
	}
	// media names a verb and a domain, so the set holds one fewer than
	// the two lists together.
	if len(got) != len(topLevelVerbs)+len(pluginDomains)-1 {
		t.Fatalf("candidates = %d, want %d", len(got), len(topLevelVerbs)+len(pluginDomains)-1)
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
			[]string{"plugins", "sync"},
			completionParse{positionals: []string{"plugins", "sync"}},
		},
		{
			"a flag and its value are not positionals",
			[]string{"plugins", "sync", "-server", "https://host", "dir"},
			completionParse{positionals: []string{"plugins", "sync", "dir"}},
		},
		{
			"an equals form binds the value",
			[]string{"fetch", "-digest=sha256:ab", "url"},
			completionParse{positionals: []string{"fetch", "url"}},
		},
		{
			"a two-dash flag is read the same",
			[]string{"plugins", "sync", "--server", "https://host"},
			completionParse{positionals: []string{"plugins", "sync"}},
		},
		{
			"an unknown flag is skipped",
			[]string{"new", "--mystery", "dir"},
			completionParse{positionals: []string{"new", "dir"}},
		},
		{
			"a value flag with no value waits for the cursor",
			[]string{"plugins", "sync", "-server"},
			completionParse{positionals: []string{"plugins", "sync"}, pendingValueFlag: "-server"},
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
		{"-server=https://host", "-server", "https://host", true},
		{"-console", "-console", "", false},
		{"-digest=sha256:a=b", "-digest", "sha256:a=b", true},
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
	if flag, ok := lookupCompletionFlag("-server"); !ok || !flag.takesValue {
		t.Fatalf("lookupCompletionFlag(-server) = (%+v, %v), want a value flag", flag, ok)
	}
	if flag, ok := lookupCompletionFlag("--server"); !ok || !flag.takesValue {
		t.Fatalf("lookupCompletionFlag(--server) = (%+v, %v), want a value flag", flag, ok)
	}
	if _, ok := lookupCompletionFlag("-nope"); ok {
		t.Fatal("lookupCompletionFlag(-nope) found an unknown flag")
	}
}

func TestCompleteFlagValueOffersNothing(t *testing.T) {
	cands, directive := completeFlagValue("-server", "")
	if cands != nil || directive != compDirectiveNoFileComp {
		t.Fatalf("completeFlagValue = (%v, %d), want (nil, %d)", cands, directive, compDirectiveNoFileComp)
	}
}

func TestRunCompleteWritesTheProtocol(t *testing.T) {
	var stdout strings.Builder
	if err := runComplete([]string{""}, &stdout); err != nil {
		t.Fatalf("runComplete: %v", err)
	}
	got := stdout.String()
	for _, want := range []string{"plugins\n", "display\n", "version\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q does not offer %q", got, want)
		}
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
	for _, want := range []string{"__complete", "complete -o default", "liken", "kubectl-liken"} {
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

func TestRunAnswersTheCompleteVerb(t *testing.T) {
	if err := run([]string{"__complete", ""}); err != nil {
		t.Fatalf("run __complete: %v", err)
	}
}

func TestRunPrintsTheCompletionScript(t *testing.T) {
	if err := run([]string{"completion", "bash"}); err != nil {
		t.Fatalf("run completion bash: %v", err)
	}
}
