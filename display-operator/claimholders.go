package main

// The claim behind a surface's socket, and the pods that hold it.
//
// A socket is named for its claim's UID and not for its namespace and
// name, because a claim that is deleted and recreated under the same
// name is a different grant, and a surface from the old pod must not
// be read as the new claim's. The API server serves a claim by
// namespace and name and has no read by UID, so this file keeps the
// index from UID to both: prepareClaim fills it, because that is the
// one call that holds all three, and a miss refills it once from the
// CDI specs on disk and one listing of every claim. In the steady
// state a pass costs one claim read per surface and one pod listing
// on this node, and no listing of claims at all.
//
// The labels a region's selector reads are the ones every holder of
// the claim carries with the same value. Pods that share one claim
// share one socket, so the compositor cannot tell their surfaces
// apart, and the labels the pass reports are the ones that are true
// of every pod that could have drawn it.

import (
	"errors"
	"maps"
	"net/http"
	"slices"
	"sync"
)

// The claim collection across every namespace, which is what the
// refill below lists. One claim is read from its own namespaced path,
// in GetResourceClaim.
const ResourceClaimsPath = "/apis/resource.k8s.io/v1/resourceclaims"

// claimRef is one claim as the API server serves it, by namespace and
// name.
type claimRef struct {
	Namespace string
	Name      string
}

// The claim as status.surfaces names it, which is the form a person
// reads and passes to kubectl.
func (r claimRef) key() string {
	if r.Name == "" {
		return ""
	}
	return r.Namespace + "/" + r.Name
}

// claimIndex answers which claim one UID names.
//
// The socket a surface arrives on carries the claim's UID, and the
// API server serves a claim by namespace and name. There is no field
// selector for a UID, so the operator keeps the pair itself: a
// prepare records it, and a UID no prepare of this process recorded
// is filled in by one listing. A pass over the claims this process
// prepared costs no request at all.
type claimIndex struct {
	client *Client

	mu     sync.Mutex
	claims map[string]claimRef
}

func newClaimIndex(client *Client) *claimIndex {
	return &claimIndex{client: client, claims: map[string]claimRef{}}
}

// remember records the pair a prepare call read. A plugin built with
// no index records nothing, which is every test that drives a prepare
// with no placement pass behind it.
func (i *claimIndex) remember(uid, namespace, name string) {
	if i == nil {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.claims[uid] = claimRef{Namespace: namespace, Name: name}
}

// claim names the claim one UID belongs to, and answers an empty
// claim for a UID no claim carries.
func (i *claimIndex) claim(uid string) (claimRef, error) {
	if i == nil {
		return claimRef{}, nil
	}
	if held, known := i.held(uid); known {
		return held, nil
	}
	if err := i.refill(uid); err != nil {
		return claimRef{}, err
	}
	held, _ := i.held(uid)
	return held, nil
}

func (i *claimIndex) held(uid string) (claimRef, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	held, known := i.claims[uid]
	return held, known
}

// refill fills the index from the specs this driver wrote and one
// listing of the claims. It runs when the index does not hold a UID,
// which is the operator's container restarting under a compositor
// that kept running: the surfaces are still there, and the prepare
// that named them ran in the process before this one.
//
// The specs are what hold the walk to this node. They name every
// claim the kubelet prepared here, so the index holds those claims
// and not the claims of every other node in the cluster.
//
// A UID the listing does not carry is remembered as a claim that is
// gone, because a surface can outlive the claim that opened its
// socket, and a miss that was not remembered would list the claims
// again on every pass for as long as the client stayed connected.
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

// listResourceClaims reads the claims of every namespace. A screen
// serves whichever namespace claims it, so the listing is not scoped
// to one.
func listResourceClaims(c *Client) ([]ResourceClaim, error) {
	list := &ResourceClaimList{}
	if err := c.RequestJSON(http.MethodGet, ResourceClaimsPath, nil, list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// claimHolders is what one claim answers about the surface that
// arrived on its socket: the claim as status names it, the pods that
// hold it, and the labels a region's selector matches.
type claimHolders struct {
	key    string
	pods   []string
	labels map[string]string
}

// holderReader answers that for each claim once per pass, and lists
// this node's pods once. A screen that shows a film over an idle
// client holds several surfaces of the same two claims, so one pass
// asks the API server for each claim once.
type holderReader struct {
	client *Client
	node   string
	claims *claimIndex

	read   map[string]claimHolders
	pods   map[string]Pod
	listed bool
}

func newHolderReader(client *Client, node string, claims *claimIndex) *holderReader {
	return &holderReader{
		client: client,
		node:   node,
		claims: claims,
		read:   map[string]claimHolders{},
	}
}

// holders answers what the claim with this UID is held by.
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

// resolve walks from the UID to the labels: the index names the
// claim, the claim's status.reservedFor names the pods, and the pods
// carry the labels.
//
// A claim that is gone answers no claim and no labels. The client
// that drew on its socket is still connected, and a surface with no
// claim matches no region's selector, so a screen that names a Layout
// stops showing it and the default layout still does.
func (h *holderReader) resolve(uid string) (claimHolders, error) {
	ref, err := h.claims.claim(uid)
	if err != nil {
		return claimHolders{}, err
	}
	if ref.Name == "" {
		return claimHolders{}, nil
	}
	claim, err := GetResourceClaim(h.client, ref.Namespace, ref.Name)
	if errors.Is(err, ErrNotFound) {
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
	// The order the API server lists the holders in is its own, and
	// status reports one order for the same set of pods.
	slices.Sort(held.pods)
	return held, nil
}

// pod reads one holder, and answers nothing for a holder that is
// gone. The node listing answers every holder that runs here, and the
// read by name is what answers for a pod that arrived after the
// listing was taken.
func (h *holderReader) pod(namespace, name string) (*Pod, error) {
	listed, err := h.onThisNode()
	if err != nil {
		return nil, err
	}
	if pod, running := listed[namespace+"/"+name]; running {
		return &pod, nil
	}
	pod, err := getPod(h.client, namespace, name)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return pod, err
}

// The pods on this node, read once per pass and keyed as status names
// them.
func (h *holderReader) onThisNode() (map[string]Pod, error) {
	if h.listed {
		return h.pods, nil
	}
	pods, err := listPods(h.client, h.node)
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

// sharedLabels answers the labels two holders carry with the same
// value.
//
// A region shows one surface, and a claim that several pods hold
// draws one surface, so a label only one holder carries must not
// place it. The rule also makes the answer the same whichever order
// the API server listed the holders in.
func sharedLabels(held, also map[string]string) map[string]string {
	shared := map[string]string{}
	for key, value := range held {
		if carried, ok := also[key]; ok && carried == value {
			shared[key] = value
		}
	}
	return shared
}
