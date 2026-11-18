package main

// A FirmwareCatalog whose jcat is signed by a key other than the LVFS's
// key, such as a vendor's own catalog or the lab's test catalog, points
// to the signer's certificate with spec.trust. fwupd refuses to refresh
// from a catalog whose jcat it cannot verify against a key in its pki
// directory, and the scan image has only the LVFS's keys. So each pod
// that loads the catalog (a scan, a rehearsal, or an apply) mounts the
// ConfigMap that holds the certificate. The scan program copies the
// certificate next to the LVFS's keys before it refreshes
// (scan/trust.go).
//
// The ConfigMap is in the operator's namespace, where the pods run. The
// operator does not read it. The kubelet mounts it, and a pod whose
// ConfigMap is missing does not start, so the pod's deadline ends it.

const (
	trustVolume = "catalog-trust"
	// trustMountPath is where a pod mounts the ConfigMap. The scan
	// program reads the certificate from the same path (trustDir in
	// scan/trust.go).
	trustMountPath = "/run/catalog-trust"
)

// catalogTrust is a FirmwareCatalog's spec.trust: the ConfigMap that
// holds the signer's certificate.
type catalogTrust struct {
	ConfigMapRef configMapRef `json:"configMapRef"`
}

type configMapRef struct {
	Name string `json:"name"`
}

// withTrust adds the catalog's ConfigMap to a pod's spec as a volume,
// mounted read-only in each container. A catalog with no spec.trust
// leaves the spec unchanged.
func withTrust(spec podSpec, c *catalog) podSpec {
	if c.Spec.Trust == nil {
		return spec
	}
	spec.Volumes = append(spec.Volumes, volume{Name: trustVolume, ConfigMap: &configMapVolume{Name: c.Spec.Trust.ConfigMapRef.Name}})
	for i := range spec.Containers {
		spec.Containers[i].VolumeMounts = append(spec.Containers[i].VolumeMounts,
			volumeMount{Name: trustVolume, MountPath: trustMountPath, ReadOnly: true})
	}
	return spec
}
