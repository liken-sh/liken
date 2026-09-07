package main

// The durable copies of a namespace's two stores. A Catalog asks for a
// number of copies of the catalog and a number of copies of the progress
// store. The copies are peers that Corrosion syncs from one another, and
// every copy of one store runs on a node of its own.

import (
	"context"
	"errors"
	"strconv"
)

// The topology the copies spread over. One copy per node is the rule,
// because two copies on one node are lost together.
const hostnameTopologyKey = "kubernetes.io/hostname"

// One of the two durable stores a Catalog stands: the name its first copy
// takes, and the store label value every copy carries. The two travel
// together, so no caller can pair one store's name with the other store's
// label.
type durableStore struct {
	base  string
	label string
}

func catalogStoreOf(catalog *NamespaceCatalog) durableStore {
	return durableStore{base: catalogPodName(catalog.Metadata.Name), label: catalogStoreLabelValue}
}

func progressStoreOf(catalog *NamespaceCatalog) durableStore {
	return durableStore{base: progressPodName(catalog.Metadata.Name), label: progressStoreLabelValue}
}

// The name one copy's pod and its claim take. The first copy keeps the
// store's own name, so a namespace that already holds a catalog and a
// progress store migrates nothing. Every copy after it carries its number.
func (s durableStore) replicaName(index int) string {
	if index == 0 {
		return s.base
	}
	return s.base + "-" + strconv.Itoa(index)
}

// The rule that keeps two copies of one store off one node. It is
// required, not preferred: a copy the scheduler cannot place stays Pending
// and the Catalog's status says so. A second copy on the same node would
// look healthy and protect nothing.
func (s durableStore) antiAffinity() *Affinity {
	return &Affinity{
		PodAntiAffinity: &PodAntiAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []PodAffinityTerm{{
				LabelSelector: &LabelSelector{MatchLabels: map[string]string{storeLabelKey: s.label}},
				TopologyKey:   hostnameTopologyKey,
			}},
		},
	}
}

// The agent as a copy past the first runs it: as the pod's one container.
// A pod needs at least one container that is not an initContainer, and
// the restartPolicy that makes an initContainer a native sidecar is not a
// field a container takes, so the copy clears it. Nothing needs the
// sidecar order here: the role that reads the agent runs on the first
// copy alone.
func replicaAgent(agent Container) Container {
	agent.RestartPolicy = ""
	return agent
}

// Take down every copy of one store at or above the count the Catalog
// asks for. The walk goes upward from that count until it finds neither a
// pod nor a claim, because the copies are numbered from zero with no gaps.
// A copy that goes takes its claim with it: a backup is a deliberate act
// elsewhere, and the peers hold the rows.
func (o *operator) sweepStoreReplicas(ctx context.Context, catalog *NamespaceCatalog, store durableStore, wanted int) error {
	for index := wanted; ; index++ {
		held, err := o.retireStoreReplica(ctx, catalog.Metadata.Namespace, store, index)
		if err != nil || !held {
			return err
		}
	}
}

// Take down one copy: the pod first, then the claim under it, so the
// volume is released before it is deleted. The store label guards both
// deletes, so a pod or a claim that another writer gave a copy's name is
// left where it is. The answer is whether anything stood under that name,
// which is what ends the walk.
func (o *operator) retireStoreReplica(ctx context.Context, namespace string, store durableStore, index int) (bool, error) {
	name := store.replicaName(index)

	pod, err := GetPod(ctx, o.client, namespace, name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return false, err
	}
	claim, err := GetPersistentVolumeClaim(ctx, o.client, namespace, name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return false, err
	}
	if pod != nil && pod.Metadata.Labels[storeLabelKey] == store.label {
		if err := DeletePod(ctx, o.client, namespace, name); err != nil {
			return true, err
		}
	}
	if claim != nil && claim.Metadata.Labels[storeLabelKey] == store.label {
		if err := DeletePersistentVolumeClaim(ctx, o.client, namespace, name); err != nil {
			return true, err
		}
	}
	return pod != nil || claim != nil, nil
}

// How many copies of one store are up, out of how many the Catalog asks
// for. A copy counts when it runs and the kubelet marks every container
// of it ready, the agent included.
func storeReplicaCount(pods []*Pod, agent string, wanted int) StoreReplicas {
	ready := 0
	for _, pod := range pods {
		if pod != nil && pod.Status.Phase == podRunning && everyContainerReadyBeside(pod, agent) {
			ready++
		}
	}
	return StoreReplicas{Ready: ready, Wanted: wanted}
}
