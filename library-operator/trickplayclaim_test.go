package main

// What these tests read: the ResourceClaimTemplate one Library's render block
// becomes, and what a pass does with it when the block changes or goes.

import (
	"testing"
)

// A movies Library that names one render node.
func libraryWithRender(class, selector string) *Library {
	library := studioMovies()
	library.Spec.Trickplay.Enabled = true
	library.Spec.Trickplay.Render = &TrickplayDevice{Class: class, Selector: selector}
	return library
}

// The template asks for exactly one device of the class, and it carries a CEL
// selector only where the Library states one.
func TestTheRenderTemplateAsksForOneDeviceOfTheClass(t *testing.T) {
	cases := []struct {
		name     string
		selector string
		want     []DeviceSelector
	}{
		{name: "a class on its own"},
		{name: "a class and a selector", selector: `device.attributes["pci"].vendor == "8086"`,
			want: []DeviceSelector{{CEL: &CELDeviceSelector{
				Expression: `device.attributes["pci"].vendor == "8086"`}}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			template := buildTrickplayTemplate(libraryWithRender("gpu.liken.sh", test.selector))

			if template.Metadata.Name != "movies-trickplay" || template.Metadata.Namespace != "house" {
				t.Errorf("the template is %s/%s, want house/movies-trickplay",
					template.Metadata.Namespace, template.Metadata.Name)
			}
			requests := template.Spec.Spec.Devices.Requests
			if len(requests) != 1 || requests[0].Name != renderRequestName {
				t.Fatalf("requests = %+v, want the one named %s", requests, renderRequestName)
			}
			exactly := requests[0].Exactly
			if exactly.DeviceClassName != "gpu.liken.sh" ||
				exactly.AllocationMode != "ExactCount" || exactly.Count != 1 {
				t.Errorf("the request is %+v, want one device of gpu.liken.sh", exactly)
			}
			if len(exactly.Selectors) != len(test.want) {
				t.Fatalf("selectors = %+v, want %+v", exactly.Selectors, test.want)
			}
			if len(test.want) == 1 && exactly.Selectors[0].CEL.Expression != test.want[0].CEL.Expression {
				t.Errorf("selector = %q, want %q",
					exactly.Selectors[0].CEL.Expression, test.want[0].CEL.Expression)
			}
		})
	}
}

// The Library owns the template, so the garbage collector takes it with the
// Library.
func TestTheRenderTemplateIsOwnedByTheLibrary(t *testing.T) {
	library := libraryWithRender("gpu.liken.sh", "")

	template := buildTrickplayTemplate(library)

	want := []OwnerReference{libraryOwner(library)}
	if len(template.Metadata.OwnerReferences) != 1 || template.Metadata.OwnerReferences[0] != want[0] {
		t.Errorf("ownerReferences = %+v, want %+v", template.Metadata.OwnerReferences, want)
	}
}

// A pass creates the template the render block names, writes it again when the
// block changes, and deletes it when the Library names none.
func TestTheRenderTemplateFollowsTheRenderBlock(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	operator := testOperator(t, cluster)

	if err := operator.standTrickplayTemplate(t.Context(), libraryWithRender("gpu.liken.sh", "")); err != nil {
		t.Fatal(err)
	}
	stood := cluster.heldClaimTemplate("house", "movies-trickplay")
	if stood == nil {
		t.Fatal("the pass stood no template")
	}

	if err := operator.standTrickplayTemplate(t.Context(), libraryWithRender("intel.liken.sh", "")); err != nil {
		t.Fatal(err)
	}
	rewritten := cluster.heldClaimTemplate("house", "movies-trickplay")
	if rewritten == nil || rewritten.Spec.Spec.Devices.Requests[0].Exactly.DeviceClassName != "intel.liken.sh" {
		t.Errorf("the template holds %+v, want the class the Library now names", rewritten)
	}

	if err := operator.standTrickplayTemplate(t.Context(), studioMovies()); err != nil {
		t.Fatal(err)
	}
	if left := cluster.heldClaimTemplate("house", "movies-trickplay"); left != nil {
		t.Errorf("the template stands at %+v, want it gone with the render block", left)
	}
}

// A Library that never named a render node leaves the API server alone.
func TestALibraryWithNoRenderBlockWritesNoTemplate(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	operator := testOperator(t, cluster)

	if err := operator.standTrickplayTemplate(t.Context(), studioMovies()); err != nil {
		t.Fatal(err)
	}

	if got := cluster.countRequests("POST", "resourceclaimtemplates"); got != 0 {
		t.Errorf("the pass wrote %d templates, want none", got)
	}
	if got := cluster.countRequests("DELETE", "resourceclaimtemplates"); got != 0 {
		t.Errorf("the pass deleted %d templates, want none", got)
	}
}
