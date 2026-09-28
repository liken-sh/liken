package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// cluster is the fake API server the node's events and arming share.
func cluster(t *testing.T, answering *node) *fake.Clientset {
	t.Helper()
	return answering.events.client.(*fake.Clientset)
}

// boundVolume writes the PersistentVolume that carries the handle and the
// claim it is bound to.
func boundVolume(t *testing.T, answering *node, handle, class string) {
	t.Helper()
	held := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: handle},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       driverName,
					VolumeHandle: handle,
				},
			},
			ClaimRef: &corev1.ObjectReference{Namespace: "home", Name: "config"},
		},
		Status: corev1.PersistentVolumeStatus{Phase: corev1.VolumeBound},
	}
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "home", Name: "config"},
	}
	if class != "" {
		claim.Spec.VolumeAttributesClassName = &class
	}
	client := cluster(t, answering)
	if _, err := client.CoreV1().PersistentVolumes().
		Create(t.Context(), held, metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the PersistentVolume: %v", err)
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("home").
		Create(t.Context(), claim, metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the claim: %v", err)
	}
}

// nameClass names the class on the claim that boundVolume wrote.
func nameClass(t *testing.T, answering *node, class string) {
	t.Helper()
	claims := cluster(t, answering).CoreV1().PersistentVolumeClaims("home")
	claim, err := claims.Get(t.Context(), "config", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the claim: %v", err)
	}
	claim.Spec.VolumeAttributesClassName = &class
	if _, err := claims.Update(t.Context(), claim, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("naming the class on the claim: %v", err)
	}
}

// attributesClass writes a VolumeAttributesClass of the driver it names.
func attributesClass(t *testing.T, answering *node, name, driver string) {
	t.Helper()
	class := &storagev1.VolumeAttributesClass{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		DriverName: driver,
	}
	if _, err := cluster(t, answering).StorageV1().VolumeAttributesClasses().
		Create(t.Context(), class, metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the class: %v", err)
	}
}

// armingClass writes a class of this driver with the parameters plan 05
// reads.
func armingClass(t *testing.T, answering *node, name string, parameters map[string]string) {
	t.Helper()
	class := &storagev1.VolumeAttributesClass{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		DriverName: driverName,
		Parameters: parameters,
	}
	if _, err := cluster(t, answering).StorageV1().VolumeAttributesClasses().
		Create(t.Context(), class, metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the class: %v", err)
	}
}

// armedVolume stages and publishes a writeable volume whose claim names a
// class with the parameters, and waits for the class to arm it.
func armedVolume(
	t *testing.T, answering *node, id, url string, parameters map[string]string,
) *volume {
	t.Helper()
	boundVolume(t, answering, id, "config-eager")
	armingClass(t, answering, "config-eager", parameters)
	held, _ := stagedWriteable(t, answering, id, url)
	waitForArmed(t, held, true)
	return held
}

// waitForArmed waits until the volume reports the armed state, or fails on
// the deadline.
func waitForArmed(t *testing.T, held *volume, want bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, armed, _ := held.reading(); armed == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the volume is not armed: %v within 30s", want)
}

func TestAClassOfThisDriverArmsTheVolume(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	boundVolume(t, answering, "config", "config-eager")
	attributesClass(t, answering, "config-eager", driverName)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})

	published, _ := stagedWriteable(t, answering, "config", fileURL(source))
	waitForArmed(t, published, true)

	claim, _, _ := published.reading()
	if claim.namespace != "home" || claim.name != "config" {
		t.Errorf("the volume names the claim %+v, want home/config", claim)
	}
	published.mu.Lock()
	class := published.class
	published.mu.Unlock()
	if class != "config-eager" {
		t.Errorf("the volume names the class %q, want config-eager", class)
	}
}

func TestAClassOfAnotherDriverLeavesTheVolumeUnarmed(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	boundVolume(t, answering, "config", "other-storage")
	attributesClass(t, answering, "other-storage", "other.example.com")
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, _ := stagedWriteable(t, answering, "config", fileURL(source))

	waitForClaim(t, published)
	if _, armed, _ := published.reading(); armed {
		t.Error("a class of another driver armed the volume")
	}
}

// waitForClaim waits until the loop has found the claim, which is the
// first thing a pass does.
func waitForClaim(t *testing.T, held *volume) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if claim, _, _ := held.reading(); claim.name != "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the loop did not find the claim within 30s")
}

func TestTheClaimIsFoundThroughTheVolumeHandle(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	boundVolume(t, answering, "config", "")

	found, err := answering.arms.claimOf(t.Context(), "config")
	if err != nil {
		t.Fatalf("claimOf: %v", err)
	}
	if found != (claimReference{namespace: "home", name: "config"}) {
		t.Errorf("claimOf answered %+v, want home/config", found)
	}
	if _, err := answering.arms.claimOf(t.Context(), "other"); err == nil {
		t.Error("claimOf answered no error for a handle no PersistentVolume carries")
	}
}

func TestTheClaimIsNotFoundWhenTheClusterCannotAnswer(t *testing.T) {
	for _, c := range []struct {
		name  string
		stand func(t *testing.T, answering *node)
		says  string
	}{
		{
			name: "a PersistentVolume of another driver",
			stand: func(t *testing.T, answering *node) {
				held := &corev1.PersistentVolume{
					ObjectMeta: metav1.ObjectMeta{Name: "config"},
					Spec: corev1.PersistentVolumeSpec{
						PersistentVolumeSource: corev1.PersistentVolumeSource{
							CSI: &corev1.CSIPersistentVolumeSource{
								Driver:       "other.example.com",
								VolumeHandle: "config",
							},
						},
					},
				}
				if _, err := cluster(t, answering).CoreV1().PersistentVolumes().
					Create(t.Context(), held, metav1.CreateOptions{}); err != nil {
					t.Fatalf("writing the PersistentVolume: %v", err)
				}
			},
			says: "carries the handle",
		},
		{
			name: "a PersistentVolume bound to no claim",
			stand: func(t *testing.T, answering *node) {
				held := &corev1.PersistentVolume{
					ObjectMeta: metav1.ObjectMeta{Name: "config"},
					Spec: corev1.PersistentVolumeSpec{
						PersistentVolumeSource: corev1.PersistentVolumeSource{
							CSI: &corev1.CSIPersistentVolumeSource{
								Driver:       driverName,
								VolumeHandle: "config",
							},
						},
					},
				}
				if _, err := cluster(t, answering).CoreV1().PersistentVolumes().
					Create(t.Context(), held, metav1.CreateOptions{}); err != nil {
					t.Fatalf("writing the PersistentVolume: %v", err)
				}
			},
			says: "bound to no claim",
		},
		{
			name: "an API server that refuses the list",
			stand: func(t *testing.T, answering *node) {
				cluster(t, answering).PrependReactor("list", "persistentvolumes",
					func(k8stesting.Action) (bool, runtime.Object, error) {
						return true, nil, errors.New("the api server said no")
					})
			},
			says: "the api server said no",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			answering, _ := testNode(t, io.Discard)
			c.stand(t, answering)
			_, err := answering.arms.claimOf(t.Context(), "config")
			if err == nil {
				t.Fatal("claimOf answered no error")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("claimOf said %q, want %q in it", err, c.says)
			}
		})
	}
}

func TestTheClassInForceIsTheOneTheStatusCarries(t *testing.T) {
	asked, current := "asked", "current"
	for _, c := range []struct {
		name  string
		claim *corev1.PersistentVolumeClaim
		want  string
	}{
		{
			name:  "a claim that names none",
			claim: &corev1.PersistentVolumeClaim{},
			want:  "",
		},
		{
			name: "a claim whose spec names one",
			claim: &corev1.PersistentVolumeClaim{
				Spec: corev1.PersistentVolumeClaimSpec{VolumeAttributesClassName: &asked},
			},
			want: "asked",
		},
		{
			name: "a claim whose status carries one",
			claim: &corev1.PersistentVolumeClaim{
				Spec: corev1.PersistentVolumeClaimSpec{VolumeAttributesClassName: &asked},
				Status: corev1.PersistentVolumeClaimStatus{
					CurrentVolumeAttributesClassName: &current,
				},
			},
			want: "current",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := className(c.claim); got != c.want {
				t.Errorf("className answered %q, want %q", got, c.want)
			}
		})
	}
}

// volumeNamed is a staged volume of the handle. Every volume the
// driver holds has the attributes its stage parsed and the pod its
// publish named, so a volume a test hands the arming loop has them too.
func volumeNamed(id string) *volume {
	return &volume{
		id:         id,
		attributes: &attributes{url: "file:///forge/" + id, ref: "main"},
		kind:       writeableVolume,
		pod:        podReference{name: "writer", namespace: "home"},
	}
}

func TestAClassTheNodeCannotReadIsAnError(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	gone := "gone"
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "home", Name: "config"},
		Spec:       corev1.PersistentVolumeClaimSpec{VolumeAttributesClassName: &gone},
	}

	err := answering.arms.read(t.Context(), volumeNamed("config"),
		claimReference{namespace: "home", name: "config"}, claim)
	if err == nil || !strings.Contains(err.Error(), "the class gone was not read") {
		t.Errorf("read answered %v, want the class that was not read", err)
	}
}

func TestAClaimOfAnotherNameArmsNothing(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	held := volumeNamed("config")
	other := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "home", Name: "other"},
	}

	if err := answering.arms.read(t.Context(), held,
		claimReference{namespace: "home", name: "config"}, other); err != nil {
		t.Fatalf("read: %v", err)
	}
	if claim, _, _ := held.reading(); claim.name != "" {
		t.Errorf("a claim of another name armed the volume for %+v", claim)
	}
}

// followArming runs the volume's arming loop until the test ends, and
// returns the channel that closes when the loop does.
func followArming(t *testing.T, answering *node, ctx context.Context, held *volume) <-chan struct{} {
	t.Helper()
	over := make(chan struct{})
	go func() {
		defer close(over)
		answering.arms.follow(ctx, held)
	}()
	return over
}

func TestTheLoopReadsTheClaimAgainWhileThereIsNone(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	ctx, stop := context.WithCancel(t.Context())
	over := followArming(t, answering, ctx, volumeNamed("config"))

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && strings.Count(logs.String(), "the claim was not found") < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	<-over
	if got := strings.Count(logs.String(), "the claim was not found"); got < 2 {
		t.Errorf("the log says the claim was not found %d times, want at least 2 (%q)", got, logs)
	}
}

func TestAClaimThatArrivesAfterTheVolumeArmsIt(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	boundVolume(t, answering, "config", "config-eager")
	attributesClass(t, answering, "config-eager", driverName)
	claims := cluster(t, answering).CoreV1().PersistentVolumeClaims("home")
	claim, err := claims.Get(t.Context(), "config", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the claim: %v", err)
	}
	if err := claims.Delete(t.Context(), "config", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("deleting the claim: %v", err)
	}
	// The retry is long, so only the watch can carry the new claim.
	answering.arms.retry = 30 * time.Second
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, _ := stagedWriteable(t, answering, "config", fileURL(source))
	time.Sleep(100 * time.Millisecond)

	claim.ResourceVersion = ""
	if _, err := claims.Create(t.Context(), claim, metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the claim: %v", err)
	}

	waitForArmed(t, published, true)
}

func TestAClassThatArrivesAfterTheClaimNamesItArmsTheVolume(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	boundVolume(t, answering, "config", "config-eager")
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, _ := stagedWriteable(t, answering, "config", fileURL(source))
	time.Sleep(100 * time.Millisecond)

	attributesClass(t, answering, "config-eager", driverName)

	waitForArmed(t, published, true)
}

func TestTheArmingLoopEndsWithTheDriver(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	boundVolume(t, answering, "config", "")
	ctx, stop := context.WithCancel(t.Context())
	over := followArming(t, answering, ctx, volumeNamed("config"))

	stop()
	select {
	case <-over:
	case <-time.After(30 * time.Second):
		t.Fatal("the loop did not end with the driver")
	}
}

func TestADeletedClaimLeavesTheVolumeArmed(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	sent := watch.NewFake()
	cluster(t, answering).PrependWatchReactor("persistentvolumeclaims",
		func(k8stesting.Action) (bool, watch.Interface, error) {
			return true, sent, nil
		})
	held := armedVolume(t, answering, "config",
		fileURL(bareRemote(t, map[string]string{"a.txt": "one"})), nil)
	armingClass(t, answering, "config-later", nil)

	// The deleted claim names no class, so a read of it would unarm the
	// volume and post an Event that says so. The claim after it names
	// another class of this driver, and the informer hands the reads
	// its events in order, so once that class is in force the delete
	// has been through the handlers. The test catches a handler that
	// arms from a delete only when the reads take the deleted copy
	// before the next one replaces it in the slot, so it can pass
	// where such a handler exists.
	sent.Delete(&corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "home", Name: "config"},
	})
	later := "config-later"
	sent.Modify(&corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "home", Name: "config"},
		Spec:       corev1.PersistentVolumeClaimSpec{VolumeAttributesClassName: &later},
	})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && classOf(held) != later {
		time.Sleep(10 * time.Millisecond)
	}

	if class := classOf(held); class != later {
		t.Fatalf("the volume is armed by %q, want %q", class, later)
	}
	if posted := eventsWithReason(t, answering, reasonUnarmed); len(posted) != 0 {
		t.Errorf("a deleted claim unarmed the volume: %v", posted)
	}
}

// classOf is the class the volume last read from its claim.
func classOf(held *volume) string {
	held.mu.Lock()
	defer held.mu.Unlock()
	return held.class
}

// waitForEvents waits until the node has posted the number of Events
// for the reason, and returns what it posted by the deadline. The
// volume reports its state before the node posts the Events, so a test
// that reads the Events waits for them, not for the state.
func waitForEvents(t *testing.T, answering *node, reason string, want int) []corev1.Event {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if posted := eventsWithReason(t, answering, reason); len(posted) >= want {
			return posted
		}
		time.Sleep(10 * time.Millisecond)
	}
	return eventsWithReason(t, answering, reason)
}

func TestTheLoopReadsTheClaimAgainWhenItChanges(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	// The retry is long, so the watch is what carries the change here.
	answering.arms.retry = 30 * time.Second
	boundVolume(t, answering, "config", "")
	attributesClass(t, answering, "config-eager", driverName)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, _ := stagedWriteable(t, answering, "config", fileURL(source))
	waitForClaim(t, published)

	nameClass(t, answering, "config-eager")

	armed := waitForEvents(t, answering, reasonArmed, 2)
	if len(armed) != 2 {
		t.Fatalf("the change posted %v, want one Event on the pod and one on the claim", armed)
	}
	kinds := armed[0].InvolvedObject.Kind + " " + armed[1].InvolvedObject.Kind
	if !strings.Contains(kinds, "Pod") || !strings.Contains(kinds, "PersistentVolumeClaim") {
		t.Errorf("the events are on %q, want the pod and the claim", kinds)
	}
	if armed[0].Message != "armed by the class config-eager" {
		t.Errorf("the event says %q", armed[0].Message)
	}
}

func TestTheVolumeThatLosesItsClassSaysSo(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	held := volumeNamed("config")
	claim := claimReference{namespace: "home", name: "config"}

	answering.armed(t.Context(), held, claim, "config-eager", &policy{}, "")
	answering.armed(t.Context(), held, claim, "", nil, "")

	unarmed := []corev1.Event{}
	for _, posted := range eventsOf(t, answering) {
		if posted.Reason == reasonUnarmed {
			unarmed = append(unarmed, posted)
		}
	}
	if len(unarmed) != 2 {
		t.Fatalf("the change posted %v, want one Event on the pod and one on the claim", unarmed)
	}
	if unarmed[0].Message != "unarmed: the claim names no class of "+driverName {
		t.Errorf("the event says %q", unarmed[0].Message)
	}
}

func TestADriverOutsideAClusterArmsNothing(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	answering.arms.client = nil
	answering.mu.Lock()
	defer answering.mu.Unlock()
	held := &volume{id: "config"}
	answering.arm(held)
	answering.disarm(held)
	if got := len(answering.armings); got != 0 {
		t.Errorf("the node holds %d arming loops, want 0", got)
	}
}

// waitForCondition waits until the volume's condition carries the text.
func waitForCondition(t *testing.T, held *volume, says string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, message := held.report(); strings.Contains(message, says) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, message := held.report()
	t.Fatalf("the condition is %q within 30s, want %q in it", message, says)
}

func TestAClassTheDriverCannotReadArmsNothing(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	held := armedVolume(t, answering, "config",
		fileURL(bareRemote(t, map[string]string{"a.txt": "one"})), nil)

	// A class's parameters are immutable, so a claim takes new rules by
	// naming another class.
	armingClass(t, answering, "config-broken", map[string]string{quiesceParameter: "1s"})
	nameClass(t, answering, "config-broken")

	waitForArmed(t, held, false)
	want := "the class config-broken is not valid: push.quiesce: 1s is shorter than 5s"
	waitForCondition(t, held, want)
	if held.policyNow() != nil {
		t.Error("a class the driver cannot read left the volume armed")
	}
	unarmed := waitForEvents(t, answering, reasonUnarmed, 2)
	if len(unarmed) != 2 {
		t.Fatalf("the change posted %v, want one Event on the pod and one on the claim", unarmed)
	}
	if unarmed[0].Message != "unarmed: "+want {
		t.Errorf("the event says %q, want %q", unarmed[0].Message, "unarmed: "+want)
	}
}
