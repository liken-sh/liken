package main

// The seeding loops: making the boot manifest's Machine and the
// image's Cluster real in the API at startup, tolerant of the races
// and not-yet-served CRDs of a fleet booting together. Seeding runs
// once, before the reconcile loop starts; from then on the cluster's
// copies are authoritative.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// ensureMachine makes the manifest's Machine real in the cluster. The
// retry-forever loop covers the operator's first minutes: k3s applies
// the Machine CRD from its manifests directory around the same time
// it starts this pod, and until the API server is serving that CRD,
// our URLs 404. The loop waits instead of crashing because that 404
// is expected during startup, not a sign of anything wrong.
func ensureMachine(c *kubernetes.Client, seed *machine.Machine) (*machine.Machine, error) {
	for {
		current, err := kubernetes.GetMachine(c, seed.Metadata.Name)
		if err == nil {
			return current, nil
		}
		if !errors.Is(err, kubernetes.ErrNotFound) {
			return nil, err
		}

		body, err := json.Marshal(&machine.Machine{
			APIVersion: api.APIVersion,
			Kind:       "Machine",
			Metadata:   api.ObjectMeta{Name: seed.Metadata.Name},
			Spec:       seed.Spec,
		})
		if err != nil {
			return nil, err
		}
		err = c.RequestJSON(http.MethodPost, kubernetes.MachinesPath, body, nil)
		if err == nil {
			fmt.Printf("created machine %s from %s\n", seed.Metadata.Name, machine.BootManifestPath)
			continue // re-GET so we return the server's copy, resourceVersion and all
		}
		if errors.Is(err, kubernetes.ErrNotFound) {
			fmt.Println("machine API not served yet; waiting")
			kubernetes.RetryPause()
			continue
		}
		return nil, err
	}
}

// ensureCluster makes the manifest's Cluster real in the cluster. It
// waits out an unserved CRD the same way ensureMachine does, and it
// tolerates one extra answer: 409 Conflict. Every machine's operator
// races to create the same object at boot, so all but one of those
// POSTs will conflict. That conflict is harmless: the loop's next GET
// confirms the object exists, which is the only outcome that matters.
func ensureCluster(c *kubernetes.Client, seed *cluster.Cluster) error {
	for {
		if _, err := kubernetes.GetCluster(c, seed.Metadata.Name); err == nil {
			return nil
		} else if !errors.Is(err, kubernetes.ErrNotFound) {
			return err
		}

		body, err := json.Marshal(&cluster.Cluster{
			APIVersion: api.APIVersion,
			Kind:       "Cluster",
			Metadata:   api.ObjectMeta{Name: seed.Metadata.Name},
			Spec:       seed.Spec,
		})
		if err != nil {
			return err
		}
		switch err := c.RequestJSON(http.MethodPost, kubernetes.ClustersPath, body, nil); {
		case err == nil:
			fmt.Printf("created cluster %s from %s\n", seed.Metadata.Name, cluster.ClusterManifestPath)
		case errors.Is(err, kubernetes.ErrNotFound):
			fmt.Println("cluster API not served yet; waiting")
			kubernetes.RetryPause()
		case errors.Is(err, kubernetes.ErrConflict):
			// Another machine's operator got there first.
		default:
			return err
		}
	}
}
