package main

// What these tests read: the claim a worker's pod takes from the
// ResourceClaimTemplate a Library names, the condition that reports a
// template that does not exist, the worker that waits for it, and the
// templates an earlier release of the operator created, which a pass
// deletes.

import (
	"reflect"
	"testing"
)

// A template the cluster owner writes in the Library's namespace. It carries
// no label and no owner reference of the operator.
func ownersTemplate(name string) *ResourceClaimTemplate {
	return &ResourceClaimTemplate{Metadata: ObjectMeta{Name: name, Namespace: "house", ResourceVersion: "1"}}
}

// A movies Library whose trickplay worker names the template given.
func trickplayOnTemplate(template string) *Library {
	library := studioMovies()
	library.Spec.Trickplay = LibraryTrickplay{Enabled: true, GPUResourceClaimTemplate: template}
	return library
}

// Each worker's pod claims from the template its block names, under the one
// claim name its container repeats, and claims nothing where the block names
// none.
func TestEachWorkerClaimsFromTheTemplateItsBlockNames(t *testing.T) {
	cases := []struct {
		name     string
		worker   factWorker
		template string
	}{
		{name: "trickplay on a template", worker: trickplayWorker, template: "trickplay-gpu"},
		{name: "trickplay on the CPU", worker: trickplayWorker},
		{name: "appearances on a template", worker: appearancesWorker, template: "appearances-gpu"},
		{name: "appearances on the CPU", worker: appearancesWorker},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			library := studioMovies()
			library.Spec.Trickplay = LibraryTrickplay{Enabled: true, GPUResourceClaimTemplate: test.template}
			library.Spec.Appearances = LibraryAppearances{Enabled: true, GPUResourceClaimTemplate: test.template}

			pod := buildFactWorkerJob(library, test.worker, jobImages{}, "", "movies-walk-1", testNow).Spec.Template.Spec

			var want []PodResourceClaim
			var wantContainer []ResourceClaim
			if test.template != "" {
				want = []PodResourceClaim{{Name: gpuClaimName, ResourceClaimTemplateName: test.template}}
				wantContainer = []ResourceClaim{{Name: gpuClaimName}}
			}
			if !reflect.DeepEqual(pod.ResourceClaims, want) {
				t.Errorf("resourceClaims = %+v, want %+v", pod.ResourceClaims, want)
			}
			if !reflect.DeepEqual(pod.Containers[0].Resources.Claims, wantContainer) {
				t.Errorf("the container claims %+v, want %+v", pod.Containers[0].Resources.Claims, wantContainer)
			}
		})
	}
}

// A worker whose template does not exist starts no Job, because its pods would
// stay Pending until the Job's deadline. The pass that reads the template
// starts the Job.
func TestAWorkerWaitsForTheTemplateItNames(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	library := trickplayOnTemplate("trickplay-gpu")
	operator := testOperator(t, cluster)

	runWorkers(t, operator, library)
	if created := cluster.heldJobs(); len(created) != 0 {
		t.Fatalf("jobs = %+v, want none while the template is missing", created)
	}

	cluster.holdClaimTemplate(ownersTemplate("trickplay-gpu"))
	runWorkers(t, operator, library)

	created := cluster.heldJobs()
	if len(created) != 1 || created[0].Spec.Template.Spec.ResourceClaims[0].ResourceClaimTemplateName != "trickplay-gpu" {
		t.Fatalf("jobs = %+v, want one trickplay worker on trickplay-gpu", created)
	}
}

// One worker step of a pass: read the templates, then start the workers.
func runWorkers(t *testing.T, operator *operator, library *Library) {
	t.Helper()
	templates, err := operator.readGPUClaimTemplates(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	if err := operator.runFactWorkers(t.Context(), library, listedReport(3), nil, templates, testNow); err != nil {
		t.Fatal(err)
	}
}

// The GPUClaimTemplates condition reports the templates the enabled workers
// name, and a Library whose enabled workers name none carries no such
// condition.
func TestTheConditionReportsTheTemplatesTheWorkersName(t *testing.T) {
	cases := []struct {
		name        string
		templates   gpuClaimTemplates
		wantStatus  ConditionStatus
		wantReason  string
		wantMessage string
	}{
		{name: "every template exists",
			templates:   gpuClaimTemplates{{fact: factTrickplay, name: "trickplay-gpu", found: true}},
			wantStatus:  ConditionTrue,
			wantReason:  reasonClaimTemplatesFound,
			wantMessage: "every ResourceClaimTemplate the workers name exists"},
		{name: "one template is missing",
			templates: gpuClaimTemplates{
				{fact: factTrickplay, name: "trickplay-gpu", found: true},
				{fact: factAppearances, name: "appearances-gpu"},
			},
			wantStatus: ConditionFalse,
			wantReason: reasonClaimTemplateNotFound,
			wantMessage: "the ResourceClaimTemplate appearances-gpu that spec.appearances.gpuResourceClaimTemplate " +
				"names does not exist in namespace house, so the appearances worker does not start"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			seen := scanning()
			seen.gpuTemplates = test.templates

			condition := conditionOf(t, deriveLibraryStatus(studioMovies(), seen, testNow), conditionGPUClaimTemplates)

			if condition.Status != test.wantStatus || condition.Reason != test.wantReason ||
				condition.Message != test.wantMessage {
				t.Errorf("condition = %s %s %q, want %s %s %q", condition.Status, condition.Reason,
					condition.Message, test.wantStatus, test.wantReason, test.wantMessage)
			}
		})
	}
}

// A Library that stops naming a template drops the condition, because there
// is nothing left to report.
func TestALibraryThatNamesNoTemplateCarriesNoCondition(t *testing.T) {
	library := studioMovies()
	library.Status.Conditions = []Condition{{Type: conditionGPUClaimTemplates, Status: ConditionFalse}}

	status := deriveLibraryStatus(library, scanning(), testNow)

	for _, condition := range status.Conditions {
		if condition.Type == conditionGPUClaimTemplates {
			t.Errorf("conditions = %+v, want no %s condition", status.Conditions, conditionGPUClaimTemplates)
		}
	}
}

// A disabled worker starts no Job, so the template it names is not read and
// not reported.
func TestADisabledWorkersTemplateIsNotRead(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	library := trickplayOnTemplate("trickplay-gpu")
	library.Spec.Trickplay.Enabled = false
	operator := testOperator(t, cluster)

	templates, err := operator.readGPUClaimTemplates(t.Context(), library)

	if err != nil || len(templates) != 0 {
		t.Errorf("templates = %+v, %v; want none", templates, err)
	}
}

// A pass deletes the templates an earlier release of the operator created for
// the Library, which name it as their owner, and leaves a template a person
// wrote at the same name.
func TestAPassDeletesOnlyTheTemplatesTheOperatorCreated(t *testing.T) {
	library := studioMovies()
	cases := []struct {
		name     string
		template *ResourceClaimTemplate
		deleted  bool
	}{
		{name: "owned by the Library", deleted: true, template: &ResourceClaimTemplate{Metadata: ObjectMeta{
			Name: "movies-trickplay", Namespace: "house", Labels: libraryLabels("movies"),
			OwnerReferences: []OwnerReference{libraryOwner(library)}}}},
		{name: "written by a person", template: &ResourceClaimTemplate{Metadata: ObjectMeta{
			Name: "movies-trickplay", Namespace: "house", Labels: libraryLabels("movies")}}},
		{name: "owned by an earlier Library of the same name", template: &ResourceClaimTemplate{Metadata: ObjectMeta{
			Name: "movies-trickplay", Namespace: "house", Labels: libraryLabels("movies"),
			OwnerReferences: []OwnerReference{{APIVersion: libraryAPIVersion, Kind: "Library", Name: "movies",
				UID: "another-uid", Controller: true}}}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cluster := newFakeCluster()
			boundHouse(cluster)
			cluster.holdClaimTemplate(test.template)
			operator := testOperator(t, cluster)

			if err := operator.retireOwnedClaimTemplates(t.Context(), library); err != nil {
				t.Fatal(err)
			}

			if gone := cluster.heldClaimTemplate("house", "movies-trickplay") == nil; gone != test.deleted {
				t.Errorf("deleted = %v, want %v", gone, test.deleted)
			}
		})
	}
}
