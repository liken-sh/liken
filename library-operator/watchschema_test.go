package main

// What these tests read: the Watch CRD as the cluster applies it,
// against the operator that writes it. The two hold one vocabulary, so
// a Watch the API server admits is a Watch this operator can serve, and
// every status field the operator writes is a field the schema names.

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

// The DNS-1123 label a Person name and a Library name take, which is
// what the schema states for both.
const labelPattern = `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`

// The schema of the one version this CRD serves.
func watchSchema(t *testing.T) map[string]any {
	t.Helper()
	body, err := os.ReadFile("deploy/watches-crd.yaml")
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

// The whole document, for the names and the scope the API server
// registers the kind under.
func watchDefinition(t *testing.T) map[string]any {
	t.Helper()
	body, err := os.ReadFile("deploy/watches-crd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	document := map[string]any{}
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

// A Watch is namespaced, in the namespace of the Library that holds the
// item, and the operator writes its status through the subresource.
func TestTheWatchIsNamespacedWithAStatusSubresource(t *testing.T) {
	document := watchDefinition(t)
	spec := document["spec"].(map[string]any)

	if document["metadata"].(map[string]any)["name"] != "watches.library.liken.sh" {
		t.Errorf("name = %v, want watches.library.liken.sh", document["metadata"])
	}
	if spec["group"] != "library.liken.sh" || spec["scope"] != "Namespaced" {
		t.Errorf("group and scope = %v, %v, want library.liken.sh and Namespaced", spec["group"], spec["scope"])
	}
	names := spec["names"].(map[string]any)
	if names["kind"] != "Watch" || names["plural"] != "watches" {
		t.Errorf("names = %+v, want the Watch kind under the watches plural", names)
	}
	version := watchSchema(t)
	if version["name"] != "v1alpha1" {
		t.Errorf("version = %v, want v1alpha1", version["name"])
	}
	if schemaField(t, version, "subresources", "status") == nil {
		t.Error("the version serves no status subresource, so a person could write the projection")
	}
}

// The people are a set of Person names, and a name is a DNS-1123
// label, because that is what a Person is named by.
func TestTheWatchTakesASetOfPeople(t *testing.T) {
	people := schemaField(t, watchSchema(t), "schema", "openAPIV3Schema", "properties",
		"spec", "properties", "people").(map[string]any)

	if people["type"] != "array" || people["x-kubernetes-list-type"] != "set" {
		t.Errorf("people = %+v, want a set", people)
	}
	if people["minItems"] != 1 {
		t.Errorf("minItems = %v, want 1", people["minItems"])
	}
	item := people["items"].(map[string]any)
	if item["type"] != "string" || item["pattern"] != labelPattern || item["maxLength"] != 63 {
		t.Errorf("a name reads %+v, want a DNS-1123 label", item)
	}
}

// Progress belongs to a set of people on an item, so a Watch that names
// no people, or an item without both halves of it, is refused at apply.
func TestTheWatchRequiresItsPeopleAndItsItem(t *testing.T) {
	spec := schemaField(t, watchSchema(t), "schema", "openAPIV3Schema", "properties", "spec")

	if required := requiredOf(t, spec); !slices.Equal(required, []string{"people", "item"}) {
		t.Errorf("the spec requires %v, want the people and the item", required)
	}
	item := spec.(map[string]any)["properties"].(map[string]any)["item"]
	if required := requiredOf(t, item); !slices.Equal(required, []string{"library", "slug"}) {
		t.Errorf("the item requires %v, want the library and the slug", required)
	}
}

// The status the schema names is the status the operator writes, so a
// projection the store publishes reaches the object whole.
func TestTheWatchStatusIsWhatTheOperatorWrites(t *testing.T) {
	status := schemaField(t, watchSchema(t), "schema", "openAPIV3Schema", "properties",
		"status", "properties").(map[string]any)

	written := map[string]any{}
	body, err := json.Marshal(WatchStatus{
		Play: "den-tv-b2k9x", Item: 1, Position: "0:12:00", Duration: "1:40:00",
		Season: 3, Episode: 5, Ended: true, LastRecorded: "2026-09-06T21:14:02Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &written); err != nil {
		t.Fatal(err)
	}
	for field := range written {
		if status[field] == nil {
			t.Errorf("the schema names no status.%s, which the operator writes", field)
		}
	}
	for field := range status {
		if _, held := written[field]; !held {
			t.Errorf("the schema names status.%s, which the operator never writes", field)
		}
	}
}

// The columns a person reads: who is watching, what they are watching,
// where the playhead is, and when the store last wrote a row.
func TestTheWatchPrintsWhoAndWhereAndWhen(t *testing.T) {
	columns := schemaField(t, watchSchema(t), "additionalPrinterColumns").([]any)

	want := []string{".spec.people", ".spec.item.slug", ".status.position",
		".status.lastRecorded", ".metadata.creationTimestamp"}
	paths := []string{}
	for _, column := range columns {
		paths = append(paths, column.(map[string]any)["jsonPath"].(string))
	}
	if !slices.Equal(paths, want) {
		t.Errorf("the columns read %v, want %v", paths, want)
	}
}

// The Watch the API server admits is the Watch the operator reads back.
func TestTheOperatorReadsAWatchTheSchemaAdmits(t *testing.T) {
	watch := Watch{}
	body := `{"metadata":{"name":"the-office-with-the-girls","namespace":"house"},` +
		`"spec":{"people":["chris","thora"],"item":{"library":"series","slug":"the-office"}}}`
	if err := json.Unmarshal([]byte(body), &watch); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(watch.Spec.People, []string{"chris", "thora"}) {
		t.Errorf("people = %v, want the two the spec names", watch.Spec.People)
	}
	if watch.Spec.Item.Library != "series" || watch.Spec.Item.Slug != "the-office" {
		t.Errorf("item = %+v, want the series library and the office", watch.Spec.Item)
	}
}

// The base applies the CRD, so a cluster that takes this operator takes
// the Watch with it.
func TestTheBaseAppliesTheWatchCRD(t *testing.T) {
	body, err := os.ReadFile("deploy/kustomization.yaml")
	if err != nil {
		t.Fatal(err)
	}
	document := map[string]any{}
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}

	resources := []string{}
	for _, resource := range document["resources"].([]any) {
		resources = append(resources, resource.(string))
	}
	if !slices.Contains(resources, "watches-crd.yaml") {
		t.Errorf("the base takes %v, want watches-crd.yaml among them", resources)
	}
}
