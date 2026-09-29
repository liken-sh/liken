package main

// The shared apiclient package sends every write, and every read that
// the watches do not answer. Each request carries the context of the
// pass that sends it (apiclient.Client.WithContext), so a pass that ends
// takes its requests with it. The shared client imports nothing from
// k8s.io, so the pod build links no client-go. Only the watches and the
// Lease use client-go (watch.go and leader.go).

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"

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

// inClusterBase is the address the in-cluster client reaches the API
// server at, from the two variables Kubernetes injects into every pod.
func inClusterBase() string {
	return "https://" + os.Getenv("KUBERNETES_SERVICE_HOST") + ":" + os.Getenv("KUBERNETES_SERVICE_PORT")
}

// The two content types the operator's requests send. A PATCH states
// its own, because the API server reads which patch dialect a request
// speaks from the Content-Type header alone. The metadata providers and
// Jellyfin take JSON too.
const (
	jsonContentType = "application/json"
	mergePatchType  = "application/merge-patch+json"
)

// drain reads whatever the caller left in the body, then closes it.
// Go returns a connection to its pool only when the body reaches
// EOF, so an early close costs a fresh connection and TLS handshake,
// and reaches the server as a hang-up on a request it answered.
const maxDrain = 4 << 20

func drain(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxDrain))
	_ = body.Close()
}

// Every API server answers /version, and the answer needs no RBAC
// rule, so it is the cheapest proof that the client reached the
// server it was configured for.
const versionPath = "/version"

// Version holds the one field of /version the operator reports.
type Version struct {
	GitVersion string `json:"gitVersion"`
}

func ServerVersion(ctx context.Context, client *apiclient.Client) (Version, error) {
	var version Version
	if err := client.WithContext(ctx).RequestJSON(http.MethodGet, versionPath, nil, &version); err != nil {
		return Version{}, err
	}
	return version, nil
}

// The collection paths. Libraries are listed and watched across every
// namespace and written back per namespace. The storage and the pods
// are ordinary core-group objects: a claim and a pod are namespaced,
// and a volume is not.
const (
	librariesPath = "/apis/" + libraryAPIVersion + "/libraries"
	// The Catalogs, listed and watched across every namespace and
	// written back per namespace, the same shape as the Libraries.
	catalogsPath = "/apis/" + libraryAPIVersion + "/catalogs"
	// The Players, listed and watched across every namespace,
	// read-only. A Player is media-operator's object, and this operator
	// reads the collection to find the screens delegated to it.
	playersPath = "/apis/" + playerAPIVersion + "/players"
	// The Plays of every namespace. The operator creates a Play in one
	// namespace and reads the whole collection back, because progress
	// is recorded for every Play in the cluster and not only for the
	// ones a screen of this operator's asked for.
	playsAllPath = "/apis/" + playerAPIVersion + "/plays"
	// The people, cluster-scoped, in the group people-operator serves.
	peoplePath = "/apis/" + personAPIVersion + "/people"
	// The MediaPreferences, cluster-scoped and read-only here, for the
	// household zone the screen pods carry.
	mediaPreferencesPath = "/apis/" + playerAPIVersion + "/mediapreferences"

	libraryPrefix = "/apis/" + libraryAPIVersion + "/namespaces/"
	corePrefix    = "/api/v1/namespaces/"
	volumesPath   = "/api/v1/persistentvolumes"
	podsAllPath   = "/api/v1/pods"

	// The StorageClasses, cluster-scoped in the storage group, read by
	// name for the provisioner behind the class a claim names.
	storageClassesPath = "/apis/storage.k8s.io/v1/storageclasses"

	// The slices behind the catalog Services, one in every namespace that
	// holds a Library.
	endpointSlicePrefix = "/apis/" + endpointSliceAPIVersion + "/namespaces/"
)

// catalogMemberSelector narrows the pod watch to the pods that hold a
// catalog agent, whatever kind of pod they are: the catalog pod, a
// running Job's pod, and a screen pod.
const catalogMemberSelector = memberLabelKey + "=" + memberLabelValue

func libraryPath(namespace, name string) string {
	return libraryPrefix + namespace + "/libraries/" + name
}

func catalogPath(namespace, name string) string {
	return libraryPrefix + namespace + "/catalogs/" + name
}

func claimPath(namespace, name string) string {
	return corePrefix + namespace + "/persistentvolumeclaims/" + name
}

func claimsPath(namespace string) string {
	return corePrefix + namespace + "/persistentvolumeclaims"
}

// playsPath is the plays collection of one namespace. A Play is
// created in the Player's namespace, which is the Library's namespace
// as well, so no reference this operator makes crosses a namespace.
func playsPath(namespace string) string {
	return "/apis/" + playerAPIVersion + "/namespaces/" + namespace + "/plays"
}

// playPath is one Play by name, which is where the operator patches
// the finalizer it holds on the Play.
func playPath(namespace, name string) string {
	return playsPath(namespace) + "/" + name
}

// personPath is one Person by name. A Person is cluster-scoped, so the
// path carries no namespace.
func personPath(name string) string {
	return peoplePath + "/" + name
}

func podsPath(namespace string) string {
	return corePrefix + namespace + "/pods"
}

func endpointSlicesPath(namespace string) string {
	return endpointSlicePrefix + namespace + "/endpointslices"
}

func servicesPath(namespace string) string {
	return corePrefix + namespace + "/services"
}

func configMapsPath(namespace string) string {
	return corePrefix + namespace + "/configmaps"
}

// PatchLibraryFinalizers writes a Library's finalizer list and
// answers with the resourceVersion the write produced, which a
// caller needs before it writes the same object again in one pass.
//
// It is a merge patch and not a replace, because a replace sends
// every field this program models and drops every field it does not,
// which would take a person's own labels and annotations off the
// Library. The resourceVersion inside the patch makes the write
// conditional the same way a replace is: a write that raced another
// answers apiclient.ErrConflict instead of clobbering it.
func PatchLibraryFinalizers(ctx context.Context, c *apiclient.Client, namespace, name, resourceVersion string, finalizers []string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"resourceVersion": resourceVersion,
			"finalizers":      finalizers,
		},
	})
	if err != nil {
		return "", err
	}
	var patched struct {
		Metadata ObjectMeta `json:"metadata"`
	}
	path := libraryPath(namespace, name)
	if err := c.WithContext(ctx).Request(http.MethodPatch, path, mergePatchType, body, &patched); err != nil {
		return "", err
	}
	return patched.Metadata.ResourceVersion, nil
}

// GetPersistentVolumeClaim reads the claim a Library names, for two
// answers: whether it is bound, and which volume it is bound to. An
// absent claim is apiclient.ErrNotFound, which the pass reports as the
// ClaimNotFound reason rather than as a failure. It also reads the
// catalog claim the operator provisions, to tell an existing one from
// none.
func GetPersistentVolumeClaim(ctx context.Context, c *apiclient.Client, namespace, name string) (*PersistentVolumeClaim, error) {
	claim := &PersistentVolumeClaim{}
	if err := c.WithContext(ctx).RequestJSON(http.MethodGet, claimPath(namespace, name), nil, claim); err != nil {
		return nil, err
	}
	return claim, nil
}

// CreatePersistentVolumeClaim provisions a catalog claim: a Library's,
// the catalog pod's, or a screen's. The operator creates one once and never
// updates it, because a claim's spec is immutable once it binds.
func CreatePersistentVolumeClaim(ctx context.Context, c *apiclient.Client, claim *PersistentVolumeClaim) (*PersistentVolumeClaim, error) {
	body, err := json.Marshal(claim)
	if err != nil {
		return nil, err
	}
	created := &PersistentVolumeClaim{}
	if err := c.WithContext(ctx).RequestJSON(http.MethodPost, claimsPath(claim.Metadata.Namespace), body, created); err != nil {
		return nil, err
	}
	return created, nil
}

// DeletePersistentVolumeClaim removes the catalog claim of a screen
// the scheduler cannot place. It is the one claim this operator deletes. An
// already-absent claim is success, because the operator deletes the claim to
// replace it and a delete that races another pass must not fail.
func DeletePersistentVolumeClaim(ctx context.Context, c *apiclient.Client, namespace, name string) error {
	err := c.WithContext(ctx).RequestJSON(http.MethodDelete, claimPath(namespace, name), nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// GetPersistentVolume reads the volume behind a bound claim, for what
// serves it. A PersistentVolume is cluster-scoped, so the path carries
// no namespace.
func GetPersistentVolume(ctx context.Context, c *apiclient.Client, name string) (*PersistentVolume, error) {
	volume := &PersistentVolume{}
	if err := c.WithContext(ctx).RequestJSON(http.MethodGet, volumesPath+"/"+name, nil, volume); err != nil {
		return nil, err
	}
	return volume, nil
}

// GetStorageClass reads the class a claim names, for its provisioner. A
// class the cluster does not serve is apiclient.ErrNotFound, which the caller reads
// as a class that is not per-node.
func GetStorageClass(ctx context.Context, c *apiclient.Client, name string) (*StorageClass, error) {
	class := &StorageClass{}
	if err := c.WithContext(ctx).RequestJSON(http.MethodGet, storageClassesPath+"/"+name, nil, class); err != nil {
		return nil, err
	}
	return class, nil
}

// ListStorageClasses reads every class the cluster serves, for the one class
// whose provisioner is per-node. The cache of a provider's dataset files
// needs that class whatever class the libraries use.
func ListStorageClasses(ctx context.Context, c *apiclient.Client) (*StorageClassList, error) {
	list := &StorageClassList{}
	if err := c.WithContext(ctx).RequestJSON(http.MethodGet, storageClassesPath, nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

// CreatePersistentVolume writes the volume a per-node claim binds to. The
// operator writes it before the claim, because the claim names it and no
// provisioner answers a claim of that class.
func CreatePersistentVolume(ctx context.Context, c *apiclient.Client, volume *PersistentVolume) (*PersistentVolume, error) {
	body, err := json.Marshal(volume)
	if err != nil {
		return nil, err
	}
	created := &PersistentVolume{}
	if err := c.WithContext(ctx).RequestJSON(http.MethodPost, volumesPath, body, created); err != nil {
		return nil, err
	}
	return created, nil
}

// uidPrecondition is the body of a delete that the API server refuses with
// a 409 unless the object holds this uid. An empty uid is no precondition,
// because the API server reads an empty uid as one no object holds.
func uidPrecondition(uid string) ([]byte, error) {
	if uid == "" {
		return nil, nil
	}
	return json.Marshal(map[string]any{
		"apiVersion":    "v1",
		"kind":          "DeleteOptions",
		"preconditions": map[string]string{"uid": uid},
	})
}

// DeletePersistentVolume removes a volume this operator wrote whose claim
// is gone. A volume that is already absent is success, because two
// passes may sweep the same volume.
//
// The delete names the uid of the copy the caller read. The copy can come
// from a watch's store, and a pass can delete a spent volume and write a
// fresh one of the same name before the store holds the fresh one. The
// API server refuses a delete whose uid is not the volume's with a 409,
// so the delete never takes the fresh volume, and the refusal is success.
func DeletePersistentVolume(ctx context.Context, c *apiclient.Client, name, uid string) error {
	body, err := uidPrecondition(uid)
	if err != nil {
		return err
	}
	err = c.WithContext(ctx).RequestJSON(http.MethodDelete, volumesPath+"/"+name, body, nil)
	if errors.Is(err, apiclient.ErrNotFound) || errors.Is(err, apiclient.ErrConflict) {
		return nil
	}
	return err
}

func GetPod(ctx context.Context, c *apiclient.Client, namespace, name string) (*Pod, error) {
	pod := &Pod{}
	if err := c.WithContext(ctx).RequestJSON(http.MethodGet, podsPath(namespace)+"/"+name, nil, pod); err != nil {
		return nil, err
	}
	return pod, nil
}

func CreatePod(ctx context.Context, c *apiclient.Client, pod *Pod) (*Pod, error) {
	body, err := json.Marshal(pod)
	if err != nil {
		return nil, err
	}
	created := &Pod{}
	if err := c.WithContext(ctx).RequestJSON(http.MethodPost, podsPath(pod.Metadata.Namespace), body, created); err != nil {
		return nil, err
	}
	return created, nil
}

// CreatePlay posts the Play one play request became. The API server
// mints the name from the prefix, because a person may start the same
// title twice and each start is its own Play.
func CreatePlay(ctx context.Context, c *apiclient.Client, play *Play) (*Play, error) {
	body, err := json.Marshal(play)
	if err != nil {
		return nil, err
	}
	created := &Play{}
	if err := c.WithContext(ctx).RequestJSON(http.MethodPost, playsPath(play.Metadata.Namespace), body, created); err != nil {
		return nil, err
	}
	return created, nil
}

// DeletePod removes one pod this operator stands. An
// already-absent pod is success, because the operator deletes a pod to
// replace it and a delete that races another pass must not fail.
func DeletePod(ctx context.Context, c *apiclient.Client, namespace, name string) error {
	err := c.WithContext(ctx).RequestJSON(http.MethodDelete, podsPath(namespace)+"/"+name, nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// ForceDeletePod deletes one pod with no grace period, so the API server
// removes it at once and waits for no kubelet. A pod on a node that is
// gone never finishes a graceful delete: it stays Terminating for as long
// as the node is away, and the claim it mounts stays with it. The heal
// uses this and nothing else does.
//
// The delete names the uid of the pod the caller read. The copy comes
// from a watch's store, and the heal stands a new pod of the same name
// in the pass that deletes the old one, so a later pass can read the old
// copy before the watch delivers the delete. The API server refuses a
// delete whose uid is not the pod's with a 409, and the answer is false:
// the pod under that name is not the one the caller read.
func ForceDeletePod(ctx context.Context, c *apiclient.Client, namespace, name, uid string) (bool, error) {
	body, err := uidPrecondition(uid)
	if err != nil {
		return false, err
	}
	err = c.WithContext(ctx).RequestJSON(http.MethodDelete, podsPath(namespace)+"/"+name+"?gracePeriodSeconds=0", body, nil)
	if errors.Is(err, apiclient.ErrNotFound) || errors.Is(err, apiclient.ErrConflict) {
		return false, nil
	}
	return err == nil, err
}

// GetService reads one Service by name from the API server. The pass
// reads the Services it stands from the watch, and through this only
// where the memo says the watch's copy is not current (objectcache.go).
func GetService(ctx context.Context, c *apiclient.Client, namespace, name string) (*Service, error) {
	service := &Service{}
	if err := c.WithContext(ctx).RequestJSON(http.MethodGet, servicesPath(namespace)+"/"+name, nil, service); err != nil {
		return nil, err
	}
	return service, nil
}

func CreateService(ctx context.Context, c *apiclient.Client, service *Service) (*Service, error) {
	body, err := json.Marshal(service)
	if err != nil {
		return nil, err
	}
	created := &Service{}
	path := servicesPath(service.Metadata.Namespace)
	if err := c.WithContext(ctx).RequestJSON(http.MethodPost, path, body, created); err != nil {
		return nil, err
	}
	return created, nil
}

// DeleteService removes one Service this operator stands. An absent Service
// is success, because a Catalog that drops a block the operator stood a
// Service for is reconciled on every pass.
func DeleteService(ctx context.Context, c *apiclient.Client, namespace, name string) error {
	err := c.WithContext(ctx).RequestJSON(http.MethodDelete, servicesPath(namespace)+"/"+name, nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// GetConfigMap reads one ConfigMap by name. The pass reads the people
// ConfigMap from the watch instead; a test reads it through this.
func GetConfigMap(ctx context.Context, c *apiclient.Client, namespace, name string) (*ConfigMap, error) {
	configMap := &ConfigMap{}
	if err := c.WithContext(ctx).RequestJSON(http.MethodGet, configMapsPath(namespace)+"/"+name, nil, configMap); err != nil {
		return nil, err
	}
	return configMap, nil
}

func CreateConfigMap(ctx context.Context, c *apiclient.Client, configMap *ConfigMap) (*ConfigMap, error) {
	body, err := json.Marshal(configMap)
	if err != nil {
		return nil, err
	}
	created := &ConfigMap{}
	if err := c.WithContext(ctx).RequestJSON(http.MethodPost, configMapsPath(configMap.Metadata.Namespace), body, created); err != nil {
		return nil, err
	}
	return created, nil
}

func UpdateConfigMap(ctx context.Context, c *apiclient.Client, configMap *ConfigMap) (*ConfigMap, error) {
	body, err := json.Marshal(configMap)
	if err != nil {
		return nil, err
	}
	written := &ConfigMap{}
	path := configMapsPath(configMap.Metadata.Namespace) + "/" + configMap.Metadata.Name
	if err := c.WithContext(ctx).RequestJSON(http.MethodPut, path, body, written); err != nil {
		return nil, err
	}
	return written, nil
}

// UpdateService writes the whole Service back. The resourceVersion in
// the body makes the write conditional, so a Service that changed
// underneath answers apiclient.ErrConflict, and the next pass reads it again.
func UpdateService(ctx context.Context, c *apiclient.Client, service *Service) (*Service, error) {
	body, err := json.Marshal(service)
	if err != nil {
		return nil, err
	}
	written := &Service{}
	path := servicesPath(service.Metadata.Namespace) + "/" + service.Metadata.Name
	if err := c.WithContext(ctx).RequestJSON(http.MethodPut, path, body, written); err != nil {
		return nil, err
	}
	return written, nil
}

// PatchPlayMetadata writes the metadata this operator owns on a Play:
// the finalizer list always, because taking a finalizer off is a write
// of the shorter list, and the owner references and the annotations
// where the caller states them. It is a merge patch, so every other
// field media-operator wrote survives the write, and the
// resourceVersion makes it conditional the same way a replace is.
func PatchPlayMetadata(ctx context.Context, c *apiclient.Client, namespace, name, resourceVersion string, metadata ObjectMeta) (string, error) {
	patch := map[string]any{
		"resourceVersion": resourceVersion,
		"finalizers":      metadata.Finalizers,
	}
	if metadata.OwnerReferences != nil {
		patch["ownerReferences"] = metadata.OwnerReferences
	}
	if metadata.Annotations != nil {
		patch["annotations"] = metadata.Annotations
	}
	return patchMetadata(ctx, c, playPath(namespace, name), patch)
}

// PatchPersonFinalizers writes a Person's finalizer list. The operator
// holds one until every namespace's progress store has dropped that
// person's rows.
func PatchPersonFinalizers(ctx context.Context, c *apiclient.Client, name, resourceVersion string, finalizers []string) (string, error) {
	return patchMetadata(ctx, c, personPath(name), map[string]any{
		"resourceVersion": resourceVersion,
		"finalizers":      finalizers,
	})
}

// patchMetadata sends one conditional merge patch of an object's
// metadata and answers with the resourceVersion the write produced,
// which a caller needs before it writes the same object again in one
// pass.
func patchMetadata(ctx context.Context, c *apiclient.Client, path string, metadata map[string]any) (string, error) {
	body, err := json.Marshal(map[string]any{"metadata": metadata})
	if err != nil {
		return "", err
	}
	var patched struct {
		Metadata ObjectMeta `json:"metadata"`
	}
	if err := c.WithContext(ctx).Request(http.MethodPatch, path, mergePatchType, body, &patched); err != nil {
		return "", err
	}
	return patched.Metadata.ResourceVersion, nil
}
