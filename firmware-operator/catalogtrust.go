package main

// A FirmwareCatalog whose jcat is signed by a key other than the LVFS's,
// such as a vendor's own catalog or the lab's test catalog, names the
// signer's certificate with spec.trust. fwupd refuses to refresh from a
// catalog whose jcat it cannot verify against a key in its pki
// directory, and the scan image holds only the LVFS's keys. So each pod
// that loads the catalog, a scan, a rehearsal, or an apply, mounts the
// ConfigMap that holds the certificate, and the scan program copies it
// beside the LVFS's keys before it refreshes (scan/trust.go).
//
// The ConfigMap is in the operator's namespace, where the pods run. The
// operator does not read it: the kubelet mounts it, and a pod whose
// ConfigMap is missing does not start, so the scan's deadline ends it.

const (
	trustVolume = "catalog-trust"
	// trustMountPath is where a pod mounts the ConfigMap. The scan
	// program reads the same path (trustDir in scan/trust.go).
	trustMountPath = "/run/catalog-trust"
)

// catalogTrust is a FirmwareCatalog's spec.trust.
type catalogTrust struct {
	ConfigMapRef configMapRef `json:"configMapRef"`
}

type configMapRef struct {
	Name string `json:"name"`
}

// withTrust adds a catalog's trust to a pod's spec: the ConfigMap as a
// volume, mounted read-only in each container.
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
