package main

// The durable copies of a namespace's two stores. A Catalog asks for a
// number of copies of the catalog and a number of copies of the progress
// store. The copies are peers that Corrosion syncs from one another, and
// every copy of one store runs on a node of its own.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

// The topology the copies spread over. One copy per node is the rule,
// because two copies on one node are lost together.
const hostnameTopologyKey = "kubernetes.io/hostname"

// One of the two durable stores a Catalog stands: the name every copy of
// it is numbered from, the store label value every copy carries, and the
// class the store's claim binds to. The name is the store's one claim as
// well, which every copy mounts. The three travel together, so no caller
// can pair one store's name with the other store's label or class.
type durableStore struct {
	base  string
	label string
	class string
}

func catalogStoreOf(catalog *NamespaceCatalog) durableStore {
	return durableStore{
		base:  catalogStoreName(catalog.Metadata.Name),
		label: catalogStoreLabelValue,
		class: catalog.Spec.Storage.StorageClassName,
	}
}

func progressStoreOf(catalog *NamespaceCatalog) durableStore {
	return durableStore{
		base:  progressStoreName(catalog.Metadata.Name),
		label: progressStoreLabelValue,
		class: progressStorageClass(catalog),
	}
}

// The name one copy's pod takes. Every copy carries its
// number, copy zero included, so one rule names them all and a person
// reads a copy's number off the pod in front of them.
func (s durableStore) replicaName(index int) string {
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

// sweepStoreReplicas takes down every copy of one store at or above the
// count the Catalog asks for. The walk goes upward from that count until
// it finds no pod, because the copies are numbered from zero with no
// gaps. The claim stays, because one claim serves every copy and the
// copies that remain mount it.
func (o *operator) sweepStoreReplicas(ctx context.Context, catalog *NamespaceCatalog, store durableStore, wanted int) error {
	for index := wanted; ; index++ {
		held, err := o.retireStoreReplica(ctx, catalog.Metadata.Namespace, store, index)
		if err != nil || !held {
			return err
		}
	}
}

// retireStoreReplica takes down one copy, which is the pod alone. The
// store label guards the delete, so a pod another writer gave a copy's
// name is left where it is. The answer is whether a pod exists under that
// name, which is what ends the walk.
func (o *operator) retireStoreReplica(ctx context.Context, namespace string, store durableStore, index int) (bool, error) {
	name := store.replicaName(index)

	pod, err := GetPod(ctx, o.client, namespace, name)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if pod.Metadata.Labels[storeLabelKey] != store.label {
		return true, nil
	}
	return true, DeletePod(ctx, o.client, namespace, name)
}

// storeCopies is how many copies of one store the pass stands. A
// per-node class gives every copy a directory of its own on the node it
// runs on, so the Catalog's count stands. On any other class the store's
// one claim binds to one node, so one copy stands and the Catalog reports
// why. One copy needs no class read.
func (o *operator) storeCopies(ctx context.Context, store durableStore, wanted int) (int, error) {
	if wanted <= 1 {
		return wanted, nil
	}
	perNode, err := o.classIsPerNode(ctx, store.class)
	if err != nil {
		return 0, err
	}
	if perNode {
		return wanted, nil
	}
	return 1, nil
}

// blockedStore is the reason and message the Catalog carries while it
// asks for more copies of a store than its class can hold, or an empty
// reason when both stores can stand what the Catalog asks for. The
// catalog store is read first, so a Catalog blocked on both stores
// reports the catalog.
func (o *operator) blockedStore(ctx context.Context, catalog *NamespaceCatalog) (string, string, error) {
	stores := []struct {
		store  durableStore
		wanted int
	}{
		{store: catalogStoreOf(catalog), wanted: catalogReplicaCount(catalog)},
		{store: progressStoreOf(catalog), wanted: progressReplicaCount(catalog)},
	}
	for _, one := range stores {
		copies, err := o.storeCopies(ctx, one.store, one.wanted)
		if err != nil {
			return "", "", err
		}
		if copies < one.wanted {
			return catalogReasonClassNotPerNode, blockedCopiesMessage(one.store, one.wanted), nil
		}
	}
	return "", "", nil
}

// blockedCopiesMessage is the sentence a person acts on: the class that
// cannot hold more than one copy, the store that asked, and the count it
// asked for.
func blockedCopiesMessage(store durableStore, wanted int) string {
	class := fmt.Sprintf("the class %q", store.class)
	if store.class == "" {
		class = "the cluster's default class"
	}
	return fmt.Sprintf("%s is not a per-node class, so %s stands one copy and not %d",
		class, store.base, wanted)
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
