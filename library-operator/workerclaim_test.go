package main

import (
	"net/http"
	"testing"
)

// The claim of the worker copies follows spec.workers.storageClassName: it
// stands while the field names a class, on that class, and goes when the
// field names none or another class.

// A workers claim on the class given, owned by the house Catalog.
func standingWorkersClaim(class string) *PersistentVolumeClaim {
	claim := buildWorkersClaim(workersOnClass(class))
	claim.Status.Phase = claimBound
	return claim
}

// A class stands the claim on that class, at the Catalog's size, owned by
// the Catalog. On a per-node class every node that mounts it holds a copy of
// its own, so it is ReadWriteMany, and on any other class ReadWriteOnce.
func TestAClassStandsTheWorkersClaim(t *testing.T) {
	cases := []struct {
		name  string
		class string
		mode  string
	}{
		{name: "a per-node class", class: "per-node", mode: accessModeReadWriteMany},
		{name: "another class", class: "local-path", mode: accessModeReadWriteOnce},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			seedStorageClass(cluster, "per-node", perNodeProvisioner)
			catalog := workersOnClass(one.class)
			catalog.Spec.Storage.Size = "3Gi"

			if err := testOperator(t, cluster).standWorkersClaim(t.Context(), catalog); err != nil {
				t.Fatal(err)
			}

			claim := cluster.heldClaim("house-workers")
			if claim == nil {
				t.Fatal("the operator stood no workers claim")
			}
			if claim.Spec.StorageClassName != one.class || claim.Spec.Resources.Requests["storage"] != "3Gi" ||
				len(claim.Spec.AccessModes) != 1 || claim.Spec.AccessModes[0] != one.mode {
				t.Errorf("claim = %+v, want %s at 3Gi, %s", claim.Spec, one.class, one.mode)
			}
			if owners := claim.Metadata.OwnerReferences; len(owners) != 1 || owners[0] != catalogObjectOwner(catalog) {
				t.Errorf("owners = %+v, want the Catalog", owners)
			}
		})
	}
}

// What the pass does with a claim that stands: keeps it while the field
// names its class, and deletes it when the field names none or another
// class. A claim the Catalog does not own is a person's, and stays.
func TestTheWorkersClaimFollowsTheField(t *testing.T) {
	cases := []struct {
		name    string
		claim   *PersistentVolumeClaim
		class   string
		deletes int
	}{
		{name: "the same class", claim: standingWorkersClaim("per-node"), class: "per-node"},
		{name: "no class", claim: standingWorkersClaim("per-node"), deletes: 1},
		{name: "another class", claim: standingWorkersClaim("per-node"), class: "local-path", deletes: 1},
		{name: "a claim of another owner", claim: &PersistentVolumeClaim{
			Metadata: ObjectMeta{Name: "house-workers", Namespace: "house"},
			Spec:     PersistentVolumeClaimSpec{StorageClassName: "per-node"},
		}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			cluster.claims["house-workers"] = one.claim

			if err := testOperator(t, cluster).standWorkersClaim(t.Context(), workersOnClass(one.class)); err != nil {
				t.Fatal(err)
			}

			if got := cluster.countRequests(http.MethodDelete, "persistentvolumeclaims"); got != one.deletes {
				t.Errorf("deletes = %d, want %d", got, one.deletes)
			}
			if got := cluster.countRequests(http.MethodPost, "persistentvolumeclaims"); got != 0 {
				t.Errorf("creates = %d, want none on the pass that finds a claim", got)
			}
		})
	}
}

// No class and no claim is the default, and the pass writes nothing.
func TestNoClassAndNoClaimWritesNothing(t *testing.T) {
	cluster := newFakeCluster()

	if err := testOperator(t, cluster).standWorkersClaim(t.Context(), testNamespaceCatalog()); err != nil {
		t.Fatal(err)
	}

	if cluster.heldClaim("house-workers") != nil {
		t.Error("the operator stood a workers claim with no class")
	}
}
