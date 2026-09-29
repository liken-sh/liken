package main

// The shared apiclient package sends every write, and every read that
// must include this program's own last write. The watches read each
// collection through client-go (clusterwatch.go and namedwatch.go).

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// serviceAccountDir is a variable so a test points it at a directory
// it controls.
var serviceAccountDir = apiclient.ServiceAccountDir

// inClusterClient builds the client from the pod's environment and its
// ServiceAccount.
func inClusterClient() (*apiclient.Client, error) {
	return apiclient.InCluster(apiclient.InClusterOptions{ServiceAccountDir: serviceAccountDir})
}

// The two content types this client sends besides JSON. An apply is a
// body the API server reads as a partial object under the caller's
// field manager. The apply media type is named for YAML and accepts
// JSON, because YAML is its superset.
//
// The merge patch type writes one metadata field and leaves every other
// field of the object alone.
const (
	applyContentType = "application/apply-patch+yaml"
	mergePatchType   = "application/merge-patch+json"
)

// The collection paths. The watches read each collection through
// client-go (clusterwatch.go), and this client writes each object
// under its namespace's path, and under its group's path for a
// cluster-scoped kind.
const (
	playsPath       = "/apis/" + mediaAPIVersion + "/plays"
	playersPath     = "/apis/" + mediaAPIVersion + "/players"
	remotesAllPath  = "/apis/" + mediaAPIVersion + "/remotes"
	keymapsPath     = "/apis/" + mediaAPIVersion + "/keymaps"
	mediaPrefsPath  = "/apis/" + mediaAPIVersion + "/mediapreferences"
	mediaPrefix     = "/apis/" + mediaAPIVersion + "/namespaces/"
	claimPrefix     = "/apis/" + claimAPIVersion + "/namespaces/"
	slicesPath      = "/apis/" + claimAPIVersion + "/resourceslices"
	displaysPath    = "/apis/" + displayAPIVersion + "/displays"
	receiversPath   = "/apis/" + receiverAPIVersion + "/receivers"
	peripheralsPath = "/apis/" + peripheralAPIVersion + "/peripherals"
	podPrefix       = "/api/v1/namespaces/"
)

func playPath(namespace, name string) string {
	return mediaPrefix + namespace + "/plays/" + name
}

func playerPath(namespace, name string) string {
	return mediaPrefix + namespace + "/players/" + name
}

func remotesPath(namespace string) string {
	return mediaPrefix + namespace + "/remotes"
}

func remotePath(namespace, name string) string {
	return remotesPath(namespace) + "/" + name
}

func claimsPath(namespace string) string {
	return claimPrefix + namespace + "/resourceclaims"
}

func podsPath(namespace string) string {
	return podPrefix + namespace + "/pods"
}

func GetPlay(c *apiclient.Client, namespace, name string) (*Play, error) {
	play := &Play{}
	if err := c.RequestJSON(http.MethodGet, playPath(namespace, name), nil, play); err != nil {
		return nil, err
	}
	return play, nil
}

// DeletePlay removes one Play once its window after finishing has passed.
// It is the one object a person created that this operator deletes, and it
// deletes it only in that one state. An already-absent Play is success,
// because a person may delete a Finished Play before its window ends.
func DeletePlay(c *apiclient.Client, namespace, name string) error {
	err := c.RequestJSON(http.MethodDelete, playPath(namespace, name), nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// PatchPlayFinalizers writes one Play's finalizer list and answers the
// resourceVersion the write left behind. The resourceVersion the caller
// read the Play at rides in the patch, so a write another program made
// first answers apiclient.ErrConflict rather than overwriting it. An absent Play
// answers apiclient.ErrNotFound.
func PatchPlayFinalizers(c *apiclient.Client, namespace, name, resourceVersion string, finalizers []string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"resourceVersion": resourceVersion,
			"finalizers":      finalizers,
		},
	})
	if err != nil {
		return "", err
	}
	patched := &Play{}
	if err := c.Request(http.MethodPatch, playPath(namespace, name),
		mergePatchType, body, patched); err != nil {
		return "", err
	}
	return patched.Metadata.ResourceVersion, nil
}

func GetPlayer(c *apiclient.Client, namespace, name string) (*Player, error) {
	player := &Player{}
	if err := c.RequestJSON(http.MethodGet, playerPath(namespace, name), nil, player); err != nil {
		return nil, err
	}
	return player, nil
}

// GetRemote reads one Remote by name in a namespace, the name a
// Player's spec.remotes entry carries.
func GetRemote(c *apiclient.Client, namespace, name string) (*Remote, error) {
	remote := &Remote{}
	if err := c.RequestJSON(http.MethodGet, remotePath(namespace, name), nil, remote); err != nil {
		return nil, err
	}
	return remote, nil
}

// PutRemoteStatus writes the Remote's status subresource, the one write
// path this operator has onto a Remote. The subresource split keeps it
// from ever rewriting the device selector a person declared.
func PutRemoteStatus(c *apiclient.Client, remote *Remote) (*Remote, error) {
	body, err := json.Marshal(remote)
	if err != nil {
		return nil, err
	}
	written := &Remote{}
	path := remotePath(remote.Metadata.Namespace, remote.Metadata.Name) + "/status"
	if err := c.RequestJSON(http.MethodPut, path, body, written); err != nil {
		return nil, err
	}
	return written, nil
}

func GetResourceClaim(c *apiclient.Client, namespace, name string) (*ResourceClaim, error) {
	claim := &ResourceClaim{}
	if err := c.RequestJSON(http.MethodGet, claimsPath(namespace)+"/"+name, nil, claim); err != nil {
		return nil, err
	}
	return claim, nil
}

func CreateResourceClaim(c *apiclient.Client, claim *ResourceClaim) (*ResourceClaim, error) {
	body, err := json.Marshal(claim)
	if err != nil {
		return nil, err
	}
	created := &ResourceClaim{}
	if err := c.RequestJSON(http.MethodPost, claimsPath(claim.Metadata.Namespace), body, created); err != nil {
		return nil, err
	}
	return created, nil
}

func GetPod(c *apiclient.Client, namespace, name string) (*Pod, error) {
	pod := &Pod{}
	if err := c.RequestJSON(http.MethodGet, podsPath(namespace)+"/"+name, nil, pod); err != nil {
		return nil, err
	}
	return pod, nil
}

func CreatePod(c *apiclient.Client, pod *Pod) (*Pod, error) {
	body, err := json.Marshal(pod)
	if err != nil {
		return nil, err
	}
	created := &Pod{}
	if err := c.RequestJSON(http.MethodPost, podsPath(pod.Metadata.Namespace), body, created); err != nil {
		return nil, err
	}
	return created, nil
}

// PatchPodLabels adds labels to one pod and leaves the labels it
// already carries alone, which is what a merge patch of
// metadata.labels does. The operator writes one label this way, the
// ending, so the patch names no other field of a pod the kubelet is
// running. An absent pod answers apiclient.ErrNotFound, because a pod
// that has gone is nothing to label.
func PatchPodLabels(c *apiclient.Client, namespace, name string, labels map[string]string) error {
	body, err := json.Marshal(map[string]any{
		"metadata": map[string]any{"labels": labels},
	})
	if err != nil {
		return err
	}
	return c.Request(http.MethodPatch, podsPath(namespace)+"/"+name,
		mergePatchType, body, nil)
}

// DeletePod removes one playback pod. An already-absent pod is
// success, because the graceful recreate deletes the pod before it
// creates the replacement, and a delete that races another pass must
// not fail.
func DeletePod(c *apiclient.Client, namespace, name string) error {
	err := c.RequestJSON(http.MethodDelete, podsPath(namespace)+"/"+name, nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// ApplyDisplayOverride writes spec.override and nothing else,
// under this operator's own field manager. A nil override applies an
// empty spec, and the API server then removes the block this manager
// owns, which is how the panel comes back.
func ApplyDisplayOverride(c *apiclient.Client, name string, override *DisplayOverride) error {
	body, err := json.Marshal(&displayApply{
		APIVersion: displayAPIVersion,
		Kind:       "Display",
		Metadata:   ObjectMeta{Name: name},
		Spec:       DisplaySpec{Override: override},
	})
	if err != nil {
		return err
	}
	path := displaysPath + "/" + name + "?fieldManager=" + applyFieldManager
	return c.Request(http.MethodPatch, path, applyContentType, body, nil)
}

// ApplyReceiverSession writes status.session and nothing else, under
// this operator's own field manager on the status subresource. A nil
// session applies an empty status, and the API server then removes the
// block this manager owns. That is how the equipment is released.
//
// The session is status and not spec because a status write changes no
// metadata.generation. The equipment operator reads a new generation as
// a new statement of its settings, so a session in spec made every
// active or awake flip send the settings again.
func ApplyReceiverSession(c *apiclient.Client, name string, session *ReceiverSession) error {
	body, err := json.Marshal(&receiverStatusApply{
		APIVersion: receiverAPIVersion,
		Kind:       "Receiver",
		Metadata:   ObjectMeta{Name: name},
		Status:     receiverSessionStatus{Session: session},
	})
	if err != nil {
		return err
	}
	path := receiversPath + "/" + name + "/status?fieldManager=" + applyFieldManager
	return c.Request(http.MethodPatch, path, applyContentType, body, nil)
}

// ReleaseReceiverSpecSession applies an empty spec under this
// operator's field manager, and the API server then removes the
// spec.session this manager owns. A field another manager owns stays,
// so the apply never removes the cluster owner's inputs or topics.
func ReleaseReceiverSpecSession(c *apiclient.Client, name string) error {
	body, err := json.Marshal(&receiverSpecApply{
		APIVersion: receiverAPIVersion,
		Kind:       "Receiver",
		Metadata:   ObjectMeta{Name: name},
	})
	if err != nil {
		return err
	}
	path := receiversPath + "/" + name + "?fieldManager=" + applyFieldManager
	return c.Request(http.MethodPatch, path, applyContentType, body, nil)
}

// DeleteResourceClaim removes one playback claim. The operator deletes
// a claim only when a Player reshaped it, so the recreate builds the
// claim the current Player produces. An already-absent claim is
// success.
func DeleteResourceClaim(c *apiclient.Client, namespace, name string) error {
	err := c.RequestJSON(http.MethodDelete, claimsPath(namespace)+"/"+name, nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}
