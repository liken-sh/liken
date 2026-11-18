package main

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestTheComparisonKeyIsTheHostAndThePath(t *testing.T) {
	for _, c := range []struct {
		name string
		url  string
		key  string
	}{
		{name: "https", url: "https://code.example.com/data/x.git", key: "code.example.com/data/x"},
		{name: "ssh", url: "ssh://git@code.example.com/data/x.git", key: "code.example.com/data/x"},
		{name: "scp-like", url: "git@code.example.com:data/x.git", key: "code.example.com/data/x"},
		{name: "a port", url: "git://code.example.com:9418/data/x", key: "code.example.com/data/x"},
		{name: "an ssh port", url: "ssh://git@code.example.com:2222/data/x.git", key: "code.example.com/data/x"},
		{name: "no .git", url: "https://code.example.com/data/x", key: "code.example.com/data/x"},
		{name: "a trailing slash", url: "https://code.example.com/data/x/", key: "code.example.com/data/x"},
		{name: "a host in capitals", url: "https://Code.Example.COM/data/x.git", key: "code.example.com/data/x"},
		{name: "a file URL", url: "file:///srv/repos/x.git", key: "/srv/repos/x"},
		{name: "a host and no path", url: "https://code.example.com", key: "code.example.com"},
		{name: "nothing", url: "", key: ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := comparisonKey(c.url); got != c.key {
				t.Errorf("comparisonKey(%q) = %q, want %q", c.url, got, c.key)
			}
		})
	}
}

func TestTheRefRuleReadsTheFullRef(t *testing.T) {
	for _, c := range []struct {
		name     string
		pushed   string
		followed string
		matches  bool
	}{
		{name: "a branch", pushed: "refs/heads/main", followed: "main", matches: true},
		{name: "a tag", pushed: "refs/tags/v1", followed: "v1", matches: true},
		{name: "the name itself", pushed: "main", followed: "main", matches: true},
		{name: "another branch", pushed: "refs/heads/next", followed: "main", matches: false},
		{name: "a branch of the tag's name", pushed: "refs/heads/v1", followed: "v1", matches: true},
		{name: "a nested branch", pushed: "refs/heads/release/1", followed: "release", matches: false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := refMatches(c.pushed, c.followed); got != c.matches {
				t.Errorf("refMatches(%q, %q) = %v, want %v",
					c.pushed, c.followed, got, c.matches)
			}
		})
	}
}

// pushedTo is the push a forge sends for the URL and the branch.
func pushedTo(url, ref string) push {
	return push{ref: ref, urls: []string{url}}
}

func TestTheSecretScopesWhatAPushMarks(t *testing.T) {
	pushed := pushedTo("https://code.example.com/data/x.git", "refs/heads/main")
	following := map[string]string{
		"url": "https://code.example.com/data/x.git", "ref": "main",
		webhookSecretAttribute: "x-webhook",
	}

	for _, c := range []struct {
		name    string
		volume  func(*corev1.PersistentVolume)
		matches bool
	}{
		{name: "a volume the Secret scopes", volume: func(*corev1.PersistentVolume) {}, matches: true},
		{
			name: "a volume of another Secret",
			volume: func(pv *corev1.PersistentVolume) {
				pv.Spec.CSI.VolumeAttributes[webhookSecretAttribute] = "other-webhook"
			},
		},
		{
			name: "a volume that names no Secret",
			volume: func(pv *corev1.PersistentVolume) {
				delete(pv.Spec.CSI.VolumeAttributes, webhookSecretAttribute)
			},
		},
		{
			name: "a claim in another namespace",
			volume: func(pv *corev1.PersistentVolume) {
				pv.Spec.ClaimRef.Namespace = "other"
			},
		},
		{
			name:   "a volume bound to no claim",
			volume: func(pv *corev1.PersistentVolume) { pv.Spec.ClaimRef = nil },
		},
		{
			name: "a volume of another driver",
			volume: func(pv *corev1.PersistentVolume) {
				pv.Spec.CSI.Driver = "another.example.com"
			},
		},
		{
			name:   "a volume of no CSI driver",
			volume: func(pv *corev1.PersistentVolume) { pv.Spec.CSI = nil },
		},
		{
			name: "a writeable volume",
			volume: func(pv *corev1.PersistentVolume) {
				pv.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{
					corev1.ReadWriteOncePod,
				}
			},
		},
		{
			name: "a volume of both modes",
			volume: func(pv *corev1.PersistentVolume) {
				pv.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{
					corev1.ReadOnlyMany, corev1.ReadWriteOnce,
				}
			},
		},
		{
			name:   "a volume with no access mode",
			volume: func(pv *corev1.PersistentVolume) { pv.Spec.AccessModes = nil },
		},
		{
			name: "a volume of another repository",
			volume: func(pv *corev1.PersistentVolume) {
				pv.Spec.CSI.VolumeAttributes["url"] = "https://code.example.com/data/y.git"
			},
		},
		{
			name: "a volume that names no URL",
			volume: func(pv *corev1.PersistentVolume) {
				delete(pv.Spec.CSI.VolumeAttributes, "url")
			},
		},
		{
			name: "a volume that follows another ref",
			volume: func(pv *corev1.PersistentVolume) {
				pv.Spec.CSI.VolumeAttributes["ref"] = "next"
			},
		},
		{
			name: "a volume that names no ref",
			volume: func(pv *corev1.PersistentVolume) {
				delete(pv.Spec.CSI.VolumeAttributes, "ref")
			},
			matches: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			hooks := testWebhook(t, io.Discard)
			followingVolume(t, hooks, "x", "sites", maps(following))
			pv, err := hooks.client.CoreV1().PersistentVolumes().
				Get(t.Context(), "x", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("reading the PersistentVolume: %v", err)
			}
			c.volume(pv)

			got := matchesPush(pv, "sites", "x-webhook",
				comparisonKeys(pushed.urls), pushed.ref)
			if got != c.matches {
				t.Errorf("matchesPush = %v, want %v", got, c.matches)
			}
		})
	}
}

// maps returns a copy of from, so a case that edits it does not change
// the original.
func maps(from map[string]string) map[string]string {
	to := map[string]string{}
	for key, value := range from {
		to[key] = value
	}
	return to
}

func TestMarkAnnotatesEveryVolumeThePushNames(t *testing.T) {
	hooks := testWebhook(t, io.Discard)
	following := map[string]string{
		"url": "git@code.example.com:data/x.git", "ref": "main",
		webhookSecretAttribute: "x-webhook",
	}
	followingVolume(t, hooks, "x", "sites", maps(following))
	followingVolume(t, hooks, "x-again", "sites", maps(following))
	followingVolume(t, hooks, "y", "sites", map[string]string{
		"url": "https://code.example.com/data/y.git", webhookSecretAttribute: "x-webhook",
	})

	marked, err := hooks.mark(t.Context(), "sites", "x-webhook",
		pushedTo("https://code.example.com/data/x.git", "refs/heads/main"))

	if err != nil {
		t.Fatalf("marking the volumes: %v", err)
	}
	if marked != 2 {
		t.Errorf("mark returned %d, want 2", marked)
	}
	for _, name := range []string{"x", "x-again"} {
		if demandedAt(t, hooks, name) != webhookTime.Format(time.RFC3339Nano) {
			t.Errorf("the volume %s is not marked", name)
		}
	}
	if demandedAt(t, hooks, "y") != "" {
		t.Error("the volume of another repository is marked")
	}
}

func TestMarkReportsWhatItCouldNotWrite(t *testing.T) {
	logs := &logbook{}
	hooks := testWebhook(t, logs)
	followingVolume(t, hooks, "x", "sites", map[string]string{
		"url": "https://code.example.com/data/x.git", webhookSecretAttribute: "x-webhook",
	})
	hooks.client.(*fake.Clientset).PrependReactor("patch", "persistentvolumes",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("the API server refused the request")
		})

	marked, err := hooks.mark(t.Context(), "sites", "x-webhook",
		pushedTo("https://code.example.com/data/x.git", "refs/heads/main"))

	if err != nil {
		t.Fatalf("marking the volumes: %v", err)
	}
	if marked != 0 {
		t.Errorf("mark returned %d, want 0", marked)
	}
	if !strings.Contains(logs.String(), "The pull demand was not set on the PersistentVolume") {
		t.Errorf("the log is %q, want the volume that was not marked in it", logs)
	}
}

func TestMarkReturnsAListError(t *testing.T) {
	hooks := testWebhook(t, io.Discard)
	refused := errors.New("the API server refused the request")
	hooks.client.(*fake.Clientset).PrependReactor("list", "persistentvolumes",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, refused
		})

	marked, err := hooks.mark(t.Context(), "sites", "x-webhook",
		pushedTo("https://code.example.com/data/x.git", "refs/heads/main"))

	if !strings.Contains(err.Error(), refused.Error()) {
		t.Errorf("mark returned the error %v, want %v", err, refused)
	}
	if marked != 0 {
		t.Errorf("mark returned %d, want 0", marked)
	}
}

func TestAStartDemandsAPullOnEveryReadOnlyVolume(t *testing.T) {
	hooks := testWebhook(t, io.Discard)
	followingVolume(t, hooks, "x", "sites", map[string]string{
		"url": "https://code.example.com/data/x.git",
	})
	followingVolume(t, hooks, "y", "other", map[string]string{
		"url": "https://code.example.com/data/y.git",
	})
	writeVolume(t, hooks, &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "config"},
		Spec: corev1.PersistentVolumeSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOncePod},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{Driver: driverName, VolumeHandle: "config"},
			},
		},
	})
	writeVolume(t, hooks, &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "elsewhere"},
		Spec: corev1.PersistentVolumeSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{Driver: "another.example.com"},
			},
		},
	})

	hooks.demandAll(t.Context())

	for _, name := range []string{"x", "y"} {
		if demandedAt(t, hooks, name) != webhookTime.Format(time.RFC3339Nano) {
			t.Errorf("the read-only volume %s is not marked", name)
		}
	}
	for _, name := range []string{"config", "elsewhere"} {
		if demandedAt(t, hooks, name) != "" {
			t.Errorf("the volume %s is marked", name)
		}
	}
}

func TestAStartReportsWhatItCouldNotMark(t *testing.T) {
	logs := &logbook{}
	hooks := testWebhook(t, logs)
	followingVolume(t, hooks, "x", "sites", map[string]string{
		"url": "https://code.example.com/data/x.git",
	})
	hooks.client.(*fake.Clientset).PrependReactor("patch", "persistentvolumes",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("the API server refused the request")
		})

	hooks.demandAll(t.Context())

	if !strings.Contains(logs.String(), "The pull demand was not set on the PersistentVolume") {
		t.Errorf("the log is %q, want the volume that was not marked in it", logs)
	}
}

func TestAStartReportsAListItCannotRead(t *testing.T) {
	logs := &logbook{}
	hooks := testWebhook(t, logs)
	hooks.client.(*fake.Clientset).PrependReactor("list", "persistentvolumes",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("the API server refused the request")
		})

	hooks.demandAll(t.Context())

	if !strings.Contains(logs.String(), "The PersistentVolumes were not listed") {
		t.Errorf("the log is %q, want the listing that failed in it", logs)
	}
}

func TestAControllerWithNoClusterMarksNothingOnStart(t *testing.T) {
	logs := &logbook{}
	hooks := testWebhook(t, logs)
	hooks.client = nil

	hooks.demandAll(t.Context())

	if logs.String() != "" {
		t.Errorf("the log is %q, want nothing", logs)
	}
}
