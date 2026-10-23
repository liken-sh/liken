package main

import (
	"slices"
	"testing"
	"time"
)

// trustedCatalog is a catalog signed by a key other than the LVFS's,
// whose certificate the ConfigMap lab-catalog-signer holds.
func trustedCatalog() *catalog {
	return &catalog{
		Metadata: firmwareMeta{Name: "lab"},
		Spec: catalogSpec{URL: "http://10.0.2.2:8017/firmware/firmware.xml.zst",
			Trust: &catalogTrust{ConfigMapRef: configMapRef{Name: "lab-catalog-signer"}}},
	}
}

// eachCatalogPod builds every pod that loads a catalog: a scan, a
// rehearsal, and an apply.
func eachCatalogPod(c *catalog) map[string]pod {
	f := &firmware{Metadata: firmwareMeta{Name: "lab-fmp"}, Spec: firmwareSpec{Update: &update{
		Component: "sh.liken.lab.fmp.firmware", Version: "2",
		Cabinet: &pinnedCabinet{URL: "http://cabinet", SHA256: "sum"}}}}
	return map[string]pod{
		"scan":      buildScanPod(c, scanAttempt{catalog: c.Metadata.Name, machine: "node-5", image: testScanImage}, "sum", "liken-system"),
		"rehearsal": buildRehearsalPod(f, rehearsalAttempt{firmware: "lab-fmp", machine: "node-5"}, c, "http://cabinet", testScanImage, "liken-system", false),
		"apply":     buildApplyPod(f, applyAttempt{firmware: "lab-fmp", machine: "node-5"}, rehearsal{}, c, testScanImage, "liken-system", time.Time{}),
	}
}

// fwupd refreshes only a catalog whose jcat it verifies against a key
// in its pki directory, so each pod that loads the catalog mounts the
// signer's certificate where the scan program reads it.
func TestEachPodThatLoadsACatalogMountsItsTrust(t *testing.T) {
	for name, p := range eachCatalogPod(trustedCatalog()) {
		v := slices.IndexFunc(p.Spec.Volumes, func(v volume) bool { return v.Name == trustVolume })
		if v < 0 || p.Spec.Volumes[v].ConfigMap == nil || p.Spec.Volumes[v].ConfigMap.Name != "lab-catalog-signer" {
			t.Errorf("the %s pod's volumes are %+v, want the ConfigMap lab-catalog-signer", name, p.Spec.Volumes)
		}
		for _, c := range p.Spec.Containers {
			want := volumeMount{Name: trustVolume, MountPath: trustMountPath, ReadOnly: true}
			if !slices.Contains(c.VolumeMounts, want) {
				t.Errorf("the %s pod's container %s mounts %+v, want %+v", name, c.Name, c.VolumeMounts, want)
			}
		}
	}
}

func TestACatalogSignedByTheLVFSMountsNoTrust(t *testing.T) {
	c := trustedCatalog()
	c.Spec.Trust = nil
	for name, p := range eachCatalogPod(c) {
		if slices.ContainsFunc(p.Spec.Volumes, func(v volume) bool { return v.Name == trustVolume }) {
			t.Errorf("the %s pod mounts a trust volume for a catalog with no spec.trust", name)
		}
	}
}

// fwupd's uefi_capsule plugin sees devices only when the variable store
// it reads is a writable mount point, and it names a capsule device by
// the path of its ESRT entry. Each pod gives fwupd a copy of the
// machine's /sys/firmware at the same path, so a device has the same
// ID in each, and mounts a directory in memory exactly where the
// copy's efivars go.
func TestEachPodMountsItsCopysVariableStore(t *testing.T) {
	for name, p := range eachCatalogPod(trustedCatalog()) {
		for volumeName, path := range map[string]string{firmwareCopyVolume: "/run/fw", copyVariablesVolume: "/run/fw/efi/efivars"} {
			v := slices.IndexFunc(p.Spec.Volumes, func(v volume) bool { return v.Name == volumeName })
			mounted := slices.Contains(p.Spec.Containers[0].VolumeMounts, volumeMount{Name: volumeName, MountPath: path})
			if v < 0 || p.Spec.Volumes[v].EmptyDir == nil || p.Spec.Volumes[v].EmptyDir.Medium != "Memory" || !mounted {
				t.Errorf("the %s pod's volumes are %+v and mounts %+v, want a directory in memory at %s",
					name, p.Spec.Volumes, p.Spec.Containers[0].VolumeMounts, path)
			}
		}
	}
}

// The scan reads the machine's firmware tables through a read-only
// mount, and copies them.
func TestTheScanPodReadsTheFirmwareReadOnly(t *testing.T) {
	p := eachCatalogPod(trustedCatalog())["scan"]
	if !slices.Contains(p.Spec.Containers[0].VolumeMounts, volumeMount{Name: "host-firmware", MountPath: hostFirmwarePath, ReadOnly: true, RecursiveReadOnly: "Enabled"}) {
		t.Errorf("the scan pod mounts %+v, want /sys/firmware read-only at %s", p.Spec.Containers[0].VolumeMounts, hostFirmwarePath)
	}
}
