package main

// The workload of a Solver: a one-replica Deployment and a Service in
// the Solver's namespace. The pod runs this binary in its solver mode,
// from the worker image, which builds on the astrometry image.

import (
	"path"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/solver"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

const roleSolver = "solver"

const (
	indexesDir = "/indexes"
	workDir    = "/work"
)

// solverName is the name of a Solver's Deployment and Service. The CRD
// limits a Solver's name to 56 characters so that this name fits in a
// 63-character Service name.
func solverName(solver string) string { return solver + "-solver" }

func solverWorkload(s *astrophotography.Solver, image string) workload {
	name, namespace := solverName(s.Metadata.Name), s.Metadata.Namespace
	labels := workloadLabels(roleSolver, s.Metadata.Name)
	owners := owner(astrophotography.APIVersion, astrophotography.SolverKind.Name, s.Metadata.Name, s.Metadata.UID)
	solve := container{
		Name:  "solver",
		Image: image,
		Args: []string{"solver",
			"--indexes", path.Join(indexesDir, s.Spec.IndexFiles.Path),
			"--work", workDir,
		},
		Ports:           []containerPort{{Name: "http", ContainerPort: solver.Port}},
		SecurityContext: restricted(),
		VolumeMounts: []volumeMount{
			{Name: "indexes", MountPath: indexesDir, ReadOnly: true},
			{Name: "work", MountPath: workDir},
		},
		// The solver reads the header of every index file before it
		// listens, so a ready pod already lists its index files.
		ReadinessProbe: &probe{HTTPGet: &httpGet{Path: solver.PathIndexes, Port: "http"}, PeriodSeconds: 10},
		Resources:      &resources{Requests: map[string]string{"cpu": "100m", "memory": "64Mi"}},
	}
	d := &deployment{
		APIVersion: "apps/v1", Kind: "Deployment",
		Metadata: metaOf(name, namespace, labels, owners),
		Spec: deploymentSpec{
			Replicas: int32Pointer(1),
			Selector: labelSelector{MatchLabels: labels},
			// The index files' claim is often ReadWriteOnce, which only
			// one node can mount at a time. Recreate stops the old pod
			// before the new pod mounts the claim.
			Strategy: strategy{Type: "Recreate"},
			Template: podTemplate{
				Metadata: metaOf("", "", labels, nil),
				Spec: podSpec{
					EnableServiceLinks:           boolPointer(false),
					AutomountServiceAccountToken: boolPointer(false),
					// No fsGroup, so the kubelet does not change the ownership
					// of the read-only claim's files.
					SecurityContext: solverSecurity(),
					Containers:      []container{solve},
					Volumes: []volume{
						{Name: "indexes", PersistentVolumeClaim: &claimName{ClaimName: s.Spec.IndexFiles.ClaimName, ReadOnly: true}},
						{Name: "work", EmptyDir: &emptyDir{}},
					},
				},
			},
		},
	}
	svc := &service{
		APIVersion: "v1", Kind: "Service",
		Metadata: metaOf(name, namespace, labels, owners),
		Spec:     serviceSpec{Selector: labels, Ports: []servicePort{{Name: "http", Port: solver.Port, TargetPort: "http"}}},
	}
	return workload{deployment: d, service: svc}
}

func solverEndpoint(namespace, service string) observatory.Endpoint {
	return observatory.Endpoint{Service: service, Host: service + "." + namespace + ".svc", Port: solver.Port}
}
