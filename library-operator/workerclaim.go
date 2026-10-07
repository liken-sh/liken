package main

// workerclaim.go is where the catalog agent in each pod of a worker Job keeps
// its copy. A worker reads its fact's gap from that copy, and the copy is a
// cache: it rebuilds from its peers, so the place is the cluster owner's
// choice of sync time against disk, in spec.workers.storageClassName.
//
// With no class, each pod keeps its copy in an emptyDir. The copy syncs in
// full when the pod starts and goes with the pod. The cluster's default class
// does not apply, because on most clusters that is local-path, which would tie
// each copy to one node, and a cache needs neither the claim nor the node.
//
// With a class, the operator stands one claim for the namespace's workers,
// <catalog>-workers, and each pod mounts the directory of its Library, its
// fact, and its index. One Library runs one worker of a fact at a time, so no
// two agents open one directory. On a per-node class, a pod that lands where
// its index ran before finds its copy there and syncs only what changed.

import (
	"context"
	"errors"
	"slices"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// The claim every worker copy in a namespace is on, named after the Catalog.
func workersClaimName(catalog string) string {
	return catalog + "-workers"
}

// The claim, sized like every other copy of the catalog, because every agent
// holds the whole namespace's catalog. It asks for ReadWriteOnce, and
// standClaim writes ReadWriteMany in its place on a per-node class, where
// every node that mounts it holds a directory of its own. It is owned by the
// Catalog, so the garbage collector takes it with the Catalog.
func buildWorkersClaim(catalog *NamespaceCatalog) *PersistentVolumeClaim {
	return &PersistentVolumeClaim{
		APIVersion: claimAPIVersion,
		Kind:       "PersistentVolumeClaim",
		Metadata: ObjectMeta{
			Name:            workersClaimName(catalog.Metadata.Name),
			Namespace:       catalog.Metadata.Namespace,
			Labels:          map[string]string{scannerLabelKey: workerLabelValue},
			OwnerReferences: []OwnerReference{catalogObjectOwner(catalog)},
		},
		Spec: PersistentVolumeClaimSpec{
			AccessModes: []string{accessModeReadWriteOnce},
			Resources: VolumeResourceRequirements{
				Requests: map[string]string{"storage": catalogStorageSize(catalog)},
			},
			StorageClassName: catalog.Spec.Workers.StorageClassName,
		},
	}
}

// The workers claim follows the field. A class with no claim stands one. No
// class, or a claim on another class, deletes the claim, and the next pass
// stands the claim of the class the field names. A claim that a running pod
// mounts stays under its protection finalizer until the pod ends, so a worker
// finishes on the copy it started on.
func (o *operator) standWorkersClaim(ctx context.Context, catalog *NamespaceCatalog) error {
	class := catalog.Spec.Workers.StorageClassName
	namespace, name := catalog.Metadata.Namespace, workersClaimName(catalog.Metadata.Name)

	live, err := o.readClaim(ctx, namespace, name)
	if errors.Is(err, apiclient.ErrNotFound) {
		if class == "" {
			return nil
		}
		return o.standClaim(ctx, buildWorkersClaim(catalog))
	}
	if err != nil {
		return err
	}
	if live.Spec.StorageClassName == class || live.Metadata.DeletionTimestamp != "" ||
		!slices.Contains(live.Metadata.OwnerReferences, catalogObjectOwner(catalog)) {
		return nil
	}
	o.forgetClaim(namespace, name)
	err = DeletePersistentVolumeClaim(ctx, o.client, namespace, name)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// The directory of one worker pod's copy on the workers claim. An Indexed Job
// gives each pod its index through JOB_COMPLETION_INDEX, which the kubelet
// expands in subPathExpr. A Job of one pod has no index, so its pod takes
// the directory of index 0 by name.
func workerCopyMount(library, fact string, pods int) VolumeMount {
	mount := VolumeMount{Name: catalogVolumeName, MountPath: catalogStatePath}
	prefix := library + "-" + fact + "-"
	if pods > 1 {
		mount.SubPathExpr = prefix + "$(" + completionIndexVariable + ")"
	} else {
		mount.SubPath = prefix + "0"
	}
	return mount
}

// The volume a worker pod's agent keeps its copy on: the workers claim where
// the Catalog names a class, and an emptyDir where it names none.
func workerCopyVolume(catalog *NamespaceCatalog) Volume {
	if catalog.Spec.Workers.StorageClassName == "" {
		return Volume{Name: catalogVolumeName, EmptyDir: &EmptyDirVolumeSource{}}
	}
	return Volume{Name: catalogVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
		ClaimName: workersClaimName(catalog.Metadata.Name),
	}}
}

// The agent of a worker pod: the agent of a library Job, on its own copy. In
// an Indexed Job the agent reads its index from the annotation the Job
// controller writes on each pod, through the downward API, so the subPathExpr
// of its mount expands whether or not the controller also sets
// JOB_COMPLETION_INDEX in init containers, where Kubernetes runs a native
// sidecar.
func workerAgent(catalog *NamespaceCatalog, library, fact, image string, pods int) Container {
	agent := libraryJobAgent(image)
	if catalog.Spec.Workers.StorageClassName != "" {
		agent.VolumeMounts = []VolumeMount{workerCopyMount(library, fact, pods)}
	}
	if pods > 1 {
		agent.Env = append(agent.Env, EnvVar{Name: completionIndexVariable, ValueFrom: &EnvVarSource{
			FieldRef: &ObjectFieldSelector{FieldPath: completionIndexFieldPath},
		}})
	}
	return agent
}

// The annotation the Job controller writes on each pod of an Indexed Job.
const completionIndexFieldPath = "metadata.annotations['batch.kubernetes.io/job-completion-index']"
