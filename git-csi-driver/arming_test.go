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

// Cluster is the fake API server the node's events and arming share.
func cluster(t *testing.T, answering *node) *fake.Clientset {
	t.Helper()
	return answering.events.client.(*fake.Clientset)
}

// BoundVolume writes the PersistentVolume that carries the handle and the
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

// AttributesClass writes a VolumeAttributesClass of the driver it names.
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

// WaitForArmed waits until the volume reports the armed state, or fails on
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

	published, _ := writeableVolume(t, answering, "config", fileURL(source))
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
	published, _ := writeableVolume(t, answering, "config", fileURL(source))

	waitForClaim(t, published)
	if _, armed, _ := published.reading(); armed {
		t.Error("a class of another driver armed the volume")
	}
}

// WaitForClaim waits until the loop has found the claim, which is the
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

func TestTheArmingReportsWhatItCannotRead(t *testing.T) {
	for _, c := range []struct {
		name  string
		class string
		says  string
	}{
		{name: "a claim that is not there", says: "the claim was not read"},
		{name: "a class that is not there", class: "gone", says: "the class was not read"},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs := &logbook{}
			answering, _ := testNode(t, logs)
			if c.class != "" {
				boundVolume(t, answering, "config", c.class)
			}
			answering.arms.read(t.Context(), &volume{id: "config"},
				claimReference{namespace: "home", name: "config"})
			if !strings.Contains(logs.String(), c.says) {
				t.Errorf("the log is %q, want %q in it", logs, c.says)
			}
		})
	}
}

func TestThePassRestsWhenTheClusterRefusesTheWatch(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	boundVolume(t, answering, "config", "")
	cluster(t, answering).PrependWatchReactor("persistentvolumeclaims",
		func(k8stesting.Action) (bool, watch.Interface, error) {
			return true, nil, errors.New("the api server said no")
		})

	answering.arms.pass(t.Context(), &volume{id: "config"})
	if !strings.Contains(logs.String(), "the claim is not watched") {
		t.Errorf("the log is %q, want the refused watch in it", logs)
	}
}

func TestThePassRestsWhenThereIsNoClaim(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	answering.arms.pass(t.Context(), &volume{id: "config"})
	if !strings.Contains(logs.String(), "the claim was not found") {
		t.Errorf("the log is %q, want the missing claim in it", logs)
	}
}

func TestThePassEndsWhenTheWatchEnds(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	answering.arms.resync = 30 * time.Second
	boundVolume(t, answering, "config", "")
	ended := watch.NewFake()
	cluster(t, answering).PrependWatchReactor("persistentvolumeclaims",
		func(k8stesting.Action) (bool, watch.Interface, error) {
			return true, ended, nil
		})

	over := make(chan struct{})
	go func() {
		defer close(over)
		answering.arms.pass(t.Context(), &volume{id: "config"})
	}()
	ended.Stop()
	select {
	case <-over:
	case <-time.After(30 * time.Second):
		t.Fatal("the pass did not end when the watch did")
	}
}

func TestThePassEndsWithTheDriver(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	answering.arms.resync = 30 * time.Second
	boundVolume(t, answering, "config", "")
	cluster(t, answering).PrependWatchReactor("persistentvolumeclaims",
		func(k8stesting.Action) (bool, watch.Interface, error) {
			return true, watch.NewFake(), nil
		})

	ctx, stop := context.WithCancel(t.Context())
	over := make(chan struct{})
	go func() {
		defer close(over)
		answering.arms.follow(ctx, &volume{id: "config"})
	}()
	stop()
	select {
	case <-over:
	case <-time.After(30 * time.Second):
		t.Fatal("the loop did not end with the driver")
	}
}

func TestTheLoopReadsTheClaimAgainWhenItChanges(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	// The resync is long, so the watch is what carries the change here.
	answering.arms.resync = 30 * time.Second
	boundVolume(t, answering, "config", "")
	attributesClass(t, answering, "config-eager", driverName)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, _ := writeableVolume(t, answering, "config", fileURL(source))
	waitForClaim(t, published)

	claim, err := cluster(t, answering).CoreV1().PersistentVolumeClaims("home").
		Get(t.Context(), "config", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the claim: %v", err)
	}
	class := "config-eager"
	claim.Spec.VolumeAttributesClassName = &class
	if _, err := cluster(t, answering).CoreV1().PersistentVolumeClaims("home").
		Update(t.Context(), claim, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("naming the class on the claim: %v", err)
	}

	waitForArmed(t, published, true)
	armed := []corev1.Event{}
	for _, posted := range eventsOf(t, answering) {
		if posted.Reason == reasonArmed {
			armed = append(armed, posted)
		}
	}
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
	held := &volume{id: "config", pod: podReference{name: "writer", namespace: "home"}}
	claim := claimReference{namespace: "home", name: "config"}

	answering.armed(t.Context(), held, claim, "config-eager", true)
	answering.armed(t.Context(), held, claim, "", false)

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
