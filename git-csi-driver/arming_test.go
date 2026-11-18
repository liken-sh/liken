package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	kevents "github.com/liken-sh/liken/kubernetes/events"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// cluster returns the fake clientset that the node's arming and demand
// watch read from.
func cluster(t *testing.T, n *node) *fake.Clientset {
	t.Helper()
	return n.events.client.(*fake.Clientset)
}

// boundVolume creates a PersistentVolume with the CSI handle, bound to
// the claim home/config, and creates that claim. A non-empty class goes
// in the claim's spec.
func boundVolume(t *testing.T, n *node, handle, class string) {
	t.Helper()
	pv := &corev1.PersistentVolume{
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
	client := cluster(t, n)
	if _, err := client.CoreV1().PersistentVolumes().
		Create(t.Context(), pv, metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the PersistentVolume: %v", err)
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("home").
		Create(t.Context(), claim, metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the claim: %v", err)
	}
}

// nameClass sets the class in the spec of the claim that boundVolume
// created.
func nameClass(t *testing.T, n *node, class string) {
	t.Helper()
	claims := cluster(t, n).CoreV1().PersistentVolumeClaims("home")
	claim, err := claims.Get(t.Context(), "config", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the claim: %v", err)
	}
	claim.Spec.VolumeAttributesClassName = &class
	if _, err := claims.Update(t.Context(), claim, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("naming the class on the claim: %v", err)
	}
}

// attributesClass creates a VolumeAttributesClass that belongs to the
// given driver.
func attributesClass(t *testing.T, n *node, name, driver string) {
	t.Helper()
	class := &storagev1.VolumeAttributesClass{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		DriverName: driver,
	}
	if _, err := cluster(t, n).StorageV1().VolumeAttributesClasses().
		Create(t.Context(), class, metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the class: %v", err)
	}
}

// createClass creates a VolumeAttributesClass of this driver with the
// given parameters.
func createClass(t *testing.T, n *node, name string, parameters map[string]string) {
	t.Helper()
	class := &storagev1.VolumeAttributesClass{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		DriverName: driverName,
		Parameters: parameters,
	}
	if _, err := cluster(t, n).StorageV1().VolumeAttributesClasses().
		Create(t.Context(), class, metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the class: %v", err)
	}
}

// armedVolume stages and publishes a writeable volume whose claim names
// a class with the parameters, and waits until the class has armed it.
func armedVolume(
	t *testing.T, n *node, id, url string, parameters map[string]string,
) *volume {
	t.Helper()
	boundVolume(t, n, id, "config-eager")
	createClass(t, n, "config-eager", parameters)
	vol, _ := stagedWriteable(t, n, id, url)
	waitForArmed(t, vol, true)
	return vol
}

// waitForArmed waits until every goroutine in the synctest bubble is
// blocked, then fails the test unless the volume's armed state is want.
func waitForArmed(t *testing.T, vol *volume, want bool) {
	t.Helper()
	synctest.Wait()
	if _, armed, _ := vol.gaugeValues(); armed != want {
		t.Fatalf("the volume is armed: %v, want %v", armed, want)
	}
}

func TestAClassOfThisDriverArmsTheVolume(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		boundVolume(t, n, "config", "config-eager")
		attributesClass(t, n, "config-eager", driverName)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})

		published, _ := stagedWriteable(t, n, "config", fileURL(source))
		waitForArmed(t, published, true)

		claim, _, _ := published.gaugeValues()
		if claim.namespace != "home" || claim.name != "config" {
			t.Errorf("the volume names the claim %+v, want home/config", claim)
		}
		published.mu.Lock()
		class := published.class
		published.mu.Unlock()
		if class != "config-eager" {
			t.Errorf("the volume names the class %q, want config-eager", class)
		}
	})
}

func TestAClassOfAnotherDriverLeavesTheVolumeUnarmed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		boundVolume(t, n, "config", "other-storage")
		attributesClass(t, n, "other-storage", "other.example.com")
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		published, _ := stagedWriteable(t, n, "config", fileURL(source))

		waitForClaim(t, published)
		if _, armed, _ := published.gaugeValues(); armed {
			t.Error("the volume is armed, want it unarmed by a class of another driver")
		}
	})
}

// waitForClaim waits until the bubble is blocked, then fails the test
// unless the loop has found the volume's claim.
func waitForClaim(t *testing.T, vol *volume) {
	t.Helper()
	synctest.Wait()
	if claim, _, _ := vol.gaugeValues(); claim.name == "" {
		t.Fatal("the loop did not find the claim")
	}
}

func TestClaimOfFindsTheClaimThroughTheVolumeHandle(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	boundVolume(t, n, "config", "")

	found, err := n.arming.claimOf(t.Context(), "config")
	if err != nil {
		t.Fatalf("finding the claim through the volume handle: %v", err)
	}
	if found != (claimReference{namespace: "home", name: "config"}) {
		t.Errorf("claimOf returned %+v, want home/config", found)
	}
	if _, err := n.arming.claimOf(t.Context(), "other"); err == nil {
		t.Error("claimOf returned no error for a handle that no PersistentVolume has, want one")
	}
}

func TestClaimOfReturnsAnErrorWhenNoClaimCanBeFound(t *testing.T) {
	for _, c := range []struct {
		name      string
		setup     func(t *testing.T, n *node)
		wantError string
	}{
		{
			name: "a PersistentVolume of another driver",
			setup: func(t *testing.T, n *node) {
				pv := &corev1.PersistentVolume{
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
				if _, err := cluster(t, n).CoreV1().PersistentVolumes().
					Create(t.Context(), pv, metav1.CreateOptions{}); err != nil {
					t.Fatalf("writing the PersistentVolume: %v", err)
				}
			},
			wantError: "has the volume handle",
		},
		{
			name: "a PersistentVolume bound to no claim",
			setup: func(t *testing.T, n *node) {
				pv := &corev1.PersistentVolume{
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
				if _, err := cluster(t, n).CoreV1().PersistentVolumes().
					Create(t.Context(), pv, metav1.CreateOptions{}); err != nil {
					t.Fatalf("writing the PersistentVolume: %v", err)
				}
			},
			wantError: "bound to no claim",
		},
		{
			name: "an API server that refuses the list",
			setup: func(t *testing.T, n *node) {
				cluster(t, n).PrependReactor("list", "persistentvolumes",
					func(k8stesting.Action) (bool, runtime.Object, error) {
						return true, nil, errors.New("the API server refused the request")
					})
			},
			wantError: "the API server refused the request",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, _ := testNode(t, io.Discard)
			c.setup(t, n)
			_, err := n.arming.claimOf(t.Context(), "config")
			if err == nil {
				t.Fatal("claimOf returned no error, want one")
			}
			if !strings.Contains(err.Error(), c.wantError) {
				t.Errorf("claimOf returned %q, want %q in it", err, c.wantError)
			}
		})
	}
}

func TestTheClassInForceIsTheStatusClassOrElseTheSpecClass(t *testing.T) {
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
			name: "a claim whose status has one",
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
				t.Errorf("className returned %q, want %q", got, c.want)
			}
		})
	}
}

// volumeNamed returns a staged writeable volume with the id. It sets the
// attributes and pod that a real stage and publish set, which the
// arming loop reads.
func volumeNamed(id string) *volume {
	return &volume{
		id:         id,
		attributes: &attributes{url: "file:///forge/" + id, ref: "main"},
		kind:       writeableVolume,
		pod:        podReference{name: "writer", namespace: "home"},
	}
}

func TestAClassTheNodeCannotReadIsAnError(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	gone := "gone"
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "home", Name: "config"},
		Spec:       corev1.PersistentVolumeClaimSpec{VolumeAttributesClassName: &gone},
	}

	err := n.arming.read(t.Context(), volumeNamed("config"),
		claimReference{namespace: "home", name: "config"}, claim)
	if !apierrors.IsNotFound(err) {
		t.Errorf("read returned %v, want the NotFound error for the class gone", err)
	}
}

func TestAClaimOfAnotherNameArmsNothing(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	vol := volumeNamed("config")
	other := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "home", Name: "other"},
	}

	if err := n.arming.read(t.Context(), vol,
		claimReference{namespace: "home", name: "config"}, other); err != nil {
		t.Fatalf("reading a claim of another name: %v", err)
	}
	if claim, _, _ := vol.gaugeValues(); claim.name != "" {
		t.Errorf("the volume is armed for the claim %+v, want it unarmed by a claim of another name", claim)
	}
}

// followArming runs the arming loop for the volume in a goroutine until
// ctx ends, and returns a channel that closes when the loop returns.
func followArming(t *testing.T, n *node, ctx context.Context, vol *volume) <-chan struct{} {
	t.Helper()
	over := make(chan struct{})
	go func() {
		defer close(over)
		n.arming.watchClaim(ctx, vol)
	}()
	return over
}

func TestTheLoopReadsTheClaimAgainWhileThereIsNone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := &logbook{}
		n, _ := testNode(t, logs)
		ctx, stop := context.WithCancel(t.Context())
		over := followArming(t, n, ctx, volumeNamed("config"))

		// The first lookup finds no claim, and the retry looks again.
		time.Sleep(n.arming.retry)
		synctest.Wait()
		stop()
		<-over
		if got := strings.Count(logs.String(), "The PersistentVolumeClaim of the volume was not found"); got < 2 {
			t.Errorf("the log has the claim-not-found line %d times, want at least 2 (%q)", got, logs)
		}
	})
}

func TestAClaimThatArrivesAfterTheVolumeArmsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		boundVolume(t, n, "config", "config-eager")
		attributesClass(t, n, "config-eager", driverName)
		claims := cluster(t, n).CoreV1().PersistentVolumeClaims("home")
		claim, err := claims.Get(t.Context(), "config", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("reading the claim: %v", err)
		}
		if err := claims.Delete(t.Context(), "config", metav1.DeleteOptions{}); err != nil {
			t.Fatalf("deleting the claim: %v", err)
		}
		// The retry is long, so only the watch can deliver the new claim.
		n.arming.retry = 30 * time.Second
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		published, _ := stagedWriteable(t, n, "config", fileURL(source))
		// The loop looks for the claim and finds none before the test
		// creates it.
		synctest.Wait()

		claim.ResourceVersion = ""
		if _, err := claims.Create(t.Context(), claim, metav1.CreateOptions{}); err != nil {
			t.Fatalf("writing the claim: %v", err)
		}

		waitForArmed(t, published, true)
	})
}

func TestAClassThatArrivesAfterTheClaimNamesItArmsTheVolume(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		boundVolume(t, n, "config", "config-eager")
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		published, _ := stagedWriteable(t, n, "config", fileURL(source))
		// The loop reads the claim and finds that its class does not
		// exist yet.
		synctest.Wait()

		attributesClass(t, n, "config-eager", driverName)

		// The loop reads the class again after the retry.
		time.Sleep(n.arming.retry)
		waitForArmed(t, published, true)
	})
}

func TestTheArmingLoopEndsWithTheDriver(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		boundVolume(t, n, "config", "")
		ctx, stop := context.WithCancel(t.Context())
		over := followArming(t, n, ctx, volumeNamed("config"))

		// A loop that outlives its context leaves a goroutine blocked in
		// the bubble, and synctest fails the test.
		stop()
		<-over
	})
}

func TestADeletedClaimLeavesTheVolumeArmed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		sent := watch.NewFake()
		cluster(t, n).PrependWatchReactor("persistentvolumeclaims",
			func(k8stesting.Action) (bool, watch.Interface, error) {
				return true, sent, nil
			})
		vol := armedVolume(t, n, "config",
			fileURL(bareRemote(t, map[string]string{"a.txt": "one"})), nil)
		createClass(t, n, "config-later", nil)

		// The deleted copy has no class, so reading it would unarm the
		// volume and post an Event. The informer delivers events in
		// order, so once config-later is in force the delete has passed
		// through the handlers. A handler that offers the delete fails
		// this test only when armFromClaims receives the deleted copy before the
		// next copy replaces it.
		sent.Delete(&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Namespace: "home", Name: "config"},
		})
		later := "config-later"
		sent.Modify(&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Namespace: "home", Name: "config"},
			Spec:       corev1.PersistentVolumeClaimSpec{VolumeAttributesClassName: &later},
		})
		synctest.Wait()

		if class := classOf(vol); class != later {
			t.Fatalf("the volume is armed by %q, want %q", class, later)
		}
		if posted := eventsWithReason(t, n, reasonUnarmed); len(posted) != 0 {
			t.Errorf("deleting the claim posted the unarmed Events %v, want none", posted)
		}
	})
}

// classOf returns the class that the volume last read from its claim.
func classOf(vol *volume) string {
	vol.mu.Lock()
	defer vol.mu.Unlock()
	return vol.class
}

// waitForEvents waits until the bubble is blocked, then returns the
// Events that the node has posted with the reason. The volume records
// its state before the node posts the Events, so a test that reads the
// Events has to wait for the posts as well.
func waitForEvents(t *testing.T, n *node, reason string) []kevents.Event {
	t.Helper()
	synctest.Wait()
	return eventsWithReason(t, n, reason)
}

func TestTheLoopReadsTheClaimAgainWhenItChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		// The retry is long, so only the watch can deliver the change.
		n.arming.retry = 30 * time.Second
		boundVolume(t, n, "config", "")
		attributesClass(t, n, "config-eager", driverName)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		published, _ := stagedWriteable(t, n, "config", fileURL(source))
		waitForClaim(t, published)

		nameClass(t, n, "config-eager")

		armed := waitForEvents(t, n, reasonArmed)
		if len(armed) != 2 {
			t.Fatalf("the change posted %v, want one Event on the pod and one on the claim", armed)
		}
		kinds := armed[0].InvolvedObject.Kind + " " + armed[1].InvolvedObject.Kind
		if !strings.Contains(kinds, "Pod") || !strings.Contains(kinds, "PersistentVolumeClaim") {
			t.Errorf("the Events are on %q, want the Pod and the PersistentVolumeClaim", kinds)
		}
		if !strings.Contains(armed[0].Message, "config-eager") {
			t.Errorf("the Event message is %q, want it to name the class config-eager", armed[0].Message)
		}
	})
}

func TestAVolumeThatLosesItsClassPostsAnUnarmedEventOnThePodAndClaim(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		vol := volumeNamed("config")
		claim := claimReference{namespace: "home", name: "config"}

		n.armed(t.Context(), vol, claim, "config-eager", &policy{}, "")
		n.armed(t.Context(), vol, claim, "", nil, "")

		unarmed := []kevents.Event{}
		for _, posted := range eventsOf(t, n) {
			if posted.Reason == reasonUnarmed {
				unarmed = append(unarmed, posted)
			}
		}
		if len(unarmed) != 2 {
			t.Fatalf("the change posted %v, want one Event on the pod and one on the claim", unarmed)
		}
		if want := "Unarmed because the claim sets no VolumeAttributesClass of " + driverName; unarmed[0].Message != want {
			t.Errorf("the Event message is %q, want %q", unarmed[0].Message, want)
		}
	})
}

func TestADriverOutsideAClusterArmsNothing(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	n.arming.client = nil
	n.mu.Lock()
	defer n.mu.Unlock()
	vol := &volume{id: "config"}
	n.arm(vol)
	n.disarm(vol)
	if got := len(n.armingCancels); got != 0 {
		t.Errorf("the node has %d arming loops, want 0", got)
	}
}

// waitForCondition waits until the bubble is blocked, then fails the
// test unless the volume's health report contains the text.
func waitForCondition(t *testing.T, vol *volume, text string) {
	t.Helper()
	synctest.Wait()
	if _, message := vol.health(); !strings.Contains(message, text) {
		t.Fatalf("the health report is %q, want %q in it", message, text)
	}
}

func TestAnInvalidClassUnarmsTheVolume(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		vol := armedVolume(t, n, "config",
			fileURL(bareRemote(t, map[string]string{"a.txt": "one"})), nil)

		// A class's parameters are immutable, so a claim gets new rules
		// by naming a different class.
		createClass(t, n, "config-broken", map[string]string{quiesceParameter: "1s"})
		nameClass(t, n, "config-broken")

		waitForArmed(t, vol, false)
		want := "the VolumeAttributesClass config-broken is not valid: push.quiesce: 1s is shorter than 5s"
		waitForCondition(t, vol, want)
		if vol.policyNow() != nil {
			t.Error("the volume is armed, want it unarmed by an invalid class")
		}
		unarmed := waitForEvents(t, n, reasonUnarmed)
		if len(unarmed) != 2 {
			t.Fatalf("the change posted %v, want one Event on the pod and one on the claim", unarmed)
		}
		if !strings.HasSuffix(unarmed[0].Message, want) {
			t.Errorf("the Event message is %q, want it to end with %q", unarmed[0].Message, want)
		}
	})
}
