package main

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// A capsule's rehearsal records its writes and the ESP's free space,
// and no append, so the apply of a database update never takes it.
func TestAPassedCapsuleRehearsalRecordsItsWrites(t *testing.T) {
	writes := []capsuleWrite{
		{Kind: "file", Name: "EFI/ubuntu/fwupdx64.efi", Bytes: 69176, SHA256: "aa"},
		{Kind: "variable", Name: "BootNext-8be4df61-93ca-11d2-aa0d-00e098032b8c", Bytes: 6, SHA256: "bb"},
	}
	r := rehearsalResult{Result: resultPassed, Message: "the install stages a capsule", Bytes: 69176, SHA256: "cc",
		Free: 4096, ESPFree: 1 << 20, Writes: writes}

	got := recordOfRehearsal(rehearsalAttempt{machine: "node-5"}, r, time.Unix(0, 0))

	if got.Append != nil || got.Capsule == nil || !slices.Equal(got.Capsule.Writes, writes) ||
		got.Capsule.SHA256 != "cc" || got.Capsule.Bytes != 69176 || got.Capsule.ESPFree != 1<<20 {
		t.Errorf("the record is %+v with capsule %+v, want the writes and no append", got, got.Capsule)
	}
}

// The rehearsal pod of a machine whose store names a proven slot mounts
// that slot's ESP, which init links, read-only, for the copy.
func TestARehearsalPodMountsTheNextBootsESP(t *testing.T) {
	c := trustedCatalog()
	c.Spec.Trust = nil
	f := &firmware{Metadata: firmwareMeta{Name: "lab-fmp"}}
	for _, nextESP := range []bool{true, false} {
		p := buildRehearsalPod(f, rehearsalAttempt{firmware: "lab-fmp", machine: "node-5"}, c, "http://cabinet", testScanImage, "liken-system", nextESP)

		v := slices.IndexFunc(p.Spec.Volumes, func(v volume) bool { return v.Name == nextESPVolume })
		mounted := slices.Contains(p.Spec.Containers[0].VolumeMounts, volumeMount{Name: nextESPVolume, MountPath: nextESPPath, ReadOnly: true})
		if (v >= 0) != nextESP || mounted != nextESP {
			t.Errorf("with an ESP %v, the pod has the volume %v and the mount %v", nextESP, v >= 0, mounted)
		}
		if v >= 0 && *p.Spec.Volumes[v].HostPath != (hostPath{Path: provenESPLink, Type: "Directory"}) {
			t.Errorf("the volume is %+v, want the link %s", p.Spec.Volumes[v].HostPath, provenESPLink)
		}
	}
}

// The schema keeps a capsule's record whole.
func TestTheSchemaKeepsACapsulesRehearsal(t *testing.T) {
	f := firmware{APIVersion: firmwareAPIVersion, Kind: firmwareKind, Metadata: firmwareMeta{Name: "lab-fmp"},
		Spec: firmwareSpec{Update: &update{Component: "sh.liken.lab.fmp.firmware", Version: "2",
			Cabinet: &pinnedCabinet{URL: "http://10.0.2.2:8017/firmware/labfmp-v2.cab", SHA256: strings.Repeat("c", 64)}}}}
	r := rehearsalResult{Result: resultPassed, Message: "the install stages a capsule", Bytes: 2959, SHA256: "dd",
		Free: 4096, ESPFree: 1 << 20, Writes: []capsuleWrite{{Kind: "file", Name: "EFI/ubuntu/fwupdx64.efi", Bytes: 69176, SHA256: "aa"}}}
	f.Status.Rehearsals = []rehearsal{recordOfRehearsal(rehearsalAttempt{machine: "node-5", boot: "2026-10-22T20:00:00Z"}, r, time.Unix(0, 0))}

	assertTheSchemaKeeps(t, firmwareCRD, f)
}
