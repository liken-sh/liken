package main

// These tests read the pod and the claim that the operator creates for
// a device that a rule matches.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
)

// startedUpload runs the operator with the QHY5L-II rule and the bare
// chip on liken-1, and answers the world once the upload has started.
func startedUpload(t *testing.T) *world {
	w := startWorld(t)
	w.api.putFirmware(qhy5lii())
	w.api.publish("liken-1", bareChip("1-4.1.2", 12))
	w.settle()
	return w
}

// A matching device gets one pod on its node, which runs the rule's
// command against the device's usbfs node, unprivileged.
func TestAMatchingDeviceGetsOneUploadPod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startedUpload(t)
		upload := w.onlyUpload(t)
		rule := w.api.firmware(t, "qhy5l-ii")
		spec, loader := upload.Spec, upload.Spec.Containers[0]

		checks := []struct {
			name string
			ok   bool
		}{
			{"the pod's name", upload.Metadata.Name == uploadPodName("qhy5l-ii", 1, appearance{"liken-1", "1-4.1.2", 12})},
			{"the node selector", spec.NodeSelector["kubernetes.io/hostname"] == "liken-1" && len(spec.NodeSelector) == 1},
			{"the restart policy", spec.RestartPolicy == "Never"},
			{"the deadline", spec.ActiveDeadlineSeconds != nil && *spec.ActiveDeadlineSeconds == 300},
			{"the owner", len(upload.Metadata.OwnerReferences) == 1 &&
				upload.Metadata.OwnerReferences[0] == ownerReference{firmwareAPIVersion, firmwareKind, "qhy5l-ii", rule.Metadata.UID}},
			{"the label", upload.Metadata.Labels[uploadLabel] == "true" && upload.Metadata.Labels["app.kubernetes.io/managed-by"] == component},
			{"the record", upload.Metadata.Annotations[firmwareKey] == "qhy5l-ii" && upload.Metadata.Annotations[generationKey] == "1" &&
				upload.Metadata.Annotations[machineKey] == "liken-1" && upload.Metadata.Annotations[addressKey] == "1-4.1.2" &&
				upload.Metadata.Annotations[deviceNumberKey] == "12" && upload.Metadata.Annotations[deviceKey] == "usb-1-4-1-2"},
			{"the image", loader.Image == "ghcr.io/liken-sh/indi-qhy:test"},
			{"the command", slices.Equal(loader.Command, qhy5lii().Spec.Upload.Command)},
			{"LIKEN_DEVICE", slices.Equal(loader.Env, []envVar{{deviceVariable, "/dev/bus/usb/001/012"}})},
			{"the claim", slices.Equal(spec.ResourceClaims, []podResourceClaim{{uploadClaim, upload.Metadata.Name}}) &&
				slices.Equal(loader.Resources.Claims, []containerClaim{{uploadClaim}})},
			{"no token", spec.AutomountServiceAccountToken != nil && !*spec.AutomountServiceAccountToken},
			{"no service links", spec.EnableServiceLinks != nil && !*spec.EnableServiceLinks},
			{"a user that is not root", *spec.SecurityContext.RunAsNonRoot && *spec.SecurityContext.RunAsUser == 1000 && *spec.SecurityContext.RunAsGroup == 1000},
			{"seccomp", spec.SecurityContext.SeccompProfile.Type == "RuntimeDefault"},
			{"no escalation", !*loader.SecurityContext.AllowPrivilegeEscalation},
			{"a read-only root", *loader.SecurityContext.ReadOnlyRootFilesystem},
			{"no capabilities", slices.Equal(loader.SecurityContext.Capabilities.Drop, []string{"ALL"})},
			{"the log in the termination message", loader.TerminationMessagePolicy == "FallbackToLogsOnError"},
		}
		// t.Run is not allowed inside a bubble, so each check names
		// itself in its error.
		for _, c := range checks {
			if !c.ok {
				t.Errorf("%s is wrong in the upload pod %+v", c.name, upload)
			}
		}
	})
}

// The pod's claim has the pod's name, belongs to the pod, and selects
// this one appearance of the device.
func TestTheUploadPodsClaimSelectsTheAppearance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startedUpload(t)
		upload := w.onlyUpload(t)
		claims := w.api.claims(t)
		if len(claims) != 1 {
			t.Fatalf("%d claims, want 1", len(claims))
		}
		claim := claims[0]

		if claim.Metadata.Name != upload.Metadata.Name {
			t.Errorf("the claim's name is %s, want the pod's, %s", claim.Metadata.Name, upload.Metadata.Name)
		}
		want := []ownerReference{{"v1", "Pod", upload.Metadata.Name, upload.Metadata.UID}}
		if !slices.Equal(claim.Metadata.OwnerReferences, want) {
			t.Errorf("the claim's owners are %+v, want %+v", claim.Metadata.OwnerReferences, want)
		}
		requests := claim.Spec.Devices.Requests
		if len(requests) != 1 || requests[0].Name != uploadClaim || requests[0].Exactly.DeviceClassName != uploadClass {
			t.Fatalf("the claim requests %+v, want one device of %s", requests, uploadClass)
		}
		selector := requests[0].Exactly.Selectors[0].CEL.Expression
		wantSelector := `device.attributes["liken.sh"].address == "1-4.1.2" && device.attributes["liken.sh"].deviceNumber == 12`
		if selector != wantSelector {
			t.Errorf("the claim selects %s, want %s", selector, wantSelector)
		}
	})
}

// The claim's selector compiles in the CEL environment of a
// DeviceClass, matches the appearance it names, and matches no other.
func TestTheClaimSelectorMatchesOnlyItsAppearance(t *testing.T) {
	selector, err := newSelectors().compile(claimSelector(appearance{"liken-1", "1-4.1.2", 12}))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		device usb
		want   bool
	}{
		{"the appearance", bareChip("1-4.1.2", 12), true},
		{"the next appearance in the port", bareChip("1-4.1.2", 13), false},
		{"another port", bareChip("1-4.1.3", 12), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := devicesOf(sliceOf("liken-1", c.device))[0]
			if got := matches(t.Context(), "claim", selector, d); got != c.want {
				t.Errorf("matches = %v, want %v", got, c.want)
			}
		})
	}
}

// sliceOf builds the slice machine-operator would publish for a
// machine's devices.
func sliceOf(machine string, devices ...usb) resourceSlice {
	var slice resourceSlice
	slice.Spec.Driver, slice.Spec.NodeName = driverName, machine
	for _, d := range devices {
		slice.Spec.Devices = append(slice.Spec.Devices, d.sliceDevice())
	}
	return slice
}

// The status records the upload as Running, and the operator posts
// UploadStarted.
func TestAStartedUploadIsRunningInTheStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startedUpload(t)
		e := w.entry(t, "qhy5l-ii")
		want := deviceEntry{Machine: "liken-1", Device: "usb-1-4-1-2", Address: "1-4.1.2", DeviceNumber: 12, Generation: 1, Result: resultRunning}
		e.Message, e.Time = "", ""
		if e != want {
			t.Errorf("the entry is %+v, want %+v", e, want)
		}
		if got := w.api.firmware(t, "qhy5l-ii").Status.Counts; got != (counts{Running: 1}) {
			t.Errorf("status.counts = %+v, want one running", got)
		}
		want2 := []posted{{"Normal", reasonCompiled}, {"Normal", reasonUploadStarted}}
		if got := w.postedAbout("qhy5l-ii"); !slices.Equal(got, want2) {
			t.Errorf("the Events are %v, want %v", got, want2)
		}
	})
}

// A device that the rule does not match gets no pod.
func TestADeviceTheRuleDoesNotMatchGetsNoPod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.putFirmware(qhy5lii())
		w.api.publish("liken-1", camera("1-4.1.2", 13))
		w.settle()

		if got := len(w.api.uploads(t)); got != 0 {
			t.Errorf("%d upload pods, want none", got)
		}
		if got := w.api.firmware(t, "qhy5l-ii").Status.Devices; len(got) != 0 {
			t.Errorf("status.devices = %+v, want none", got)
		}
	})
}

// A pod name holds the rule's name for a person to read, and stays a
// DNS label however long the rule's name is.
func TestTheUploadPodsNameIsADNSLabel(t *testing.T) {
	a := appearance{"liken-1", "1-4.1.2", 12}
	cases := []struct {
		name, rule, wantPrefix string
	}{
		{"a short rule", "qhy5l-ii", "qhy5l-ii-"},
		{"a long rule", strings.Repeat("a", 49) + "-b" + strings.Repeat("c", 20), strings.Repeat("a", 49) + "-"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := uploadPodName(c.rule, 1, a)
			if len(got) > 63 || !strings.HasPrefix(got, c.wantPrefix) || strings.Contains(got, "--") {
				t.Errorf("uploadPodName = %q, want at most 63 characters that start with %q", got, c.wantPrefix)
			}
		})
	}
}

// A create that answers 409, for a pod the watch does not hold yet,
// reads the pod that exists, and its claim names that pod as its
// owner.
func TestACreateThatMeetsAnExistingPodClaimsForIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		api.putFirmware(qhy5lii())
		chip := bareChip("1-4.1.2", 12)
		rule := api.firmware(t, "qhy5l-ii")
		hidden := buildUploadPod(&rule, devicesOf(sliceOf("liken-1", chip))[0], operatorNS)
		// With no upload label, the watch never holds the pod.
		hidden.Metadata.Labels = nil
		api.putUpload(hidden)
		w := &world{api: api}
		w.run(t)
		w.api.publish("liken-1", chip)
		w.settle()

		claims := w.api.claims(t)
		if len(claims) != 1 || claims[0].Metadata.OwnerReferences[0].UID != "uid-earlier" {
			t.Errorf("the claims are %+v, want one owned by the pod that exists", claims)
		}
		if e := w.entry(t, "qhy5l-ii"); e.Result != resultRunning {
			t.Errorf("the entry is %+v, want Running", e)
		}
	})
}
