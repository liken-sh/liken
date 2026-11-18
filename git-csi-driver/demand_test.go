package main

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
)

// demandPull sets the demand annotation on the PersistentVolume, which
// is how a person or the webhook requests a pull.
func demandPull(t *testing.T, n *node, name, at string) {
	t.Helper()
	volumes := cluster(t, n).CoreV1().PersistentVolumes()
	pv, err := volumes.Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the PersistentVolume: %v", err)
	}
	if pv.Annotations == nil {
		pv.Annotations = map[string]string{}
	}
	pv.Annotations[demandAnnotation] = at
	if _, err := volumes.Update(t.Context(), pv, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("annotating the PersistentVolume: %v", err)
	}
}

// oldDemand is earlier than any stage in a test, because a synctest
// bubble's clock starts at 2000-01-01. It is like an annotation that
// the webhook wrote long ago and never removed.
const oldDemand = "1999-09-01T09:00:00Z"

// demandAt returns the RFC 3339 stamp of a demand at the offset from
// now.
func demandAt(offset time.Duration) string {
	return time.Now().Add(offset).UTC().Format(time.RFC3339)
}

// waitForCommit waits until the bubble is blocked, then fails unless
// the volume's work tree is on the commit.
func waitForCommit(t *testing.T, vol *volume, want string) {
	t.Helper()
	synctest.Wait()
	if commit, fault := vol.commitAndFault(); commit != want {
		t.Fatalf("the volume is on %s (%q), want %s", commit, fault, want)
	}
}

// demandedVolume stages a read-only volume of the URL with the pull
// mode, publishes it to one pod, and creates the PersistentVolume that a
// demand is written on.
func demandedVolume(t *testing.T, n *node, id, url, pull string) *volume {
	t.Helper()
	claimedVolume(t, n, id)
	staged, vol := stagedReadOnly(t, n, id, url, map[string]string{"pull": pull})
	publishedTo(t, n, staged, "reader")
	return vol
}

// claimedVolume creates a bound PersistentVolume and its claim, both
// named for the handle.
func claimedVolume(t *testing.T, n *node, id string) {
	t.Helper()
	pv := csiVolume(id, driverName)
	pv.Spec.ClaimRef = &corev1.ObjectReference{Namespace: "home", Name: id}
	pv.Status.Phase = corev1.VolumeBound
	client := cluster(t, n)
	if _, err := client.CoreV1().PersistentVolumes().
		Create(t.Context(), pv, metav1.CreateOptions{}); err != nil {
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

// watchDemands runs the node's PersistentVolume watch until the test
// ends.
func watchDemands(t *testing.T, n *node) {
	t.Helper()
	go n.demands.run(t.Context())
}

func TestAnAnnotationOnThePersistentVolumePullsTheTree(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		vol := demandedVolume(t, n, "franchises", fileURL(source), "on-demand")
		watchDemands(t, n)

		want := commitFiles(t, source, map[string]string{"a.txt": "two"})
		demandPull(t, n, "franchises", demandAt(0))

		waitForCommit(t, vol, want)
		if got := readTree(t, vol.tree); !sameTree(got, map[string]string{"a.txt": "two"}) {
			t.Errorf("the tree has %v, want the commit the demand pulled", got)
		}
	})
}

func TestADemandDoesNotMoveAVolumeWithPullNever(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		pinned := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		moved := repositoryWithACommit(t, map[string]string{"b.txt": "one"})
		vol := demandedVolume(t, n, "pinned", fileURL(pinned), "never")
		moving := demandedVolume(t, n, "moving", fileURL(moved), "on-demand")
		before, _ := vol.commitAndFault()
		watchDemands(t, n)

		commitFiles(t, pinned, map[string]string{"a.txt": "two"})
		want := commitFiles(t, moved, map[string]string{"b.txt": "two"})
		demandPull(t, n, "pinned", demandAt(0))
		demandPull(t, n, "moving", demandAt(0))

		// The moving volume's pull shows the watch has already read the
		// demand on the pinned volume, and that it did nothing.
		waitForCommit(t, moving, want)
		if commit, _ := vol.commitAndFault(); commit != before {
			t.Errorf("the pinned volume moved to %s, want %s", commit, before)
		}
	})
}

func TestADemandOnAPinnedVolumeMovesNoVolumeOfTheSameRepository(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		url := fileURL(source)
		pinned := demandedVolume(t, n, "pinned", url, "never")
		moving := demandedVolume(t, n, "moving", url, "on-demand")
		before, _ := moving.commitAndFault()
		watchDemands(t, n)

		want := commitFiles(t, source, map[string]string{"a.txt": "two"})
		demandPull(t, n, "pinned", demandAt(0))

		// After the watch reads the demand on the pinned volume, the
		// other volume of the same repository must stay on its staged
		// commit for a whole interval.
		time.Sleep(n.demandMin)
		synctest.Wait()
		if commit, _ := moving.commitAndFault(); commit != before {
			t.Fatalf("a demand on the pinned volume moved %s to %s", moving.id, commit)
		}

		demandPull(t, n, "moving", demandAt(2*time.Second))
		waitForCommit(t, moving, want)
		if commit, _ := pinned.commitAndFault(); commit != before {
			t.Errorf("the pinned volume moved to %s, want %s", commit, before)
		}
	})
}

func TestADemandOnAVolumeWhoseLoopIsGoneDoesNothing(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	n.demand(&volume{
		id: "franchises",
		attributes: &attributes{
			url:  "file:///gone",
			pull: pullPolicy{mode: pullOnDemand},
		},
	})
}

func TestADemandOnAWriteableVolumePullsNothingAndLogsOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := &logbook{}
		n, _ := testNode(t, logs)
		source := bareRemote(t, map[string]string{"a.txt": "one"})
		boundVolume(t, n, "config", "")
		vol, _ := stagedWriteable(t, n, "config", fileURL(source))
		before, _ := vol.commitAndFault()
		watchDemands(t, n)

		demandPull(t, n, "config", demandAt(0))

		// The watch reads the demand once, so the node logs once that
		// it did nothing.
		synctest.Wait()
		if got := strings.Count(logs.String(), "Ignored a pull demand"); got != 1 {
			t.Errorf("the log has Ignored a pull demand %d times, want 1 (%q)", got, logs)
		}
		if commit, _ := vol.commitAndFault(); commit != before {
			t.Errorf("the writeable volume moved to %s, want %s", commit, before)
		}
	})
}

func TestADemandReadWhileTheVolumeStagesIsActedOnWhenTheStageEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})

		// The stage lists PersistentVolumes to find the claim before it
		// adds the volume to the node. The demand is read during that
		// list, so the volume is not staged yet.
		var once sync.Once
		cluster(t, n).PrependReactor("list", "persistentvolumes",
			func(k8stesting.Action) (bool, runtime.Object, error) {
				once.Do(func() {
					n.demands.read(t.Context(),
						annotated(csiVolume("franchises", driverName), demandAt(0)))
				})
				return false, nil, nil
			})
		vol := demandedVolume(t, n, "franchises", fileURL(source), "on-demand")

		waitForCondition(t, vol, ", demanded ")
		waitForPass(t, vol)
	})
}

// waitForPass fails unless the pass that the demand started has
// pulled. synctest.Wait does not return while a goroutine waits on a
// git process, so the check runs after the pass ends.
func waitForPass(t *testing.T, vol *volume) {
	t.Helper()
	waitForCondition(t, vol, ", pulled ")
}

func TestAnOldDemandIsNotActedOnWhenTheVolumeStages(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})

	// The stage's own fetch is newer than the demand, so the demand
	// starts no pull.
	n.demands.read(t.Context(),
		annotated(csiVolume("franchises", driverName), oldDemand))
	vol := demandedVolume(t, n, "franchises", fileURL(source), "on-demand")

	noDemand(t, vol)
}

// scriptedVolumeWatches returns each watch of PersistentVolumes that
// the node opens, as a fake watcher on the channel, so the test sends
// the watch's events.
func scriptedVolumeWatches(t *testing.T, n *node) chan *watch.FakeWatcher {
	t.Helper()
	opened := make(chan *watch.FakeWatcher, 4)
	cluster(t, n).PrependWatchReactor("persistentvolumes",
		func(k8stesting.Action) (bool, watch.Interface, error) {
			sent := watch.NewFake()
			opened <- sent
			return true, sent, nil
		})
	return opened
}

// noDemand fails when the volume's report shows a demand.
func noDemand(t *testing.T, vol *volume) {
	t.Helper()
	if _, message := vol.health(); strings.Contains(message, ", demanded ") {
		t.Errorf("the report is %q, want no demand", message)
	}
}

func TestADeletedPersistentVolumeTakesItsDemandAway(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		opened := scriptedVolumeWatches(t, n)
		watchDemands(t, n)
		sent := <-opened

		// The demand is newer than the stage, so only the delete stops
		// the stage from acting on it.
		demanded := annotated(csiVolume("franchises", driverName), demandAt(0))
		sent.Modify(demanded)
		sent.Delete(demanded)
		sent.Delete(&corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "local"}})
		settled(t, n, sent)
		vol := demandedVolume(t, n, "franchises", fileURL(source), "on-demand")

		noDemand(t, vol)
	})
}

func TestAListThatNoLongerHoldsAPersistentVolumeTakesItsDemandAway(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		volumes := cluster(t, n).CoreV1().PersistentVolumes()
		if _, err := volumes.Create(t.Context(),
			annotated(csiVolume("franchises", driverName), demandAt(0)),
			metav1.CreateOptions{}); err != nil {
			t.Fatalf("writing the PersistentVolume: %v", err)
		}
		opened := scriptedVolumeWatches(t, n)
		watchDemands(t, n)
		sent := <-opened

		if err := volumes.Delete(t.Context(), "franchises", metav1.DeleteOptions{}); err != nil {
			t.Fatalf("deleting the PersistentVolume: %v", err)
		}
		// A 410 Gone makes the informer list again, and the second watch
		// opens only after that list.
		sent.Action(watch.Error, gone().Object)
		settled(t, n, <-opened)
		vol := demandedVolume(t, n, "franchises", fileURL(source), "on-demand")

		noDemand(t, vol)
	})
}

// writeableDemand is a PersistentVolume with a demand for the
// writeable volume config. A writeable volume logs each demand it acts
// on and pulls nothing, so the log counts those demands.
func writeableDemand(at string) *corev1.PersistentVolume {
	return annotated(csiVolume("config", driverName), at)
}

// settled sends a demand for a PersistentVolume that no test stages,
// and fails unless the node has read it when the bubble is blocked. The
// informer calls its handlers in the order of the watch's events, so
// the node has read every earlier event too.
func settled(t *testing.T, n *node, sent *watch.FakeWatcher) {
	t.Helper()
	sent.Modify(annotated(csiVolume("sentinel", driverName), oldDemand))
	synctest.Wait()
	n.demands.mu.Lock()
	_, read := n.demands.seen["sentinel"]
	n.demands.mu.Unlock()
	if !read {
		t.Fatal("the node did not read the sentinel")
	}
}

func TestARecreatedPersistentVolumeWithTheSameDemandIsNotActedOnAgain(t *testing.T) {
	for _, c := range []struct {
		name   string
		delete func(t *testing.T, n *node, sent *watch.FakeWatcher, opened chan *watch.FakeWatcher) *watch.FakeWatcher
	}{
		{
			name: "a delete the watch sends",
			delete: func(t *testing.T, _ *node, sent *watch.FakeWatcher, _ chan *watch.FakeWatcher) *watch.FakeWatcher {
				sent.Delete(writeableDemand(oldDemand))
				return sent
			},
		},
		{
			name: "a list that no longer has it",
			delete: func(t *testing.T, n *node, sent *watch.FakeWatcher, opened chan *watch.FakeWatcher) *watch.FakeWatcher {
				if err := cluster(t, n).CoreV1().PersistentVolumes().
					Delete(t.Context(), "config", metav1.DeleteOptions{}); err != nil {
					t.Fatalf("deleting the PersistentVolume: %v", err)
				}
				sent.Action(watch.Error, gone().Object)
				return <-opened
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				logs := &logbook{}
				n, _ := testNode(t, logs)
				boundVolume(t, n, "config", "")
				stagedWriteable(t, n, "config",
					fileURL(bareRemote(t, map[string]string{"a.txt": "one"})))
				opened := scriptedVolumeWatches(t, n)
				watchDemands(t, n)
				sent := <-opened

				// The node acted on the demand before the delete, so the
				// same time on a recreated PersistentVolume is not a new
				// demand.
				at := demandAt(0)
				sent.Modify(writeableDemand(at))
				sent = c.delete(t, n, sent, opened)
				sent.Add(writeableDemand(at))
				settled(t, n, sent)

				if got := strings.Count(logs.String(), "Ignored a pull demand"); got != 1 {
					t.Errorf("the log has Ignored a pull demand %d times, want 1 (%q)", got, logs)
				}
			})
		})
	}
}

func TestARelistActsOnceOnADemandItAlreadyActedOn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := &logbook{}
		n, _ := testNode(t, logs)
		boundVolume(t, n, "config", "")
		stagedWriteable(t, n, "config",
			fileURL(bareRemote(t, map[string]string{"a.txt": "one"})))
		opened := scriptedVolumeWatches(t, n)
		watchDemands(t, n)
		sent := <-opened

		at := demandAt(0)
		demandPull(t, n, "config", at)
		sent.Modify(writeableDemand(at))
		sent.Action(watch.Error, gone().Object)
		settled(t, n, <-opened)

		if got := strings.Count(logs.String(), "Ignored a pull demand"); got != 1 {
			t.Errorf("the log has Ignored a pull demand %d times, want 1 (%q)", got, logs)
		}
	})
}

func TestTheSameDemandReadTwiceIsActedOnOnce(t *testing.T) {
	logs := &logbook{}
	n, _ := testNode(t, logs)
	source := bareRemote(t, map[string]string{"a.txt": "one"})
	boundVolume(t, n, "config", "")
	stagedWriteable(t, n, "config", fileURL(source))
	demanded := annotated(csiVolume("config", driverName), demandAt(0))

	n.demands.read(t.Context(), demanded)
	n.demands.read(t.Context(), demanded)

	if got := strings.Count(logs.String(), "Ignored a pull demand"); got != 1 {
		t.Errorf("the log has Ignored a pull demand %d times, want 1 (%q)", got, logs)
	}
}

func TestADemandOnALoopThatIsAlreadyWokenWaitsForThatPass(t *testing.T) {
	vol := &volume{id: "franchises"}
	loop := &follower{
		node:     &node{},
		demanded: make(chan struct{}, 1),
		volumes:  map[string]*volume{vol.id: vol},
		wanted:   map[string]*volume{},
	}

	loop.demand(vol)
	loop.demand(vol)

	if got := len(loop.demanded); got != 1 {
		t.Errorf("the loop has %d wakes, want 1", got)
	}
}

func TestABurstOfDemandsCostsOnePullPerInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		n.demandMin = time.Second
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		vol := demandedVolume(t, n, "franchises", fileURL(source), "on-demand")
		watchDemands(t, n)

		first := commitFiles(t, source, map[string]string{"a.txt": "two"})
		demandPull(t, n, "franchises", demandAt(0))
		waitForCommit(t, vol, first)

		second := commitFiles(t, source, map[string]string{"a.txt": "three"})
		for demand := range 20 {
			demandPull(t, n, "franchises", demandAt(time.Duration(demand+2)*time.Second))
		}

		// The demands wait out the interval since the first pull.
		waitForCommit(t, vol, first)
		time.Sleep(n.demandMin)
		waitForCommit(t, vol, second)
		count, found := demandedOf(t, n.metrics, "home", "franchises")
		if !found || count != 2 {
			t.Errorf("21 demands counted %v pulls (found: %v), want 2", count, found)
		}
	})
}

// demandedOf returns the git_csi_demanded_pulls_total value for the
// volume, and false when the metric has no series for it.
func demandedOf(t *testing.T, registry *metrics, namespace, id string) (float64, bool) {
	t.Helper()
	families, err := registry.registry.Gather()
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

func TestTheReportShowsTheLastDemandAndTheLastPull(t *testing.T) {
	demanded := time.Unix(1757000000, 0).UTC()
	vol := &volume{attributes: &attributes{ref: "main"}, commit: "d633176146e997"}

	if _, message := vol.health(); message != "main at d633176" {
		t.Errorf("a volume nothing demanded reports %q, want the commit alone", message)
	}
	vol.reportDemanded(demanded)
	vol.reportPulled(demanded.Add(time.Second))

	_, message := vol.health()
	want := "main at d633176, demanded 2025-09-04T15:33:20Z, pulled 2025-09-04T15:33:21Z"
	if message != want {
		t.Errorf("the report is %q, want %q", message, want)
	}
}

func TestARestartPullsEveryVolumeThatDoesNotPullNever(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		demandedVolume(t, n, "franchises", fileURL(source), "on-demand")

		want := commitFiles(t, source, map[string]string{"a.txt": "two"})
		again := restarted(t, n, true)
		again.mu.Lock()
		resumed := again.staged["franchises"]
		again.mu.Unlock()

		waitForCommit(t, resumed, want)
	})
}

// csiVolume returns a PersistentVolume of the driver whose name is the
// handle.
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

func annotated(pv *corev1.PersistentVolume, at string) *corev1.PersistentVolume {
	pv.Annotations = map[string]string{demandAnnotation: at}
	return pv
}

func TestADemandTheNodeCannotActOnDoesNothing(t *testing.T) {
	for _, c := range []struct {
		name string
		pv   *corev1.PersistentVolume
	}{
		{
			name: "a PersistentVolume of another driver",
			pv:   annotated(csiVolume("franchises", "other.example.com"), "2026-09-06T14:31:07Z"),
		},
		{
			name: "a PersistentVolume of no CSI driver",
			pv: annotated(&corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "franchises"},
			}, "2026-09-06T14:31:07Z"),
		},
		{
			name: "a PersistentVolume nothing demanded",
			pv:   csiVolume("franchises", driverName),
		},
		{
			name: "a handle this node does not hold",
			pv:   annotated(csiVolume("other", driverName), "2026-09-06T14:31:07Z"),
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, _ := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			vol := demandedVolume(t, n, "franchises", fileURL(source), "on-demand")
			before, _ := vol.commitAndFault()

			commitFiles(t, source, map[string]string{"a.txt": "two"})
			n.demands.read(t.Context(), c.pv)

			if commit, _ := vol.commitAndFault(); commit != before {
				t.Errorf("the volume moved to %s, want %s", commit, before)
			}
		})
	}
}

func TestTheDemandWatchEndsWithTheDriver(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		cluster(t, n).PrependWatchReactor("persistentvolumes",
			func(k8stesting.Action) (bool, watch.Interface, error) {
				return true, watch.NewFake(), nil
			})

		ctx, stop := context.WithCancel(t.Context())
		over := make(chan struct{})
		go func() {
			defer close(over)
			n.demands.run(ctx)
		}()
		// A watch that outlives its context blocks the bubble forever,
		// and synctest fails the test.
		stop()
		<-over
	})
}

func TestADriverOutsideAClusterWatchesNothing(t *testing.T) {
	outside := &demandWatch{}
	outside.run(t.Context())
}

// watchRestartsOf returns the git_csi_watch_restarts_total value for
// the kind, and false when no restart has been counted yet.
func watchRestartsOf(t *testing.T, registry *metrics, kind string) (float64, bool) {
	t.Helper()
	families, err := registry.registry.Gather()
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
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		// The fake clientset closes every watch at once, so the
		// informer keeps opening new ones.
		cluster(t, n).PrependWatchReactor("persistentvolumes",
			func(k8stesting.Action) (bool, watch.Interface, error) {
				closed := watch.NewFake()
				closed.Stop()
				return true, closed, nil
			})

		ctx, stop := context.WithCancel(t.Context())
		over := make(chan struct{})
		go func() {
			defer close(over)
			n.demands.run(ctx)
		}()

		// The reflector backs off for less than a minute before it
		// opens the next watch.
		time.Sleep(time.Minute)
		stop()
		<-over

		if count, found := watchRestartsOf(t, n.metrics, persistentVolumeKind); !found || count == 0 {
			t.Errorf("git_csi_watch_restarts_total reads %v (found: %v), want at least one restart", count, found)
		}
	})
}

func TestADeleteWithNoCopyOfThePersistentVolumeIsLogged(t *testing.T) {
	logs := &logbook{}
	n, _ := testNode(t, logs)

	n.demands.deleted(t.Context(), cache.DeletedFinalStateUnknown{Key: "franchises"})

	if !strings.Contains(logs.String(), `msg="The delete event carried no PersistentVolume"`) {
		t.Errorf("the log is %q, want the delete event with no PersistentVolume", logs)
	}
}
