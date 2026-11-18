package main

// This file finds the claim that a surface's socket belongs to, and
// the pods that hold that claim.
//
// A socket is named for its claim's UID, not for the claim's namespace
// and name, because a claim that is deleted and created again under
// the same name is a different allocation. A surface from the old pod
// must not match the new claim.
//
// Several pods can share one claim and one socket, so the compositor
// cannot tell which pod drew a surface. A region's selector matches
// only the labels that every pod of the claim has with the same value.

import (
	"errors"
	"maps"
	"net/http"
	"slices"
	"sync"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// ResourceClaimsPath is the collection of claims in all namespaces.
const ResourceClaimsPath = "/apis/resource.k8s.io/v1/resourceclaims"

type claimRef struct {
	Namespace string
	Name      string
}

// key returns "namespace/name", the form that status.surfaces uses,
// or an empty string when the claimRef has no name.
func (r claimRef) key() string {
	if r.Name == "" {
		return ""
	}
	return r.Namespace + "/" + r.Name
}

// claimIndex maps a claim's UID to its namespace and name. The API
// server reads a claim only by namespace and name, and has no field
// selector for a UID. So prepareClaim records each claim that this
// process prepares, and the index lists claims only for a UID that it
// has no entry for.
type claimIndex struct {
	client *apiclient.Client

	mu     sync.Mutex
	claims map[string]claimRef
}

func newClaimIndex(client *apiclient.Client) *claimIndex {
	return &claimIndex{client: client, claims: map[string]claimRef{}}
}

// remember does nothing on a nil index, which tests that prepare
// claims without a placement pass use.
func (i *claimIndex) remember(uid, namespace, name string) {
	if i == nil {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.claims[uid] = claimRef{Namespace: namespace, Name: name}
}

// claim returns an empty claimRef for a UID that no claim has.
func (i *claimIndex) claim(uid string) (claimRef, error) {
	if i == nil {
		return claimRef{}, nil
	}
	if held, known := i.lookup(uid); known {
		return held, nil
	}
	if err := i.refill(uid); err != nil {
		return claimRef{}, err
	}
	held, _ := i.lookup(uid)
	return held, nil
}

func (i *claimIndex) lookup(uid string) (claimRef, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	held, known := i.claims[uid]
	return held, known
}

// refill lists all claims and keeps the ones that have a CDI spec on
// this node. It runs when the index has no entry for a UID, which
// happens after the operator container restarts while the compositor
// keeps running: the surfaces still exist, but their prepare ran in
// the previous process.
//
// A UID that is not in the list is stored with an empty claimRef.
// A surface can outlive its claim, and without that entry every
// placement pass would list all claims again while the client stays
// connected.
func (i *claimIndex) refill(missing string) error {
	prepared := map[string]bool{}
	if err := eachPreparedSpec(func(claimUID string, _ cdiSpec) {
		prepared[claimUID] = true
	}); err != nil {
		return err
	}
	claims, err := listResourceClaims(i.client)
	if err != nil {
		return err
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	for _, claim := range claims {
		if !prepared[claim.Metadata.UID] {
			continue
		}
		i.claims[claim.Metadata.UID] = claimRef{
			Namespace: claim.Metadata.Namespace,
			Name:      claim.Metadata.Name,
		}
	}
	if _, resolved := i.claims[missing]; !resolved {
		i.claims[missing] = claimRef{}
	}
	return nil
}

// listResourceClaims lists the claims of every namespace, because a
// claim from any namespace can use a screen on this node.
func listResourceClaims(c *apiclient.Client) ([]ResourceClaim, error) {
	list := &ResourceClaimList{}
	if err := c.RequestJSON(http.MethodGet, ResourceClaimsPath, nil, list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// claimHolders describes the claim behind one surface. pods is
// sorted, and labels has the labels that all of the pods share.
type claimHolders struct {
	key    string
	pods   []string
	labels map[string]string
}

// holderReader caches claimHolders and this node's pods for one
// placement pass. A screen often has several surfaces of the same
// claim, and the cache reads each claim from the API server once per
// pass.
type holderReader struct {
	client *apiclient.Client
	node   string
	claims *claimIndex
	stores clusterStores

	read   map[string]claimHolders
	pods   map[string]Pod
	listed bool
}

func newHolderReader(client *apiclient.Client, node string, claims *claimIndex, stores clusterStores) *holderReader {
	return &holderReader{
		client: client,
		node:   node,
		claims: claims,
		stores: stores,
		read:   map[string]claimHolders{},
	}
}

func (h *holderReader) holders(uid string) (claimHolders, error) {
	if held, known := h.read[uid]; known {
		return held, nil
	}
	held, err := h.resolve(uid)
	if err != nil {
		return claimHolders{}, err
	}
	h.read[uid] = held
	return held, nil
}

// resolve reads the claim's status.reservedFor for its pods, and the
// pods for their labels.
//
// When the claim is deleted it returns an empty claimHolders, because
// a client can stay connected after its claim is deleted. A surface
// with no claim matches no region's selector, so a Layout does not
// show it and the default layout still does.
func (h *holderReader) resolve(uid string) (claimHolders, error) {
	ref, err := h.claims.claim(uid)
	if err != nil {
		return claimHolders{}, err
	}
	if ref.Name == "" {
		return claimHolders{}, nil
	}
	// The claim is read and not watched, because a claim has no field
	// that selects a node. A watch would send every claim in the
	// cluster to the operator on every node.
	claim, err := GetResourceClaim(h.client, ref.Namespace, ref.Name)
	if errors.Is(err, apiclient.ErrNotFound) {
		return claimHolders{}, nil
	}
	if err != nil {
		return claimHolders{}, err
	}
	held := claimHolders{key: ref.key()}
	for _, consumer := range claim.Status.ReservedFor {
		if consumer.Resource != podsResource {
			continue
		}
		pod, err := h.pod(ref.Namespace, consumer.Name)
		if err != nil {
			return claimHolders{}, err
		}
		if pod == nil {
			continue
		}
		held.pods = append(held.pods, pod.Metadata.key())
		if len(held.pods) == 1 {
			held.labels = maps.Clone(pod.Metadata.Labels)
			continue
		}
		held.labels = sharedLabels(held.labels, pod.Metadata.Labels)
	}
	// Sorting keeps the status field the same for the same pods in any
	// order of status.reservedFor.
	slices.Sort(held.pods)
	return held, nil
}

// pod returns nil when the pod is deleted. A pod that is not in this
// pass's list of the node's pods, such as one that started after the
// list, is read by name.
func (h *holderReader) pod(namespace, name string) (*Pod, error) {
	listed, err := h.onThisNode()
	if err != nil {
		return nil, err
	}
	if pod, running := listed[namespace+"/"+name]; running {
		return &pod, nil
	}
	pod, err := h.stores.pod(h.client, namespace, name)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil, nil
	}
	return pod, err
}

// onThisNode returns this node's pods by "namespace/name".
func (h *holderReader) onThisNode() (map[string]Pod, error) {
	if h.listed {
		return h.pods, nil
	}
	pods, err := h.stores.podsOn(h.client, h.node)
	if err != nil {
		return nil, err
	}
	h.pods = make(map[string]Pod, len(pods))
	for _, pod := range pods {
		h.pods[pod.Metadata.key()] = pod
	}
	h.listed = true
	return h.pods, nil
}

// sharedLabels returns the labels that both maps have with the same
// value. A region shows a claim's surfaces however many pods hold the
// claim, so a label of only one pod must not decide where they go.
func sharedLabels(held, also map[string]string) map[string]string {
	shared := map[string]string{}
	for key, value := range held {
		if carried, ok := also[key]; ok && carried == value {
			shared[key] = value
		}
	}
	return shared
}
