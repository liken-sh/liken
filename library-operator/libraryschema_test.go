package main

// What these tests read: the Library CRD as the cluster applies it,
// against the operator that reconciles it. The two hold one vocabulary,
// so a spec the API server admits is a spec this operator can serve.

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// The schema of the one version this CRD serves.
func librarySchema(t *testing.T) map[string]any {
	t.Helper()
	body, err := os.ReadFile("deploy/libraries-crd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	document := map[string]any{}
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	versions := document["spec"].(map[string]any)["versions"].([]any)
	return versions[0].(map[string]any)
}

// The names a CEL list holds, in the order the rule states them.
func namesInRule(t *testing.T, rule string) []string {
	t.Helper()
	start, end := strings.Index(rule, "["), strings.LastIndex(rule, "]")
	if start < 0 || end < start {
		t.Fatalf("the rule holds no list: %s", rule)
	}
	names := []string{}
	for _, name := range strings.Split(rule[start+1:end], ",") {
		names = append(names, strings.Trim(strings.TrimSpace(name), "'"))
	}
	return names
}

// Spec.refresh takes one RFC 3339 time per refresh target, and the rule that
// guards its keys names every target the operator serves: the facts a
// container runs and the walk. A person cannot ask for a refresh of
// something nothing runs.
func TestTheRefreshMapTakesOneTimePerTarget(t *testing.T) {
	refresh := schemaField(t, librarySchema(t), "schema", "openAPIV3Schema", "properties",
		"spec", "properties", "refresh").(map[string]any)

	values := refresh["additionalProperties"].(map[string]any)
	if values["type"] != "string" || values["format"] != "date-time" {
		t.Errorf("a value reads %+v, want an RFC 3339 string", values)
	}
	rules := refresh["x-kubernetes-validations"].([]any)
	if len(rules) != 1 {
		t.Fatalf("rules = %+v, want the one that guards the keys", rules)
	}
	names := namesInRule(t, rules[0].(map[string]any)["rule"].(string))
	if !slices.Equal(names, refreshVocabulary) {
		t.Errorf("the rule names %v, want %v", names, refreshVocabulary)
	}
	if limit := refresh["maxProperties"]; limit != len(refreshVocabulary) {
		t.Errorf("maxProperties = %v, want the %d targets the rule names",
			limit, len(refreshVocabulary))
	}
}

// The spec the API server admits is the spec the operator reads: a time
// under a fact's name and a time under the walk's name land in one map.
func TestTheOperatorReadsARefreshTheSchemaAdmits(t *testing.T) {
	spec := LibrarySpec{}
	body := `{"refresh":{"credits":"2026-09-03T21:00:00Z","scan":"2026-09-19T14:00:00Z"}}`
	if err := json.Unmarshal([]byte(body), &spec); err != nil {
		t.Fatal(err)
	}

	want := time.Date(2026, 9, 3, 21, 0, 0, 0, time.UTC)
	if at := spec.Refresh[factCredits]; !at.Equal(want) {
		t.Errorf("refresh[%s] = %v, want %v", factCredits, at, want)
	}
	walk := time.Date(2026, 9, 19, 14, 0, 0, 0, time.UTC)
	if at := spec.Refresh[refreshWalk]; !at.Equal(walk) {
		t.Errorf("refresh[%s] = %v, want %v", refreshWalk, at, walk)
	}
}

// rulesOf is the rules one field of the schema carries, as the CRD states
// them.
func rulesOf(t *testing.T, field any) []string {
	t.Helper()
	held, _ := field.(map[string]any)["x-kubernetes-validations"].([]any)
	rules := []string{}
	for _, rule := range held {
		text, _ := rule.(map[string]any)["rule"].(string)
		rules = append(rules, strings.Join(strings.Fields(text), " "))
	}
	return rules
}

// The kind enum and the settings blocks of the CRD are the kinds the operator
// serves, and a kind the schema admits resolves to a settings block in Go.
func TestTheSchemaAdmitsTheKindsTheOperatorServes(t *testing.T) {
	spec := schemaField(t, librarySchema(t), "schema", "openAPIV3Schema", "properties",
		"spec", "properties").(map[string]any)
	kinds, _ := spec["kind"].(map[string]any)["enum"].([]any)

	for _, kind := range kinds {
		name := kind.(string)
		if _, held := spec[name]; !held {
			t.Errorf("the kind %s names no settings block", name)
		}
		if settings := (LibrarySpec{Kind: name, Movies: &LibrarySettings{}, Series: &LibrarySettings{},
			Franchises: &LibraryFranchises{}}).settings(); settings == nil {
			t.Errorf("the operator resolves no settings block for the kind %s", name)
		}
	}
	if len(kinds) != 3 {
		t.Errorf("the enum names %v, want the three kinds the operator serves", kinds)
	}
}

// Every kind names a claim, so the storage block requires it, and it names
// nothing else: a franchises library's checkout is a claim like any other.
func TestTheSchemaTakesAClaimForEveryKind(t *testing.T) {
	storage := schemaField(t, librarySchema(t), "schema", "openAPIV3Schema", "properties",
		"spec", "properties", "storage")

	if required := requiredOf(t, storage); !slices.Equal(required, []string{"claim"}) {
		t.Errorf("storage requires %v, want the claim every kind names", required)
	}
	fields := storage.(map[string]any)["properties"].(map[string]any)
	if len(fields) != 2 || fields["claim"] == nil || fields["root"] == nil {
		t.Errorf("storage holds %v, want the claim and the root alone", slices.Sorted(maps.Keys(fields)))
	}
}

// The art claim is required inside the franchises block, so a franchises
// library that names none is refused at apply.
func TestTheSchemaRequiresTheArtClaimOfAFranchisesLibrary(t *testing.T) {
	franchises := schemaField(t, librarySchema(t), "schema", "openAPIV3Schema", "properties",
		"spec", "properties", "franchises")

	if required := requiredOf(t, franchises); !slices.Equal(required, []string{"art"}) {
		t.Errorf("the franchises block requires %v, want the art block", required)
	}
	art := franchises.(map[string]any)["properties"].(map[string]any)["art"]
	if required := requiredOf(t, art); !slices.Equal(required, []string{"claim"}) {
		t.Errorf("art requires %v, want the claim", required)
	}
}

// requiredOf is the fields one object of the schema requires, in the order it
// names them.
func requiredOf(t *testing.T, field any) []string {
	t.Helper()
	held, _ := field.(map[string]any)["required"].([]any)
	names := []string{}
	for _, name := range held {
		names = append(names, name.(string))
	}
	return names
}

// The kind rule names one clause per settings block, and a franchises library
// names the art claim beside it.
func TestTheSchemaTiesTheKindToItsBlockAndItsArtClaim(t *testing.T) {
	spec := schemaField(t, librarySchema(t), "schema", "openAPIV3Schema", "properties", "spec")

	rules := rulesOf(t, spec)
	for _, want := range []string{
		"has(self.movies) == (self.kind == 'movies') && has(self.series) == (self.kind == 'series') && has(self.franchises) == (self.kind == 'franchises')",
		"self.kind != 'franchises' || (has(self.franchises) && has(self.franchises.art))",
	} {
		if !slices.Contains(rules, want) {
			t.Errorf("the spec rules are %v, want %q among them", rules, want)
		}
	}
}

// The spec the API server admits is the one the operator reads back: a
// franchises library carries a storage claim for the checkout and an art
// claim of its own.
func TestTheOperatorReadsAFranchisesSpecTheSchemaAdmits(t *testing.T) {
	spec := LibrarySpec{}
	body := `{"kind":"franchises","franchises":{"art":{"claim":"franchise-art"}},` +
		`"storage":{"claim":"franchises","root":"/"}}`
	if err := json.Unmarshal([]byte(body), &spec); err != nil {
		t.Fatal(err)
	}

	if spec.Storage.Claim != "franchises" || spec.Franchises.Art.Claim != "franchise-art" {
		t.Errorf("spec = %+v, want the checkout claim beside the art claim", spec)
	}
	if spec.screenClaim() != "franchise-art" {
		t.Errorf("screenClaim = %q, want the art claim", spec.screenClaim())
	}
}

// The template name the API server admits under each worker's block is the
// name the operator reads: a string, and no render block beside it.
func TestTheOperatorReadsAGPUClaimTemplateTheSchemaAdmits(t *testing.T) {
	for _, worker := range factWorkers {
		t.Run(worker.fact, func(t *testing.T) {
			block := schemaField(t, librarySchema(t), "schema", "openAPIV3Schema", "properties",
				"spec", "properties", worker.fact, "properties").(map[string]any)
			field := block["gpuResourceClaimTemplate"].(map[string]any)
			if field["type"] != "string" || field["minLength"] != 1 {
				t.Errorf("gpuResourceClaimTemplate = %+v, want a string that is not empty", field)
			}
			if _, held := block["render"]; held {
				t.Error("the schema admits a render block, which the operator does not read")
			}
			library := &Library{}
			if err := json.Unmarshal([]byte(`{"spec":{"`+worker.fact+`":{"enabled":true,`+
				`"gpuResourceClaimTemplate":"house-gpu"}}}`), library); err != nil {
				t.Fatal(err)
			}
			if got := worker.gpuClaimTemplate(library); got != "house-gpu" {
				t.Errorf("the operator reads %q, want house-gpu", got)
			}
		})
	}
}

// The languages a library may name: BCP-47 tags the schema's own pattern
// admits, in the order the owner wrote them, and the list the operator reads
// back off a spec the API server admitted.
func TestTheSchemaTakesTheLanguagesOfALibrary(t *testing.T) {
	languages := schemaField(t, librarySchema(t), "schema", "openAPIV3Schema", "properties",
		"spec", "properties", "languages").(map[string]any)

	if languages["type"] != "array" || languages["maxItems"] != 8 {
		t.Errorf("languages reads %+v, want an array of at most 8 tags", languages)
	}
	pattern := languages["items"].(map[string]any)["pattern"].(string)
	tags := regexp.MustCompile(pattern)
	cases := []struct {
		tag   string
		admit bool
	}{
		{tag: "en", admit: true},
		{tag: "en-US", admit: true},
		{tag: "pt-BR", admit: true},
		{tag: "zh-Hant", admit: true},
		{tag: "fil", admit: true},
		{tag: "e"},
		{tag: "en_US"},
		{tag: "english (US)"},
		{tag: ""},
	}
	for _, one := range cases {
		t.Run(one.tag, func(t *testing.T) {
			if got := tags.MatchString(one.tag); got != one.admit {
				t.Errorf("the pattern admits %q = %v, want %v", one.tag, got, one.admit)
			}
		})
	}
}

// The spec the API server admits is the spec the operator reads: the
// languages in the order the owner named them.
func TestTheOperatorReadsTheLanguagesTheSchemaAdmits(t *testing.T) {
	spec := LibrarySpec{}
	if err := json.Unmarshal([]byte(`{"languages":["ko","en-US"]}`), &spec); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(spec.Languages, []string{"ko", "en-US"}) {
		t.Errorf("languages = %v, want the order the owner named", spec.Languages)
	}
}

// The API server drops every status field the schema does not name, and
// it answers a write that then changes nothing with no new version. The
// operator compares the status it derives against the stored one before
// it writes, so a field the schema drops makes the two differ on every
// pass and costs one empty write per pass. So every field the operator
// derives, with every optional field set, must be one the schema keeps.
func TestTheSchemaKeepsEveryStatusFieldTheOperatorWrites(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	seen := scanning()
	seen.report = &libraryReport{Titles: 1, LastWalk: now, LastChange: now,
		Gaps: map[string]int{"trickplay": 2},
		Runs: []libraryRun{{Worker: workerScan, Job: "movies-walk-1", Started: now, Finished: now,
			Unidentified: 1, Removed: 1, Failure: "a failure", Actor: "agent", Version: 7}}}
	seen.resolved = []librarySource{{Name: "tmdb", Block: "tmdb", Ready: true, Reason: "Reachable"}}
	status := deriveLibraryStatus(studioMovies(), seen, now)
	body, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var written map[string]any
	if err := json.Unmarshal(body, &written); err != nil {
		t.Fatal(err)
	}
	schema := schemaField(t, librarySchema(t), "schema", "openAPIV3Schema", "properties",
		"status").(map[string]any)

	for _, path := range fieldsTheSchemaDrops(schema, written, "status") {
		t.Errorf("the schema drops %s", path)
	}
}

// The paths in a written value that the schema has no property for. A map
// the schema types with additionalProperties keeps every key.
func fieldsTheSchemaDrops(schema map[string]any, written any, path string) []string {
	var dropped []string
	switch value := written.(type) {
	case map[string]any:
		if _, open := schema["additionalProperties"]; open {
			return nil
		}
		properties, _ := schema["properties"].(map[string]any)
		for key, inner := range value {
			field, known := properties[key].(map[string]any)
			if !known {
				dropped = append(dropped, path+"."+key)
				continue
			}
			dropped = append(dropped, fieldsTheSchemaDrops(field, inner, path+"."+key)...)
		}
	case []any:
		items, _ := schema["items"].(map[string]any)
		for _, inner := range value {
			dropped = append(dropped, fieldsTheSchemaDrops(items, inner, path+"[]")...)
		}
	}
	return dropped
}

// A Library's name starts the name of every Job it becomes, and the pod
// of an Indexed Job takes the hostname <job>-<index>, which must fit a
// 63-character DNS label. The CRD caps the name, so the longest name it
// admits still gives every Job a valid name and every worker pod a valid
// hostname, at the highest index the parallelism cap allows and a
// creation time far in the future.
func TestTheLongestLibraryNameLeavesEveryJobNameValid(t *testing.T) {
	schema := librarySchema(t)
	rules := schemaField(t, schema, "schema", "openAPIV3Schema", "x-kubernetes-validations").([]any)
	rule := rules[0].(map[string]any)["rule"]
	if want := fmt.Sprintf("size(self.metadata.name) <= %d", maxLibraryNameLength); rule != want {
		t.Fatalf("the CRD's first rule is %q, want %q", rule, want)
	}
	name := strings.Repeat("n", maxLibraryNameLength)
	far := time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)

	names := []string{cleanupJobName(name), libraryJobName(name, jobModeWalk, far),
		libraryJobName(name, jobModeGaps, far)}
	for _, worker := range factWorkers {
		highest := schemaField(t, schema, "schema", "openAPIV3Schema", "properties", "spec",
			"properties", worker.fact, "properties", "parallelism", "maximum").(int)
		names = append(names, fmt.Sprintf("%s-%d", libraryJobName(name, worker.fact, far), highest-1))
	}
	for _, one := range names {
		if len(one) > 63 {
			t.Errorf("%s is %d characters, past the 63 a DNS label holds", one, len(one))
		}
	}
}
