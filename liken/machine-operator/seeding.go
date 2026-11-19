package main

// At startup, before the reconcile loop, the operator creates the
// Machine and the Cluster from the boot manifests when the API has
// none. After that, the objects in the API are the source of truth.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// ensureMachine returns this node's Machine, and creates it from the
// boot manifest when it does not exist. k3s applies the Machine CRD
// from its manifests directory at about the time it starts this pod,
// so a create can get 404 until the CRD is served, and ensureMachine
// retries it.
//
// When it created the Machine, it posts a MachineJoined Event.
func ensureMachine(c *apiclient.Client, manifest *machine.Machine, recorder *events.Recorder) (*machine.Machine, error) {
	created := false
	for {
		current, err := kubernetes.GetMachine(c, manifest.Metadata.Name)
		if err == nil {
			if created {
				recorder.Normal(machineReference(current), reasonMachineJoined,
					"Created the Machine from the boot manifest, because the cluster had none")
			}
			return current, nil
		}
		if !errors.Is(err, apiclient.ErrNotFound) {
			return nil, err
		}

		body, err := json.Marshal(&machine.Machine{
			APIVersion: api.APIVersion,
			Kind:       "Machine",
			Metadata:   api.ObjectMeta{Name: manifest.Metadata.Name},
			Spec:       manifest.Spec,
		})
		if err != nil {
			return nil, err
		}
		err = c.RequestJSON(http.MethodPost, kubernetes.MachinesPath, body, nil)
		if err == nil {
			fmt.Printf("created the Machine %s from %s\n", manifest.Metadata.Name, machine.BootManifestPath)
			created = true
			continue // read it back to return the server's copy, with its resourceVersion
		}
		if errors.Is(err, apiclient.ErrNotFound) {
			fmt.Println("the Machine API is not served yet, waiting")
			kubernetes.RetryPause()
			continue
		}
		return nil, err
	}
}

// ensureCluster creates the Cluster from the boot manifest when it
// does not exist, and waits for the CRD as ensureMachine does. The
// operator on every machine creates the same Cluster, so a 409
// Conflict means another machine created it first, and the next GET
// finds it.
func ensureCluster(c *apiclient.Client, manifest *cluster.Cluster) error {
	for {
		if _, err := kubernetes.GetCluster(c, manifest.Metadata.Name); err == nil {
			return nil
		} else if !errors.Is(err, apiclient.ErrNotFound) {
			return err
		}

		body, err := json.Marshal(&cluster.Cluster{
			APIVersion: api.APIVersion,
			Kind:       "Cluster",
			Metadata:   api.ObjectMeta{Name: manifest.Metadata.Name},
			Spec:       manifest.Spec,
		})
		if err != nil {
			return err
		}
		switch err := c.RequestJSON(http.MethodPost, kubernetes.ClustersPath, body, nil); {
		case err == nil:
			fmt.Printf("created the Cluster %s from %s\n", manifest.Metadata.Name, cluster.ClusterManifestPath)
		case errors.Is(err, apiclient.ErrNotFound):
			fmt.Println("the Cluster API is not served yet, waiting")
			kubernetes.RetryPause()
		case errors.Is(err, apiclient.ErrConflict):
			// Another machine's operator created it first.
		default:
			return err
		}
	}
}
