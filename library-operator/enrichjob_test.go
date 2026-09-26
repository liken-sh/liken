package main

// What these tests read: the one Job a Library becomes, the containers in its
// pod, the facts and the needs each phase names, the keys the phases
// receive, and the volumes they mount.

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// the provider a Library's sources resolve to, as the pass would have
// checked it.
func readyProvider(name, namespace string, facts ...string) *MetadataProvider {
	provider := seedProvider(newFakeCluster(), name, namespace, facts...)
	provider.Status.Conditions = []Condition{
		{Type: conditionReady, Status: ConditionTrue, Reason: reasonReachable},
	}
	return provider
}

// The images a test Job runs.
var testJobImages = jobImages{operator: testScannerImage, ffmpeg: testFFmpegImage, corrosion: testCorrosionImage}

// The walk Job of one Library, built the way a pass builds it, with every
// phase its sources serve. Every provider named here is a source of the
// Library, in the order given. A path makes it a walk of that folder.
func testEnrichJob(library *Library, path string, providers ...*MetadataProvider) *Job {
	set := providerSet{}
	for _, provider := range providers {
		set[libraryKey(provider.Metadata.Namespace, provider.Metadata.Name)] = provider
		library.Spec.Sources = append(library.Spec.Sources, provider.Metadata.Name)
	}
	plan := libraryJob{mode: jobModeWalk, phases: servedPhases(library, set)}
	if path != "" {
		plan.paths = []string{path}
	}
	return buildLibraryJob(library, set, nil, plan, testJobImages, testNow)
}

// The names of the regular containers of a Job, in the pod's order.
func jobContainerNames(job *Job) []string {
	names := []string{}
	for _, container := range job.Spec.Template.Spec.Containers {
		names = append(names, container.Name)
	}
	return names
}

// The phase containers of a Job: every regular container but the scan and
// the close container.
func phaseContainers(job *Job) []Container {
	return slices.DeleteFunc(slices.Clone(job.Spec.Template.Spec.Containers), func(container Container) bool {
		return container.Name == scannerContainer || container.Name == closeMode
	})
}

// One container of a Job by name, and nil where the pod holds none.
func jobContainer(job *Job, name string) *Container {
	for at, container := range job.Spec.Template.Spec.Containers {
		if container.Name == name {
			return &job.Spec.Template.Spec.Containers[at]
		}
	}
	return nil
}

// The agent is the one init container. The walk, every phase the sources
// serve, and the close container are regular containers, so all of them
// start together.
func TestAWalkJobRunsEveryPhaseBesideTheWalk(t *testing.T) {
	job := testEnrichJob(studioMovies(), "", readyProvider("tmdb", "house"),
		providerOfBlock("intros", providerBlockTheIntroDB))

	spec := job.Spec.Template.Spec
	if len(spec.InitContainers) != 1 || spec.InitContainers[0].Name != catalogContainer {
		t.Fatalf("initContainers = %+v, want the catalog agent alone", spec.InitContainers)
	}
	want := []string{scannerContainer, factProbe, arrivalContainerName, factIdentity, nfoContainerName,
		artContainerName, trailerContainerName, marksContainerName, contributorsContainerName, closeMode}
	if got := jobContainerNames(job); !slices.Equal(got, want) {
		t.Errorf("containers = %v, want %v", got, want)
	}
}

// A Job that fills gaps runs no walk, and only the phases it names.
func TestAGapJobRunsNoWalk(t *testing.T) {
	library := studioMovies()
	plan := libraryJob{mode: jobModeGaps, phases: servedPhases(library, providerSet{})[:1]}

	job := buildLibraryJob(library, providerSet{}, nil, plan, testJobImages, testNow)

	if got := jobContainerNames(job); !slices.Equal(got, []string{factProbe, closeMode}) {
		t.Errorf("containers = %v, want the probe and the close container", got)
	}
	if job.Metadata.Labels[workerLabelKey] != jobModeGaps || !strings.HasPrefix(job.Metadata.Name, "movies-gaps-") {
		t.Errorf("metadata = %+v, want the gaps mode in the label and the name", job.Metadata)
	}
	if got := containerEnvironment(*jobContainer(job, factProbe))[libraryPhaseNeedsVariable]; got != "" {
		t.Errorf("the probe waits for %q, want nothing in a Job with no walk", got)
	}
}

// Each phase runs the facts role and names its facts, and the close
// container runs its own role.
func TestEachPhaseNamesItsFacts(t *testing.T) {
	job := testEnrichJob(studioMovies(), "", readyProvider("tmdb", "house", factIdentity))

	for _, container := range phaseContainers(job) {
		if len(container.Command) != 2 || container.Command[1] != factsMode {
			t.Errorf("%s runs %v, want the binary in the facts role", container.Name, container.Command)
		}
	}
	for name, want := range map[string]string{factProbe: factProbe, arrivalContainerName: factArrival,
		factIdentity: factIdentity} {
		if got := containerEnvironment(*jobContainer(job, name))[libraryFactsVariable]; got != want {
			t.Errorf("%s reads %s = %q, want %q", name, libraryFactsVariable, got, want)
		}
	}
	closing := jobContainer(job, closeMode)
	if closing == nil || closing.Command[1] != closeMode {
		t.Fatalf("close = %+v, want the binary in the close role", closing)
	}
	if got := containerEnvironment(*closing)[libraryFactsVariable]; got != "" {
		t.Errorf("close reads %s = %q, want none", libraryFactsVariable, got)
	}
}

// Each phase waits for the phases whose rows open its gap, through the whole
// table, and the close container waits for every container.
func TestEachContainerWaitsForThePhasesBeforeIt(t *testing.T) {
	job := testEnrichJob(studioMovies(), "", readyProvider("tmdb", "house"),
		providerOfBlock("intros", providerBlockTheIntroDB))

	cases := map[string]string{
		factProbe:                 "scan",
		factIdentity:              "scan",
		nfoContainerName:          "scan,identity",
		marksContainerName:        "scan,probe,identity",
		contributorsContainerName: "scan,identity,nfo",
		closeMode:                 "scan,probe,arrival,identity,nfo,art,trailer,marks,contributors",
	}
	for name, want := range cases {
		if got := containerEnvironment(*jobContainer(job, name))[libraryPhaseNeedsVariable]; got != want {
			t.Errorf("%s waits for %q, want %q", name, got, want)
		}
	}
}

// A phase whose direct need is absent from the Job waits for what that need
// waited for.
func TestAPhaseInheritsTheNeedsOfAnAbsentPhase(t *testing.T) {
	if got := phaseNeedsOf(nfoContainerName, []string{scanPhase, nfoContainerName}); !slices.Equal(got, []string{scanPhase}) {
		t.Errorf("needs = %v, want the walk the absent identity phase waited for", got)
	}
}

// The scan mounts the storage read-only, so a scanner a person supplies never
// writes the media. The phases mount it read-write, because they write beside
// the media. Every container but the agent mounts the phases volume, and the
// agent runs on the Library's one catalog claim.
func TestTheJobMountsTheVolumes(t *testing.T) {
	job := testEnrichJob(studioMovies(), "", readyProvider("tmdb", "house", factIdentity))

	for _, container := range job.Spec.Template.Spec.Containers {
		mounts := map[string]VolumeMount{}
		for _, mount := range container.VolumeMounts {
			mounts[mount.Name] = mount
		}
		library, phases := mounts[libraryVolumeName], mounts[phasesVolumeName]
		if library.MountPath != libraryMountPath || library.ReadOnly != (container.Name == scannerContainer) {
			t.Errorf("%s mounts the library as %+v", container.Name, library)
		}
		if phases.MountPath != phasesMountPath {
			t.Errorf("%s mounts the phases volume as %+v", container.Name, phases)
		}
	}
	volumes := map[string]Volume{}
	for _, volume := range job.Spec.Template.Spec.Volumes {
		volumes[volume.Name] = volume
	}
	if claim := volumes[catalogVolumeName].PersistentVolumeClaim; claim == nil || claim.ClaimName != "movies-catalog" {
		t.Errorf("catalog volume = %+v, want the Library's one catalog claim", volumes[catalogVolumeName])
	}
	if claim := volumes[libraryVolumeName].PersistentVolumeClaim; claim == nil || claim.ClaimName != "movies" || claim.ReadOnly {
		t.Errorf("library volume = %+v, want the Library's claim, read-write at the volume", volumes[libraryVolumeName])
	}
	if volumes[phasesVolumeName].EmptyDir == nil {
		t.Errorf("phases volume = %+v, want an emptyDir", volumes[phasesVolumeName])
	}
}

// A franchises library's phases write into the art claim, because the art is
// the file set a phase writes for this kind and the storage claim holds the
// checkout. The scan mounts both.
func TestTheJobOfAFranchisesLibraryWritesIntoTheArtClaim(t *testing.T) {
	job := testEnrichJob(studioFranchises(), "")

	probe := jobContainer(job, factProbe)
	if probe.VolumeMounts[0].Name != artVolumeName || probe.VolumeMounts[0].MountPath != libraryMountPath {
		t.Errorf("probe mounts %+v, want the art claim at the library mount", probe.VolumeMounts)
	}
	scan := jobContainer(job, scannerContainer)
	names := []string{}
	for _, mount := range scan.VolumeMounts {
		names = append(names, mount.Name)
	}
	if !slices.Equal(names, []string{libraryVolumeName, artVolumeName, phasesVolumeName}) {
		t.Errorf("scan mounts %v, want the storage, the art, and the phases", names)
	}
}

// A franchises library's phases write only into the art claim, so its
// storage claim is read-only at the volume, which a git checkout claim
// requires. The phases of every other kind write beside the media, so their
// storage claim is read-write at the volume.
func TestTheStorageVolumeIsReadOnlyWhereThePhasesWriteElsewhere(t *testing.T) {
	series := studioMovies()
	series.Spec.Kind = libraryKindSeries
	cases := []struct {
		name     string
		library  *Library
		readOnly bool
	}{
		{name: "movies", library: studioMovies()},
		{name: "series", library: series},
		{name: "franchises", library: studioFranchises(), readOnly: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			job := testEnrichJob(one.library, "")

			for _, volume := range job.Spec.Template.Spec.Volumes {
				if volume.Name == libraryVolumeName && volume.PersistentVolumeClaim.ReadOnly != one.readOnly {
					t.Errorf("library volume = %+v, want read-only %v", volume.PersistentVolumeClaim, one.readOnly)
				}
			}
		})
	}
}

// Every container of a franchises Job that mounts the storage claim mounts
// it read-only, to match the volume.
func TestEveryFranchisesMountOfTheStorageIsReadOnly(t *testing.T) {
	job := testEnrichJob(studioFranchises(), "")

	for _, container := range job.Spec.Template.Spec.Containers {
		for _, mount := range container.VolumeMounts {
			if mount.Name == libraryVolumeName && !mount.ReadOnly {
				t.Errorf("%s mounts the storage as %+v, want read-only", container.Name, mount)
			}
		}
	}
}

// The key reaches every phase through a secretKeyRef, so no container reads
// the API server, and the agent carries none.
func TestEveryPhaseCarriesEveryProviderKey(t *testing.T) {
	job := testEnrichJob(studioMovies(), "",
		readyProvider("tmdb", "house", factIdentity),
		providerOfBlock("omdb", providerBlockOMDb))

	for _, container := range phaseContainers(job) {
		keys := map[string]*SecretKeySelector{}
		for _, variable := range container.Env {
			if variable.ValueFrom != nil && variable.ValueFrom.SecretKeyRef != nil {
				keys[variable.Name] = variable.ValueFrom.SecretKeyRef
			}
		}
		if reference := keys[tmdbTokenVariable]; reference == nil || reference.Name != "tmdb-key" || reference.Key != "token" {
			t.Errorf("%s reads %+v for TMDb, want the account's own Secret and key", container.Name, reference)
		}
		if reference := keys[providerTokenVariable(providerBlockOMDb)]; reference == nil || reference.Name != "omdb-key" {
			t.Errorf("%s reads %+v for OMDb, want the account's own Secret", container.Name, reference)
		}
	}
	for _, variable := range job.Spec.Template.Spec.InitContainers[0].Env {
		if variable.ValueFrom != nil && variable.ValueFrom.SecretKeyRef != nil {
			t.Errorf("the catalog agent reads %s, want no provider key", variable.Name)
		}
	}
}

// A Library whose sources name no ready provider still runs the probe and
// the arrival fact, the two that ask no provider.
func TestAJobWithoutAProviderRunsTheProviderFreePhasesAlone(t *testing.T) {
	job := testEnrichJob(studioMovies(), "")

	want := []string{scannerContainer, factProbe, arrivalContainerName, closeMode}
	if got := jobContainerNames(job); !slices.Equal(got, want) {
		t.Errorf("containers = %v, want %v", got, want)
	}
}

// Every container learns its Library, its folders, and the Job it runs from
// its environment. The scan reads one folder from SCAN_PATH as well, so a
// scanner image built before the list rescans it.
func TestTheJobCarriesItsEnvironment(t *testing.T) {
	cases := []struct {
		name, path, list string
	}{
		{name: "the whole library"},
		{name: "one folder", path: "/library/movies/Arrival (2016)", list: `["/library/movies/Arrival (2016)"]`},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			job := testEnrichJob(studioMovies(), one.path, readyProvider("tmdb", "house", factIdentity))

			for _, container := range job.Spec.Template.Spec.Containers {
				got := containerEnvironment(container)
				if got[libraryNameVariable] != "movies" || got[libraryKindVariable] != libraryKindMovies {
					t.Errorf("%s reads %v, want the Library it serves", container.Name, got)
				}
				if got[scanPathsVariable] != one.list {
					t.Errorf("%s reads %s = %q, want %q", container.Name, scanPathsVariable, got[scanPathsVariable], one.list)
				}
				if got[libraryPhasesVariable] != phasesMountPath {
					t.Errorf("%s reads %s = %q", container.Name, libraryPhasesVariable, got[libraryPhasesVariable])
				}
				if !readsTheJobName(container) {
					t.Errorf("%s reads no %s", container.Name, jobNameVariable)
				}
			}
			if got := containerEnvironment(*jobContainer(job, scannerContainer))[scanPathVariable]; got != one.path {
				t.Errorf("scan reads %s = %q, want %q", scanPathVariable, got, one.path)
			}
		})
	}
}

// A walk of several folders names none in SCAN_PATH, which a scanner image
// built before the list reads as a full walk, and the Job records them.
func TestAWalkOfSeveralFoldersNamesNoneInTheOnePath(t *testing.T) {
	library := studioMovies()
	plan := libraryJob{mode: jobModeWalk, paths: []string{"/a", "/b"}}

	job := buildLibraryJob(library, providerSet{}, nil, plan, testJobImages, testNow)

	if got := containerEnvironment(*jobContainer(job, scannerContainer))[scanPathVariable]; got != "" {
		t.Errorf("scan reads %s = %q, want none", scanPathVariable, got)
	}
	if got := job.Metadata.Annotations[jobPathsAnnotation]; got != `["/a","/b"]` {
		t.Errorf("paths = %q, want both folders", got)
	}
}

// Every container but the scan counts under the enrich worker, reads the
// write its copy must hold, and names itself.
func TestEveryPhaseNamesItsWorkerAndItsSyncTarget(t *testing.T) {
	library := studioMovies()
	plan := libraryJob{mode: jobModeWalk, phases: servedPhases(library, providerSet{}),
		sync: syncTarget{actor: sqliteAgentActor, version: 40}}

	job := buildLibraryJob(library, providerSet{}, nil, plan, testJobImages, testNow)

	for _, container := range job.Spec.Template.Spec.Containers {
		got := containerEnvironment(container)
		if container.Name == scannerContainer {
			continue
		}
		if got[libraryWorkerVariable] != workerEnrich || got[libraryContainerVariable] != container.Name {
			t.Errorf("%s reads %v, want the enrich worker and its own name", container.Name, got)
		}
		if got[syncActorVariable] != sqliteAgentActor || got[syncVersionVariable] != "40" {
			t.Errorf("%s reads %v, want the sync target", container.Name, got)
		}
	}
}

// whether a container reads its own Job's name off the pod, which is
// what its runs row carries.
func readsTheJobName(container Container) bool {
	for _, variable := range container.Env {
		if variable.Name != jobNameVariable || variable.ValueFrom == nil {
			continue
		}
		return variable.ValueFrom.FieldRef != nil && variable.ValueFrom.FieldRef.FieldPath == jobNameFieldPath
	}
	return false
}

// The Job belongs to its Library, runs to completion, stays for an hour after
// it finishes, and records when the operator created it.
func TestTheJobBelongsToItsLibrary(t *testing.T) {
	job := testEnrichJob(studioMovies(), "")

	if !strings.HasPrefix(job.Metadata.Name, "movies-walk-") || job.Metadata.Namespace != "house" {
		t.Errorf("metadata = %+v, want the Library's walk", job.Metadata)
	}
	if job.Metadata.Labels[workerLabelKey] != jobModeWalk {
		t.Errorf("labels = %v, want the walk label", job.Metadata.Labels)
	}
	if got := jobCreated(job); !got.Equal(testNow) {
		t.Errorf("created = %v, want %v", got, testNow)
	}
	if len(job.Metadata.OwnerReferences) != 1 || job.Metadata.OwnerReferences[0].Name != "movies" {
		t.Errorf("ownerReferences = %+v, want the Library", job.Metadata.OwnerReferences)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != scanBackoffLimit {
		t.Errorf("backoffLimit = %+v, want %d", job.Spec.BackoffLimit, scanBackoffLimit)
	}
	if job.Spec.TTLSecondsAfterFinished == nil || *job.Spec.TTLSecondsAfterFinished != scanJobTTL {
		t.Errorf("ttlSecondsAfterFinished = %+v, want %d", job.Spec.TTLSecondsAfterFinished, scanJobTTL)
	}
	if spec := job.Spec.Template.Spec; spec.RestartPolicy != "Never" || spec.AutomountServiceAccountToken == nil ||
		*spec.AutomountServiceAccountToken {
		t.Errorf("pod = %+v, want no restart and no token", spec)
	}
}

// Each phase runs where a Ready source serves one of its facts, and names the
// facts the sources serve in the order the group runs them.
func TestAPhaseRunsWhereASourceServesItsFacts(t *testing.T) {
	anonymous := providerOfBlock("intros", providerBlockTheIntroDB)
	anonymous.Spec.TheIntroDB.SecretRef = nil
	cases := []struct {
		name     string
		provider *MetadataProvider
		phase    string
		want     string
	}{
		{name: "nfo from a provider of identity alone", provider: readyProvider("tmdb", "house", factIdentity),
			phase: nfoContainerName},
		{name: "nfo from a provider of the overview", provider: readyProvider("tmdb", "house", factIdentity, factOverview),
			phase: nfoContainerName, want: factOverview},
		{name: "nfo from a provider of every fact", provider: readyProvider("tmdb", "house"),
			phase: nfoContainerName, want: "overview,certification,rating.tmdb,credits"},
		{name: "art from a provider of one art fact", provider: readyProvider("tmdb", "house", factPoster),
			phase: artContainerName, want: factPoster},
		{name: "art from a provider of every fact", provider: readyProvider("tmdb", "house"),
			phase: artContainerName, want: "poster,backdrop,logo,season-poster,episode-thumb"},
		{name: "art from a provider of identity alone", provider: readyProvider("tmdb", "house", factIdentity),
			phase: artContainerName},
		{name: "trailers from a provider of every fact", provider: readyProvider("tmdb", "house"),
			phase: trailerContainerName, want: factTrailer},
		{name: "trailers from a video instance", provider: providerOfBlock("tube", providerBlockPeerTube),
			phase: trailerContainerName, want: factTrailer},
		{name: "marks from TheIntroDB with no key", provider: anonymous, phase: marksContainerName, want: factMarks},
		{name: "marks from IntroDB", provider: providerOfBlock("intros", providerBlockIntroDB),
			phase: marksContainerName, want: factMarks},
		{name: "marks from a provider of none", provider: readyProvider("tmdb", "house"), phase: marksContainerName},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			job := testEnrichJob(studioMovies(), "", one.provider)

			container := jobContainer(job, one.phase)
			if (container != nil) != (one.want != "") {
				t.Fatalf("containers = %v, want %s only where it is served", jobContainerNames(job), one.phase)
			}
			if container == nil {
				return
			}
			if got := containerEnvironment(*container)[libraryFactsVariable]; got != one.want {
				t.Errorf("%s = %q, want %q", libraryFactsVariable, got, one.want)
			}
		})
	}
}

// The address of a PeerTube instance reaches the container that asks it.
func TestTheTrailerContainerReadsThePeerTubeAddress(t *testing.T) {
	job := testEnrichJob(studioMovies(), "", providerOfBlock("tube", providerBlockPeerTube))

	got := containerEnvironment(*jobContainer(job, trailerContainerName))[providerEndpointVariable(providerBlockPeerTube)]
	if got != "https://tube.example" {
		t.Errorf("%s = %q, want the instance the source names", providerEndpointVariable(providerBlockPeerTube), got)
	}
}

// The languages the pass resolved reach every phase, because the score of a
// trailer reads them and no container holds a credential to read the Library
// or the household itself.
func TestEveryPhaseCarriesTheLanguages(t *testing.T) {
	library := studioMovies()
	library.Spec.Languages = []string{"en-US"}
	library.Spec.Sources = []string{"tmdb"}
	tmdb := readyProvider("tmdb", "house", factIdentity)
	set := providerSet{libraryKey(tmdb.Metadata.Namespace, tmdb.Metadata.Name): tmdb}
	plan := libraryJob{mode: jobModeWalk, phases: servedPhases(library, set)}

	job := buildLibraryJob(library, set, []string{"en-US", "ko"}, plan, testJobImages, testNow)

	for _, container := range phaseContainers(job) {
		if got := containerEnvironment(container)[libraryLanguagesVariable]; got != "en-US,ko" {
			t.Errorf("%s reads %s = %q, want the union", container.Name, libraryLanguagesVariable, got)
		}
	}
}

// The phases that open a media file run on the ffmpeg image and take the
// memory their work needs, above the scanner's limit.
func TestThePhasesThatOpenAFileTakeTheirOwnImageAndMemory(t *testing.T) {
	library := studioMovies()
	library.Spec.Trickplay.Enabled = true
	library.Spec.Trailers.Enabled = true
	job := testEnrichJob(library, "", readyProvider("tmdb", "house"), providerOfBlock("tube", providerBlockPeerTube))

	cases := []struct {
		phase, image, memory string
	}{
		{phase: factProbe, image: testFFmpegImage, memory: probeMemoryLimit},
		{phase: artContainerName, image: testScannerImage, memory: artMemoryLimit},
		{phase: trickplayContainerName, image: testFFmpegImage, memory: trickplayMemoryLimit},
		{phase: trailerFileContainerName, image: testFFmpegImage, memory: trailersMemoryLimit},
	}
	for _, one := range cases {
		container := jobContainer(job, one.phase)
		if container == nil {
			t.Fatalf("containers = %v, want %s", jobContainerNames(job), one.phase)
		}
		if container.Image != one.image || container.Resources.Limits["memory"] != one.memory ||
			one.memory == scannerMemoryLimit {
			t.Errorf("%s runs %s with %v, want %s with %s", one.phase, container.Image,
				container.Resources.Limits, one.image, one.memory)
		}
	}
	if got := jobContainer(job, trickplayContainerName).Resources.Requests["cpu"]; got != trickplayCPURequest {
		t.Errorf("trickplay requests %s of CPU, want %s", got, trickplayCPURequest)
	}
}

// The pod holds the render claim only where the Library names a render block
// and the Job runs trickplay, and only the trickplay container takes it.
func TestTheRenderClaimGoesToTheTrickplayPhase(t *testing.T) {
	cases := []struct {
		name      string
		trickplay bool
		render    bool
		want      bool
	}{
		{name: "trickplay with a render block", trickplay: true, render: true, want: true},
		{name: "trickplay in software", trickplay: true},
		{name: "a render block with no trickplay", render: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			library := studioMovies()
			library.Spec.Trickplay.Enabled = one.trickplay
			if one.render {
				library.Spec.Trickplay.Render = &TrickplayDevice{Class: "display-render"}
			}

			job := testEnrichJob(library, "")

			claims := job.Spec.Template.Spec.ResourceClaims
			if (len(claims) == 1) != one.want {
				t.Fatalf("resourceClaims = %+v, want one: %v", claims, one.want)
			}
			if !one.want {
				return
			}
			if claims[0].ResourceClaimTemplateName != "movies-trickplay" {
				t.Errorf("template = %q, want the Library's own", claims[0].ResourceClaimTemplateName)
			}
			for _, container := range job.Spec.Template.Spec.Containers {
				if held := len(container.Resources.Claims) == 1; held != (container.Name == trickplayContainerName) {
					t.Errorf("%s holds the claim: %v", container.Name, held)
				}
			}
		})
	}
}

// The trailer files run where the Library turns them on and a Ready source
// names a site to fetch from.
func TestTheTrailerFilesRunWhereASiteServesThem(t *testing.T) {
	cases := []struct {
		name     string
		enabled  bool
		provider *MetadataProvider
		want     bool
	}{
		{name: "on, with a video instance", enabled: true, provider: providerOfBlock("tube", providerBlockPeerTube), want: true},
		{name: "off", provider: providerOfBlock("tube", providerBlockPeerTube)},
		{name: "on, with no site", enabled: true, provider: readyProvider("tmdb", "house", factIdentity)},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			library := studioMovies()
			library.Spec.Trailers.Enabled = one.enabled

			job := testEnrichJob(library, "", one.provider)

			if held := jobContainer(job, trailerFileContainerName) != nil; held != one.want {
				t.Errorf("containers = %v, want the trailer files: %v", jobContainerNames(job), one.want)
			}
		})
	}
}

// The refresh times travel as one JSON value into every container of the
// Job, so a fact of any name reaches the container whole, and a Library
// that names none writes an empty value.
func TestTheJobCarriesTheRefreshTimes(t *testing.T) {
	library := studioMovies()
	library.Spec.Refresh = map[string]time.Time{
		factCredits: time.Date(2026, 9, 3, 21, 0, 0, 0, time.UTC),
	}

	job := testEnrichJob(library, "", readyProvider("tmdb", "house", factIdentity))

	for _, container := range phaseContainers(job) {
		got := containerEnvironment(container)[libraryRefreshVariable]
		if got != `{"credits":"2026-09-03T21:00:00Z"}` {
			t.Errorf("%s reads %s = %q, want the JSON-encoded map", container.Name, libraryRefreshVariable, got)
		}
	}
	empty := testEnrichJob(studioMovies(), "", readyProvider("tmdb", "house", factIdentity))
	if got := containerEnvironment(*jobContainer(empty, factProbe))[libraryRefreshVariable]; got != "" {
		t.Errorf("%s = %q, want no value for a Library that names no refresh", libraryRefreshVariable, got)
	}
}

// What the container reads back out of that environment, and what it
// reads out of a value it cannot parse: no refresh at all, because a
// container that took a bad value for a refresh would ask a provider
// about every title.
func TestTheContainerReadsTheRefreshTimesItIsGiven(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want refreshTimes
	}{
		{name: "no value at all", want: refreshTimes{}},
		{
			name: "the map the operator writes",
			raw:  `{"credits":"2026-09-03T21:00:00Z"}`,
			want: refreshTimes{factCredits: time.Date(2026, 9, 3, 21, 0, 0, 0, time.UTC)},
		},
		{name: "a value that is not JSON", raw: "{", want: refreshTimes{}},
		{name: "a time this image cannot read", raw: `{"credits":"soon"}`, want: refreshTimes{}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := parseRefresh(test.raw)

			if len(got) != len(test.want) {
				t.Fatalf("refresh = %v, want %v", got, test.want)
			}
			for fact, at := range test.want {
				if !got[fact].Equal(at) {
					t.Errorf("refresh[%s] = %v, want %v", fact, got[fact], at)
				}
			}
		})
	}
}

// The walk is not a fact and no container runs it, so it does not travel to
// the phases' LIBRARY_REFRESH.
func TestRefreshValueSendsTheFactsAlone(t *testing.T) {
	library := studioMovies()
	library.Spec.Refresh = map[string]time.Time{
		factCredits: testNow,
		refreshWalk: testNow,
	}

	sent := map[string]string{}
	if err := json.Unmarshal([]byte(refreshValue(library)), &sent); err != nil {
		t.Fatal(err)
	}
	if _, held := sent[factCredits]; !held {
		t.Errorf("refreshValue sent %v, want the fact", sent)
	}
	if _, held := sent[refreshWalk]; held {
		t.Errorf("refreshValue sent %v, want no walk", sent)
	}
}
