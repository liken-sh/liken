package main

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
)

// demandPull annotates the PersistentVolume, which is the one action
// that demands a pull.
func demandPull(t *testing.T, answering *node, name, at string) {
	t.Helper()
	volumes := cluster(t, answering).CoreV1().PersistentVolumes()
	held, err := volumes.Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the PersistentVolume: %v", err)
	}
	if held.Annotations == nil {
		held.Annotations = map[string]string{}
	}
	held.Annotations[demandAnnotation] = at
	if _, err := volumes.Update(t.Context(), held, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("annotating the PersistentVolume: %v", err)
	}
}

// oldDemand is a demand stamped long before any test stages a volume:
// the value a webhook leaves behind, because it never removes it.
const oldDemand = "2026-09-01T09:00:00Z"

// demandAt is a demand stamped the offset from now, in the form the
// webhook writes.
func demandAt(offset time.Duration) string {
	return time.Now().Add(offset).UTC().Format(time.RFC3339)
}

// waitForCommit waits until the volume's tree stands on the commit,
// and fails at the deadline.
func waitForCommit(t *testing.T, held *volume, want string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if commit, _ := held.condition(); commit == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	commit, trouble := held.condition()
	t.Fatalf("the volume is on %s (%q) within 30s, want %s", commit, trouble, want)
}

// demandedVolume is one read-only claim of the URL with the pull it
// names, published to one pod, with the PersistentVolume a demand is
// written on.
func demandedVolume(t *testing.T, answering *node, id, url, pull string) *volume {
	t.Helper()
	claimedVolume(t, answering, id)
	staged, held := stagedReadOnly(t, answering, id, url, map[string]string{"pull": pull})
	publishedTo(t, answering, staged, "reader")
	return held
}

// claimedVolume writes the PersistentVolume that carries the handle,
// bound to a claim of the same name.
func claimedVolume(t *testing.T, answering *node, id string) {
	t.Helper()
	held := csiVolume(id, driverName)
	held.Spec.ClaimRef = &corev1.ObjectReference{Namespace: "home", Name: id}
	held.Status.Phase = corev1.VolumeBound
	client := cluster(t, answering)
	if _, err := client.CoreV1().PersistentVolumes().
		Create(t.Context(), held, metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the PersistentVolume: %v", err)
	}
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "home", Name: id},
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("home").
		Create(t.Context(), claim, metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the claim: %v", err)
	}
}

// watchDemands starts the one watch the node holds, for the test's
// own run.
func watchDemands(t *testing.T, answering *node) {
	t.Helper()
	go answering.demands.follow(t.Context())
}

func TestAnAnnotationOnThePersistentVolumePullsTheTree(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	held := demandedVolume(t, answering, "franchises", fileURL(source), "on-demand")
	watchDemands(t, answering)

	want := commitFiles(t, source, map[string]string{"a.txt": "two"})
	demandPull(t, answering, "franchises", demandAt(0))

	waitForCommit(t, held, want)
	if got := readTree(t, held.tree); !sameTree(got, map[string]string{"a.txt": "two"}) {
		t.Errorf("the tree holds %v, want the commit the demand pulled", got)
	}
}

func TestAVolumeThatPullsNeverTakesNoDemand(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	pinned := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	moved := repositoryWithACommit(t, map[string]string{"b.txt": "one"})
	held := demandedVolume(t, answering, "pinned", fileURL(pinned), "never")
	moving := demandedVolume(t, answering, "moving", fileURL(moved), "on-demand")
	standing, _ := held.condition()
	watchDemands(t, answering)

	commitFiles(t, pinned, map[string]string{"a.txt": "two"})
	want := commitFiles(t, moved, map[string]string{"b.txt": "two"})
	demandPull(t, answering, "pinned", demandAt(0))
	demandPull(t, answering, "moving", demandAt(0))

	// The volume that pulled is the evidence that the demand on the
	// pinned volume was read and did nothing.
	waitForCommit(t, moving, want)
	if commit, _ := held.condition(); commit != standing {
		t.Errorf("the pinned volume moved to %s, want %s", commit, standing)
	}
}

func TestADemandOnAPinnedVolumeMovesNoVolumeOfTheSameRepository(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	pinned := demandedVolume(t, answering, "pinned", url, "never")
	moving := demandedVolume(t, answering, "moving", url, "on-demand")
	standing, _ := moving.condition()
	watchDemands(t, answering)

	want := commitFiles(t, source, map[string]string{"a.txt": "two"})
	demandPull(t, answering, "pinned", demandAt(0))

	// The watch reads the demand on the pinned volume, and the volume
	// that shares the repository has to keep the commit it staged.
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		if commit, _ := moving.condition(); commit != standing {
			t.Fatalf("a demand on the pinned volume moved %s to %s", moving.id, commit)
		}
		time.Sleep(10 * time.Millisecond)
	}

	demandPull(t, answering, "moving", demandAt(2*time.Second))
	waitForCommit(t, moving, want)
	if commit, _ := pinned.condition(); commit != standing {
		t.Errorf("the pinned volume moved to %s, want %s", commit, standing)
	}
}

func TestADemandOnAVolumeWhoseLoopIsGoneDoesNothing(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	answering.demand(&volume{
		id: "franchises",
		attributes: &attributes{
			url:  "file:///gone",
			pull: pullPolicy{mode: pullOnDemand},
		},
	})
}

func TestADemandOnAWriteableVolumeDoesNothingAndSaysSoOnce(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	source := bareRemote(t, map[string]string{"a.txt": "one"})
	boundVolume(t, answering, "config", "")
	held, _ := stagedWriteable(t, answering, "config", fileURL(source))
	standing, _ := held.condition()
	watchDemands(t, answering)

	demandPull(t, answering, "config", demandAt(0))

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logs.String(), "the demand did nothing") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A watch that resumes can send the same volume again, and one
	// demand is acted on once.
	time.Sleep(200 * time.Millisecond)
	if got := strings.Count(logs.String(), "the demand did nothing"); got != 1 {
		t.Errorf("the log says the demand did nothing %d times, want 1 (%q)", got, logs)
	}
	if commit, _ := held.condition(); commit != standing {
		t.Errorf("the writeable volume moved to %s, want %s", commit, standing)
	}
}

func TestADemandReadWhileTheVolumeStagesIsActedOnWhenTheStageEnds(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})

	// The stage lists the PersistentVolumes to find the claim, after it
	// starts and before it adds the volume to the node. The watch sends
	// the demand at that moment, so the volume is not staged yet when
	// the node reads it.
	var once sync.Once
	cluster(t, answering).PrependReactor("list", "persistentvolumes",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			once.Do(func() {
				answering.demands.read(t.Context(),
					annotated(csiVolume("franchises", driverName), demandAt(0)))
			})
			return false, nil, nil
		})
	held := demandedVolume(t, answering, "franchises", fileURL(source), "on-demand")

	waitForCondition(t, held, ", demanded ")
	waitForPass(t, answering, held)
}

// waitForPass waits until the pass the demand started has ended. The
// pass writes into the store, and a pass that outlives the test writes
// while the test's cleanup removes the store. The pass records its
// start under the repository's lock and holds the lock until it ends,
// so the lock is free again only after the pass.
func waitForPass(t *testing.T, answering *node, held *volume) {
	t.Helper()
	waitForCondition(t, held, ", pulled ")
	answering.store.repository(held.attributes.url).lock()()
}

func TestAnOldDemandIsNotActedOnWhenTheVolumeStages(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})

	// A webhook never removes its annotation, so the node reads an old
	// value before it stages the volume. The stage's own fetch is newer
	// than that demand.
	answering.demands.read(t.Context(),
		annotated(csiVolume("franchises", driverName), oldDemand))
	held := demandedVolume(t, answering, "franchises", fileURL(source), "on-demand")

	noDemand(t, held)
}

// scriptedVolumeWatches answers every watch on PersistentVolumes with
// the next watcher from the returned channel, which the test sends
// events on.
func scriptedVolumeWatches(t *testing.T, answering *node) chan *watch.FakeWatcher {
	t.Helper()
	opened := make(chan *watch.FakeWatcher, 4)
	cluster(t, answering).PrependWatchReactor("persistentvolumes",
		func(k8stesting.Action) (bool, watch.Interface, error) {
			sent := watch.NewFake()
			opened <- sent
			return true, sent, nil
		})
	return opened
}

// noDemand fails when the volume's report says a demand named it.
func noDemand(t *testing.T, held *volume) {
	t.Helper()
	if _, message := held.report(); strings.Contains(message, ", demanded ") {
		t.Errorf("the report says %q, want no demand", message)
	}
}

func TestADeletedPersistentVolumeTakesItsDemandAway(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	opened := scriptedVolumeWatches(t, answering)
	watchDemands(t, answering)
	sent := <-opened

	// The demand is newer than the stage, so only its delete keeps the
	// stage from acting on it.
	demanded := annotated(csiVolume("franchises", driverName), demandAt(0))
	sent.Modify(demanded)
	sent.Delete(demanded)
	sent.Delete(&corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "local"}})
	settled(t, answering, sent)
	held := demandedVolume(t, answering, "franchises", fileURL(source), "on-demand")

	noDemand(t, held)
}

func TestAListThatNoLongerHoldsAPersistentVolumeTakesItsDemandAway(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	volumes := cluster(t, answering).CoreV1().PersistentVolumes()
	if _, err := volumes.Create(t.Context(),
		annotated(csiVolume("franchises", driverName), demandAt(0)),
		metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the PersistentVolume: %v", err)
	}
	opened := scriptedVolumeWatches(t, answering)
	watchDemands(t, answering)
	sent := <-opened

	if err := volumes.Delete(t.Context(), "franchises", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("deleting the PersistentVolume: %v", err)
	}
	// A 410 makes the informer list again, and the second watch opens
	// only after that list.
	sent.Action(watch.Error, gone().Object)
	settled(t, answering, <-opened)
	held := demandedVolume(t, answering, "franchises", fileURL(source), "on-demand")

	noDemand(t, held)
}

// writeableDemand is a demand on the writeable volume config. A
// writeable volume logs each demand it acts on and pulls nothing, so the
// log counts the demands the node acted on.
func writeableDemand(at string) *corev1.PersistentVolume {
	return annotated(csiVolume("config", driverName), at)
}

// settled sends a demand on a PersistentVolume no test stages, and
// waits until the node has read it. The informer hands the handlers
// its events in the order the watch sent them, so settled returns only
// once the node has read every event sent before it.
func settled(t *testing.T, answering *node, sent *watch.FakeWatcher) {
	t.Helper()
	sent.Modify(annotated(csiVolume("sentinel", driverName), oldDemand))
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		answering.demands.mu.Lock()
		_, read := answering.demands.seen["sentinel"]
		answering.demands.mu.Unlock()
		if read {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the node did not read the sentinel within 30s")
}

func TestARecreatedPersistentVolumeWithTheSameDemandIsNotActedOnAgain(t *testing.T) {
	for _, c := range []struct {
		name   string
		delete func(t *testing.T, answering *node, sent *watch.FakeWatcher, opened chan *watch.FakeWatcher) *watch.FakeWatcher
	}{
		{
			name: "a delete the watch sends",
			delete: func(t *testing.T, _ *node, sent *watch.FakeWatcher, _ chan *watch.FakeWatcher) *watch.FakeWatcher {
				sent.Delete(writeableDemand(oldDemand))
				return sent
			},
		},
		{
			name: "a list that no longer holds it",
			delete: func(t *testing.T, answering *node, sent *watch.FakeWatcher, opened chan *watch.FakeWatcher) *watch.FakeWatcher {
				if err := cluster(t, answering).CoreV1().PersistentVolumes().
					Delete(t.Context(), "config", metav1.DeleteOptions{}); err != nil {
					t.Fatalf("deleting the PersistentVolume: %v", err)
				}
				sent.Action(watch.Error, gone().Object)
				return <-opened
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs := &logbook{}
			answering, _ := testNode(t, logs)
			boundVolume(t, answering, "config", "")
			stagedWriteable(t, answering, "config",
				fileURL(bareRemote(t, map[string]string{"a.txt": "one"})))
			opened := scriptedVolumeWatches(t, answering)
			watchDemands(t, answering)
			sent := <-opened

			// The node answered the demand before the delete, so the same
			// time on a PersistentVolume created again asks for nothing
			// new.
			at := demandAt(0)
			sent.Modify(writeableDemand(at))
			sent = c.delete(t, answering, sent, opened)
			sent.Add(writeableDemand(at))
			settled(t, answering, sent)

			if got := strings.Count(logs.String(), "the demand did nothing"); got != 1 {
				t.Errorf("the log says the demand did nothing %d times, want 1 (%q)", got, logs)
			}
		})
	}
}

func TestARelistActsOnceOnADemandItAlreadyActedOn(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	boundVolume(t, answering, "config", "")
	stagedWriteable(t, answering, "config",
		fileURL(bareRemote(t, map[string]string{"a.txt": "one"})))
	opened := scriptedVolumeWatches(t, answering)
	watchDemands(t, answering)
	sent := <-opened

	at := demandAt(0)
	demandPull(t, answering, "config", at)
	sent.Modify(writeableDemand(at))
	sent.Action(watch.Error, gone().Object)
	settled(t, answering, <-opened)

	if got := strings.Count(logs.String(), "the demand did nothing"); got != 1 {
		t.Errorf("the log says the demand did nothing %d times, want 1 (%q)", got, logs)
	}
}

func TestTheSameDemandReadTwiceIsActedOnOnce(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	source := bareRemote(t, map[string]string{"a.txt": "one"})
	boundVolume(t, answering, "config", "")
	stagedWriteable(t, answering, "config", fileURL(source))
	demanded := annotated(csiVolume("config", driverName), demandAt(0))

	answering.demands.read(t.Context(), demanded)
	answering.demands.read(t.Context(), demanded)

	if got := strings.Count(logs.String(), "the demand did nothing"); got != 1 {
		t.Errorf("the log says the demand did nothing %d times, want 1 (%q)", got, logs)
	}
}

func TestADemandOnALoopThatIsAlreadyWokenWaitsForThatPass(t *testing.T) {
	held := &volume{id: "franchises"}
	loop := &follower{
		node:     &node{},
		demanded: make(chan struct{}, 1),
		volumes:  map[string]*volume{held.id: held},
		wanted:   map[string]*volume{},
	}

	loop.demand(held)
	loop.demand(held)

	if got := len(loop.demanded); got != 1 {
		t.Errorf("the loop holds %d wakes, want 1", got)
	}
}

func TestABurstOfDemandsCostsOnePullPerInterval(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	answering.demandMin = time.Second
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	held := demandedVolume(t, answering, "franchises", fileURL(source), "on-demand")
	watchDemands(t, answering)

	first := commitFiles(t, source, map[string]string{"a.txt": "two"})
	demandPull(t, answering, "franchises", demandAt(0))
	waitForCommit(t, held, first)

	second := commitFiles(t, source, map[string]string{"a.txt": "three"})
	for demand := range 20 {
		demandPull(t, answering, "franchises", demandAt(time.Duration(demand+2)*time.Second))
	}

	waitForCommit(t, held, second)
	counted, found := demandedOf(t, answering.readings, "home", "franchises")
	if !found || counted != 2 {
		t.Errorf("21 demands counted %v pulls (found: %v), want 2", counted, found)
	}
}

// demandedOf is what git_csi_demanded_pulls_total reads for the
// volume, and false when the volume is on no counter.
func demandedOf(t *testing.T, readings *metrics, namespace, id string) (float64, bool) {
	t.Helper()
	families, err := readings.registry.Gather()
	if err != nil {
		t.Fatalf("gathering the metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "git_csi_demanded_pulls_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["namespace"] == namespace && labels["volume"] == id {
				return metric.GetCounter().GetValue(), true
			}
		}
	}
	return 0, false
}

func TestTheReportCarriesTheLastDemandAndTheLastPull(t *testing.T) {
	demanded := time.Unix(1757000000, 0).UTC()
	held := &volume{attributes: &attributes{ref: "main"}, commit: "d633176146e997"}

	if _, message := held.report(); message != "main at d633176" {
		t.Errorf("a volume nothing demanded reports %q, want the commit alone", message)
	}
	held.reportDemanded(demanded)
	held.reportPulled(demanded.Add(time.Second))

	_, message := held.report()
	want := "main at d633176, demanded 2025-09-04T15:33:20Z, pulled 2025-09-04T15:33:21Z"
	if message != want {
		t.Errorf("the report says %q, want %q", message, want)
	}
}

func TestARestartPullsEveryVolumeThatDoesNotPullNever(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	demandedVolume(t, answering, "franchises", fileURL(source), "on-demand")

	want := commitFiles(t, source, map[string]string{"a.txt": "two"})
	again := restarted(t, answering, true)
	again.mu.Lock()
	resumed := again.staged["franchises"]
	again.mu.Unlock()

	waitForCommit(t, resumed, want)
}

// csiVolume is a PersistentVolume of the driver it names, with the
// handle as its name.
func csiVolume(handle, driver string) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: handle},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       driver,
					VolumeHandle: handle,
				},
			},
		},
	}
}

// annotated is the PersistentVolume with the demand written on it.
func annotated(held *corev1.PersistentVolume, at string) *corev1.PersistentVolume {
	held.Annotations = map[string]string{demandAnnotation: at}
	return held
}

func TestADemandTheNodeCannotActOnDoesNothing(t *testing.T) {
	for _, c := range []struct {
		name string
		held *corev1.PersistentVolume
	}{
		{
			name: "a PersistentVolume of another driver",
			held: annotated(csiVolume("franchises", "other.example.com"), "2026-09-06T14:31:07Z"),
		},
		{
			name: "a PersistentVolume of no CSI driver",
			held: annotated(&corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "franchises"},
			}, "2026-09-06T14:31:07Z"),
		},
		{
			name: "a PersistentVolume nothing demanded",
			held: csiVolume("franchises", driverName),
		},
		{
			name: "a handle this node does not hold",
			held: annotated(csiVolume("other", driverName), "2026-09-06T14:31:07Z"),
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			answering, _ := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			held := demandedVolume(t, answering, "franchises", fileURL(source), "on-demand")
			standing, _ := held.condition()

			commitFiles(t, source, map[string]string{"a.txt": "two"})
			answering.demands.read(t.Context(), c.held)

			if commit, _ := held.condition(); commit != standing {
				t.Errorf("the volume moved to %s, want %s", commit, standing)
			}
		})
	}
}

func TestTheDemandWatchEndsWithTheDriver(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	cluster(t, answering).PrependWatchReactor("persistentvolumes",
		func(k8stesting.Action) (bool, watch.Interface, error) {
			return true, watch.NewFake(), nil
		})

	ctx, stop := context.WithCancel(t.Context())
	over := make(chan struct{})
	go func() {
		defer close(over)
		answering.demands.follow(ctx)
	}()
	stop()
	select {
	case <-over:
	case <-time.After(30 * time.Second):
		t.Fatal("the watch did not end with the driver")
	}
}

func TestADriverOutsideAClusterWatchesNothing(t *testing.T) {
	outside := &demanding{}
	outside.follow(t.Context())
}

// watchRestartsOf is what git_csi_watch_restarts_total reads for the
// kind, and false when nothing has counted one yet.
func watchRestartsOf(t *testing.T, readings *metrics, kind string) (float64, bool) {
	t.Helper()
	families, err := readings.registry.Gather()
	if err != nil {
		t.Fatalf("gathering the metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "git_csi_watch_restarts_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "kind" && label.GetValue() == kind {
					return metric.GetCounter().GetValue(), true
				}
			}
		}
	}
	return 0, false
}

func TestARestartedWatchCountsOnGitCSIWatchRestartsTotal(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	// Every watch the API server answers closes at once, so the node
	// opens it again.
	cluster(t, answering).PrependWatchReactor("persistentvolumes",
		func(k8stesting.Action) (bool, watch.Interface, error) {
			closed := watch.NewFake()
			closed.Stop()
			return true, closed, nil
		})

	ctx, stop := context.WithCancel(t.Context())
	over := make(chan struct{})
	go func() {
		defer close(over)
		answering.demands.follow(ctx)
	}()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if count, found := watchRestartsOf(t, answering.readings, persistentVolumeKind); found && count > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	select {
	case <-over:
	case <-time.After(30 * time.Second):
		t.Fatal("the watch did not end with the driver")
	}

	if count, found := watchRestartsOf(t, answering.readings, persistentVolumeKind); !found || count == 0 {
		t.Errorf("git_csi_watch_restarts_total reads %v (found: %v), want at least one restart", count, found)
	}
}

func TestADeleteWithNoCopyOfThePersistentVolumeIsLogged(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)

	answering.demands.deleted(t.Context(), cache.DeletedFinalStateUnknown{Key: "franchises"})

	if !strings.Contains(logs.String(), `msg="the delete carries no PersistentVolume"`) {
		t.Errorf("the log is %q, want the delete that carries no PersistentVolume", logs)
	}
}
