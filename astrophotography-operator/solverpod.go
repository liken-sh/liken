package main

// The workload of a Solver: a Deployment of one pod, with the Recreate
// strategy, and a Service, in the Solver's namespace (plan 02). The pod
// runs this binary in its solver mode, in the worker image, which
// builds on the astrometry image. It mounts the claim of the index
// files read-only, and an emptyDir where each job writes its files.

import (
	"path"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/solver"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// roleSolver labels the objects of a Solver, with the Solver's name.
const roleSolver = "solver"

// The directories of the solver's pod.
const (
	indexesDir = "/indexes"
	workDir    = "/work"
)

// solverName names the Deployment and the Service of a Solver. The CRD
// holds a Solver's name to 56 characters, so the name fits a Service.
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
		// The server listens only after it read every index file's
		// header, so a ready pod has its list.
		ReadinessProbe: &probe{HTTPGet: &httpGet{Path: solver.PathIndexes, Port: "http"}, PeriodSeconds: 10},
		// A blind solve of a frame of the CCD simulator peaked at 65 MB
		// of resident memory in plan 02's probe.
		Resources: &resources{Requests: map[string]string{"cpu": "100m", "memory": "64Mi"}},
	}
	d := &deployment{
		APIVersion: "apps/v1", Kind: "Deployment",
		Metadata: metaOf(name, namespace, labels, owners),
		Spec: deploymentSpec{
			Replicas: int32Pointer(1),
			Selector: labelSelector{MatchLabels: labels},
			// A claim of index files is often ReadWriteOnce on one
			// node, so the old pod stops before the new one mounts it.
			Strategy: strategy{Type: "Recreate"},
			Template: podTemplate{
				Metadata: metaOf("", "", labels, nil),
				Spec: podSpec{
					EnableServiceLinks:           boolPointer(false),
					AutomountServiceAccountToken: boolPointer(false),
					// The claim is mounted read-only, so the pod states no
					// fsGroup, and the kubelet changes no file of it.
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

// solverEndpoint answers the address of a Solver's Service.
func solverEndpoint(namespace, solverName string) observatory.Endpoint {
	return observatory.Endpoint{Service: solverName, Host: solverName + "." + namespace + ".svc", Port: solver.Port}
}
