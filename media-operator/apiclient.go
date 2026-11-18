package main

// Every write, and every read that must see this program's own last
// write, goes through the apiclient package. The watches read through
// client-go (clusterwatch.go and informer.WatchOne).

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// serviceAccountDir is a variable so that a test can point it at a
// directory of its own.
var serviceAccountDir = apiclient.ServiceAccountDir

// inClusterClient builds a client from the pod's ServiceAccount. timeout
// limits one request, and zero uses apiclient's default.
func inClusterClient(timeout time.Duration) (*apiclient.Client, error) {
	return apiclient.InCluster(apiclient.InClusterOptions{ServiceAccountDir: serviceAccountDir, Timeout: timeout})
}

// The apply content type says YAML, and the API server accepts the JSON
// body that this client sends because JSON is valid YAML.
const (
	applyContentType = "application/apply-patch+yaml"
	mergePatchType   = "application/merge-patch+json"
)

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

// DeletePlay deletes a Play. The operator deletes a Finished Play when
// its retention window has passed, and a Play that a newer Play on the
// same Player replaces. A Play that is already gone is not an error,
// because a person can delete it first.
func DeletePlay(c *apiclient.Client, namespace, name string) error {
	err := c.RequestJSON(http.MethodDelete, playPath(namespace, name), nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// PatchPlayFinalizers sets a Play's finalizer list and returns the new
// resourceVersion. The patch includes the resourceVersion that the
// caller read, so when another writer changed the Play first, the call
// returns apiclient.ErrConflict and does not overwrite that change. A
// Play that does not exist returns apiclient.ErrNotFound.
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

func GetRemote(c *apiclient.Client, namespace, name string) (*Remote, error) {
	remote := &Remote{}
	if err := c.RequestJSON(http.MethodGet, remotePath(namespace, name), nil, remote); err != nil {
		return nil, err
	}
	return remote, nil
}

// PutRemoteStatus replaces a Remote's status through the status
// subresource, so the write cannot change the Remote's spec. The
// operator writes a Remote only this way.
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

// PatchPodLabels merge-patches labels into a running pod's
// metadata.labels. It keeps the pod's other labels and changes no other
// field. A pod that does not exist returns apiclient.ErrNotFound.
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

// DeletePod deletes a pod. A pod that is already gone is not an error,
// so two deletes that race both succeed.
func DeletePod(c *apiclient.Client, namespace, name string) error {
	err := c.RequestJSON(http.MethodDelete, podsPath(namespace)+"/"+name, nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// ApplyDisplayOverride server-side applies a Display's spec.override as
// this operator's field manager. A nil override applies an empty spec,
// so the API server removes the override that this manager set, and the
// panel returns to its normal state.
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

// ApplyReceiverSession server-side applies a Receiver's status.session
// as this operator's field manager. A nil session applies an empty
// status, so the API server removes the session that this manager set.
//
// The session is in status because a status write does not change
// metadata.generation. equipment-operator sends a Receiver's settings
// to the receiver again for each new generation, so a session in spec
// would resend the settings on every change to the session.
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

// ReleaseReceiverSpecSession applies an empty spec as this operator's
// field manager, so the API server removes any spec.session that this
// manager set. Fields that another manager set, such as the inputs and
// topics, stay.
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

// DeleteResourceClaim deletes a ResourceClaim. A claim that is already
// gone is not an error.
func DeleteResourceClaim(c *apiclient.Client, namespace, name string) error {
	err := c.RequestJSON(http.MethodDelete, claimsPath(namespace)+"/"+name, nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}
