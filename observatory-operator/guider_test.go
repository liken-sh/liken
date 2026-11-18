package main

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/client-go/util/jsonpath"
	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/observatory-operator/observatory"
	"github.com/liken-sh/liken/observatory-operator/phd2/phd2test"
)

func TestTheProfileNamesTheEquipmentPHD2Reads(t *testing.T) {
	t.Parallel()
	head := "ConfigVersion=2001\ncurrentProfile=1\n[profile]\n[profile/1]\nname=east\n" +
		"[profile/1/camera]\nLastMenuChoice=INDI Camera [Guide Simulator]\nAutoLoadDarks=0\nAutoLoadDefectMap=0\n"
	tail := "[profile/1/frame]\nfocalLength=200\n" +
		"[profile/1/indi]\nINDIhost=east-telescope.observatory.svc\nINDIport=7624\nINDIcam=Guide Simulator\nINDIcam_ccd=0\n"
	others := "[profile/1/stepguider]\nLastMenuChoice=None\n[profile/1/rotator]\nLastMenuChoice=None\n"
	cases := []struct {
		what   string
		pulses observatory.PulseTarget
		mount  string
		want   string
	}{
		{"pulses to the mount", observatory.PulsesMount, "Telescope Simulator",
			head + "[profile/1/scope]\nLastMenuChoice=INDI Mount [Telescope Simulator]\n" + tail + "INDImount=Telescope Simulator\n" + others},
		{"pulses through the camera", observatory.PulsesCamera, "Telescope Simulator",
			head + "[profile/1/scope]\nLastMenuChoice=On-camera\n" + tail + "INDImount=Telescope Simulator\n" + others},
		{"pulses through the camera of a telescope with no mount", observatory.PulsesCamera, "",
			head + "[profile/1/scope]\nLastMenuChoice=On-camera\n" + tail + others},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			got := guiderProfile("east", c.pulses, "east-telescope.observatory.svc", guiderEquipment{camera: "Guide Simulator", mount: c.mount, focalLength: 199.6})
			if got != c.want {
				t.Errorf("the profile is:\n%s\nwant:\n%s", got, c.want)
			}
		})
	}
}

// The guider's pod runs PHD2 with the pinned weston as a native
// sidecar. It has no device claim, and it is scheduled on the node of
// the INDI server.
func TestTheGuiderPodRunsPHD2BesideItsCompositor(t *testing.T) {
	t.Parallel()
	guider := &observatory.Guider{Metadata: observatory.ObjectMeta{Name: "east", UID: "u1"}, Spec: observatory.GuiderSpec{Telescope: "east", OpticalTrain: "east-guiding", Pulses: observatory.PulsesMount}}
	files, p, svc, err := guiderPod(testNamespace, serverRef{observatory.TelescopeKind, "east"}, guider, guiderEquipment{camera: "Guide Simulator", mount: "Telescope Simulator", focalLength: 200})
	if err != nil {
		t.Fatalf("building the guider pod: %v", err)
	}
	if p.Metadata.Name != "east-guider" || files.Metadata.Name != "east-guider" || svc.Metadata.Name != "east-guider" {
		t.Errorf("the pod, ConfigMap, and Service are named %s, %s, %s, want east-guider for each", p.Metadata.Name, files.Metadata.Name, svc.Metadata.Name)
	}
	sidecar, phd2 := p.Spec.InitContainers[0], p.Spec.Containers[0]
	if sidecar.RestartPolicy != "Always" || sidecar.StartupProbe == nil || !strings.HasPrefix(sidecar.Image, "ghcr.io/liken-sh/weston:") {
		t.Errorf("the compositor sidecar is %s, want an always-restarting init container with a startup probe and a weston image", mustJSON(sidecar))
	}
	if !strings.HasPrefix(phd2.Image, "ghcr.io/liken-sh/indi-phd2:") || phd2.ReadinessProbe == nil || phd2.Ports[0].ContainerPort != 4400 {
		t.Errorf("the PHD2 container is %s, want an indi-phd2 image, a readiness probe, and port 4400", mustJSON(phd2))
	}
	for _, c := range []container{sidecar, phd2} {
		if !c.SecurityContext.ReadOnlyRootFilesystem || c.SecurityContext.RunAsUser != 1000 || c.Resources != nil {
			t.Errorf("container %s has security context %+v and resources %+v, want a read-only root filesystem, user 1000, and no resources", c.Name, c.SecurityContext, c.Resources)
		}
		if !slices.ContainsFunc(c.VolumeMounts, func(m volumeMount) bool { return m.MountPath == waylandDir }) {
			t.Errorf("container %s does not mount the Wayland socket directory", c.Name)
		}
	}
	if len(p.Spec.ResourceClaims) != 0 || *p.Spec.TerminationGracePeriodSeconds != 1 || mustJSON(p.Spec.Affinity) != mustJSON(besideEast) {
		t.Errorf("the pod spec is %s, want no resource claims, a termination grace period of 1 second, and affinity to the INDI server pod", mustJSON(p.Spec))
	}
	if svc.Spec.Ports[0].Port != 4400 || p.Metadata.OwnerReferences[0].Kind != "Guider" {
		t.Errorf("the Service is %s and the pod owners are %+v, want port 4400 and a Guider owner", mustJSON(svc.Spec), p.Metadata.OwnerReferences)
	}
	if !strings.Contains(files.Data[westonKey], "require-input=false") || !strings.Contains(files.Data[profileKey], "ConfigVersion=2001") {
		t.Errorf("the ConfigMap data is %v, want the weston setting require-input=false and the PHD2 profile ConfigVersion=2001", files.Data)
	}
}

// A changed profile replaces the pod, because PHD2 reads its profile
// only when it starts.
func TestAChangedProfileReplacesThePod(t *testing.T) {
	t.Parallel()
	guider := &observatory.Guider{Metadata: observatory.ObjectMeta{Name: "east"}, Spec: observatory.GuiderSpec{Pulses: observatory.PulsesMount}}
	ref := serverRef{observatory.TelescopeKind, "east"}
	_, a, _, _ := guiderPod(testNamespace, ref, guider, guiderEquipment{camera: "Guide Simulator", mount: "Telescope Simulator", focalLength: 200})
	_, b, _, _ := guiderPod(testNamespace, ref, guider, guiderEquipment{camera: "Guide Simulator", mount: "Telescope Simulator", focalLength: 400})
	if current(a, b) {
		t.Error("two different profiles have the same digest, want different digests")
	}
}

func readyGuider(t *testing.T) *world {
	w := startWorld(t)
	w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
	w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
	return w
}

func guider(w *world) observatory.Guider {
	g, _ := decode[observatory.Guider](w.t, w.api, kindCollection(observatory.GuiderKind), "east")
	return g
}

func TestStartGuiderConnectsPHD2AndLeavesItIdle(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyGuider(t)
		r, _ := w.reservation("east-tonight")
		if s := stepOf(r, observatory.StepStartGuider); s.Summary != "Connected PHD2 to camera Guide Simulator and mount Telescope Simulator" {
			t.Errorf("the StartGuider step is %+v, want the summary of connecting PHD2 to its camera and mount", s)
		}
		phd2 := w.guiders.phd2("east-guider")
		if got := phd2.Received(); !strings.Contains(got, "set_connected") || strings.Contains(got, "loop") || strings.Contains(got, "guide") {
			t.Errorf("PHD2 received %q, want set_connected and no loop or guide", got)
		}
		files, _ := w.api.object(configMapsCollection, "east-guider")
		profile := files["data"].(map[string]any)[profileKey].(string)
		for _, line := range []string{"LastMenuChoice=INDI Camera [Guide Simulator]", "LastMenuChoice=INDI Mount [Telescope Simulator]", "focalLength=200", "INDIhost=east-telescope.observatory.svc"} {
			if !strings.Contains(profile, line+"\n") {
				t.Errorf("the profile has no %q:\n%s", line, profile)
			}
		}
		w.until(time.Minute, "the Guider is not Ready", func() bool { return guider(w).Status.Phase == observatory.PhaseReady })
		s := guider(w).Status
		if s.State != observatory.GuiderStopped || s.Endpoint == nil || s.Endpoint.Host != "east-guider.observatory.svc" || s.Endpoint.Port != 4400 || s.Pod != "east-guider" {
			t.Errorf("the Guider status is %s, want state Stopped, endpoint east-guider.observatory.svc:4400, and pod east-guider", mustJSON(s))
		}
		if c := conditionOf(s.Conditions, observatory.ConditionReady); c.Status != observatory.ConditionTrue || c.Message != "PHD2 is connected to its camera and mount" {
			t.Errorf("the Ready condition is %+v, want True with the message PHD2 is connected to its camera and mount", c)
		}
	})
}

func TestARefusedConnectFailsStartGuider(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.guiders.prepare = func(s *phd2test.Server) { s.Refuse = "equipment failed to connect: camera" }
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		r := w.phase("east-tonight", observatory.ReservationFailed, 15*time.Minute)
		s := stepOf(r, observatory.StepStartGuider)
		want := "Failed: PHD2 did not connect camera Guide Simulator and mount Telescope Simulator: phd2: set_connected: equipment failed to connect: camera"
		if s.State != observatory.StepFailed || s.Summary != want {
			t.Errorf("the StartGuider step is %+v, want Failed with %q", s, want)
		}
	})
}

func TestStartGuiderWaitsForTheGuiderPod(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.holdPending("east-guider")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		r := w.phase("east-tonight", observatory.ReservationFailed, 15*time.Minute)
		s := stepOf(r, observatory.StepStartGuider)
		if !strings.Contains(s.Summary, "Timed out after 10 min: waiting for pod east-guider (Pending)") {
			t.Errorf("the StartGuider summary is %q, want it to contain the timeout and the pending pod", s.Summary)
		}
		w.until(time.Minute, "the Guider is not in Error", func() bool {
			c := conditionOf(guider(w).Status.Conditions, observatory.ConditionReady)
			return c.Reason == string(observatory.PhaseError) && c.Message == "Reservation east-tonight failed"
		})
	})
}

// StopWork stops PHD2 first, and StopGuider deletes the guider's pod
// while the camera and the mount are still connected, so PHD2 never
// reports a lost device.
func TestDeactivationStopsPHD2BeforeItsDevicesDisconnect(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyGuider(t)
		var atStop []string
		w.guiders.stopped = func(string) { atStop = w.indi.connected("east-telescope") }
		phd2 := w.guiders.phd2("east-guider")
		phd2.Set(func(s *phd2test.Server) { s.AppState = "Guiding" })
		phd2.Broadcast(phd2test.Event("StartGuiding", nil))
		w.until(time.Minute, "the Guider is not Guiding", func() bool { return guider(w).Status.State == observatory.GuiderGuiding })
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(10*time.Minute, "the Reservation is still there", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		if !strings.Contains(phd2.Received(), "stop_capture") {
			t.Errorf("PHD2 received %q, want stop_capture", phd2.Received())
		}
		if !slices.Contains(atStop, "Guide Simulator") || !slices.Contains(atStop, "Telescope Simulator") {
			t.Errorf("when PHD2 stopped, %v were connected, want both Guide Simulator and Telescope Simulator", atStop)
		}
		if names := w.api.names(configMapsCollection); len(names) != 0 {
			t.Errorf("after deactivation, the ConfigMaps %v remain, want none", names)
		}
	})
}

// StopWork also runs when a reservation ends normally, so its summary
// and its Event describe stopping PHD2 and do not suggest a failure.
func TestStopWorkDescribesThePHD2ItStopped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		end := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": end})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.guiders.phd2("east-guider").Set(func(s *phd2test.Server) { s.AppState = "Looping" })
		w.guiders.phd2("east-guider").Broadcast(phd2test.Event("LoopingExposures", map[string]any{"Frame": 1}))
		w.until(time.Minute, "the Guider is not Looping", func() bool { return guider(w).Status.State == observatory.GuiderLooping })
		time.Sleep(time.Hour)
		r := w.phase("east-tonight", observatory.ReservationReleased, 10*time.Minute)
		if s := stepOf(r, observatory.StepStopWork); s.State != observatory.StepDone || s.Summary != "Stopped PHD2, which was Looping" {
			t.Errorf("the StopWork step is %+v, want Done with the summary %q", s, "Stopped PHD2, which was Looping")
		}
		if events, want := eventMessages(w.api), "StopWork: Done in 0 s: stopped PHD2, which was Looping"; !slices.Contains(events, want) {
			t.Errorf("the Events %q do not include %q", events, want)
		}
		if s := stepOf(r, observatory.StepStopGuider); s.State != observatory.StepDone || s.Summary != "Stopped east-guider" {
			t.Errorf("the StopGuider step is %+v, want Done with the summary %q", s, "Stopped east-guider")
		}
	})
}

// printerRow returns what kubectl get prints for an object: each
// printer column of the kind's CRD with priority 0, evaluated from its
// JSONPath.
func printerRow(t *testing.T, file string, object any) map[string]string {
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("reading the CRD file %s: %v", file, err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatalf("parsing the CRD file %s: %v", file, err)
	}
	row := map[string]string{}
	for _, column := range crd.Spec.Versions[0].AdditionalPrinterColumns {
		if column.Priority != 0 || column.Type == "date" {
			continue
		}
		path := jsonpath.New(column.Name).AllowMissingKeys(true)
		if err := path.Parse("{" + column.JSONPath + "}"); err != nil {
			t.Fatalf("parsing the JSONPath of the %s column: %v", column.Name, err)
		}
		var out bytes.Buffer
		if err := path.Execute(&out, asJSON(object)); err != nil {
			t.Fatalf("evaluating the JSONPath of the %s column: %v", column.Name, err)
		}
		row[column.Name] = out.String()
	}
	return row
}

func TestTheGuiderStatusFollowsPHD2(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.guiders.prepare = func(s *phd2test.Server) { s.Scale = phd2test.Pointer(2.5) }
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		phd2 := w.guiders.phd2("east-guider")
		phd2.Broadcast(phd2test.Event("CalibrationComplete", map[string]any{"Mount": "INDI Mount [Telescope Simulator]"}))
		phd2.Broadcast(phd2test.Event("StartGuiding", nil))
		phd2.Broadcast(phd2test.GuideStep(1, 0.3, -0.4))
		phd2.Broadcast(phd2test.GuideStep(2, -0.3, 0.4))
		w.until(time.Minute, "the Guider shows no RMS", func() bool { return guider(w).Status.RMS != nil && guider(w).Status.RMS.Steps == 2 })
		g := guider(w)
		s := g.Status
		if s.RMS.RA != 0.75 || s.RMS.Dec != 1 || s.RMS.Total != 1.25 || s.Display.RMS != "1.25 arcsec" {
			t.Errorf("the RMS is %+v and its display is %q, want RA 0.75, Dec 1, total 1.25, and %q", s.RMS, s.Display.RMS, "1.25 arcsec")
		}
		if !s.RMS.Since.Equal(time.Unix(1790000000, 0)) {
			t.Errorf("the RMS since time is %v, want the time of the first step", s.RMS.Since)
		}
		if s.State != observatory.GuiderGuiding || s.Calibrated == nil || !*s.Calibrated || *s.PixelScale != 2.5 {
			t.Errorf("the Guider status is %s, want state Guiding, calibrated, and pixel scale 2.5", mustJSON(s))
		}
		if s.Star == nil || s.Star.SNR != 41.25 || s.Star.HFD != 2.31 || s.LastStepTime == nil || !s.LastStepTime.Equal(time.Unix(1790000000, 0)) {
			t.Errorf("the star is %+v and the last step time is %v, want SNR 41.25, HFD 2.31, and the time of the last step", s.Star, s.LastStepTime)
		}
		row := printerRow(t, "deploy/guiders-crd.yaml", g)
		t.Logf("kubectl get guider: %v", row)
		if row["Phase"] != "Ready" || row["State"] != "Guiding" || row["RMS"] != "1.25 arcsec" || row["Telescope"] != "east" {
			t.Errorf("kubectl get prints %v, want phase Ready, state Guiding, RMS 1.25 arcsec, and telescope east", row)
		}
		east, _ := decode[observatory.Telescope](t, w.api, kindCollection(observatory.TelescopeKind), "east")
		if g := east.Status.Guider; g == nil || g.Phase != observatory.PhaseReady || g.State != observatory.GuiderGuiding || east.Status.Display.Guider != "Ready, Guiding" {
			t.Errorf("the Telescope status shows guider %+v and column %q, want phase Ready, state Guiding, and %q", g, east.Status.Display.Guider, "Ready, Guiding")
		}

		phd2.Broadcast(phd2test.Event("Alert", map[string]any{"Msg": "Star lost - low HFD", "Type": "warning"}))
		w.until(time.Minute, "the Guider shows no alert", func() bool { return guider(w).Status.Alert != nil })
		if a := guider(w).Status.Alert; a.Message != "Star lost - low HFD" || a.Type != "warning" {
			t.Errorf("the Guider alert is %+v, want the message %q and type warning", a, "Star lost - low HFD")
		}
	})
}

// A PHD2 that starts again has no equipment connected, so the operator
// connects it. When the reservation's holder disconnects the equipment
// of a running PHD2, the operator does not connect it again.
func TestTheRunnerConnectsEachNewPHD2Once(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyGuider(t)
		first := w.guiders.phd2("east-guider")
		first.Set(func(s *phd2test.Server) { s.Equipment = false })
		first.Clear()
		first.Broadcast(phd2test.Event("Alert", map[string]any{"Msg": "INDI camera disconnected", "Type": "error"}))
		w.until(time.Minute, "the Guider is still Ready", func() bool { return guider(w).Status.Phase == observatory.PhaseActivating })
		if strings.Contains(first.Received(), "set_connected") {
			t.Errorf("the operator connected a PHD2 that it had already connected once, and PHD2 received %q, want no set_connected", first.Received())
		}

		w.api.deleteNamed(podsCollection, "east-guider")
		w.until(time.Minute, "the Guider is not Ready again", func() bool {
			return w.guiders.starts("east-guider") == 2 && guider(w).Status.Phase == observatory.PhaseReady
		})
		if got := w.guiders.phd2("east-guider").Received(); !strings.Contains(got, "set_connected") {
			t.Errorf("the new PHD2 received %q, want set_connected", got)
		}
	})
}

// When the INDI server's pod is replaced, PHD2 loses its INDI
// connection but the guider's pod keeps running. The operator connects
// PHD2's equipment again after the new server's devices connect.
func TestANewServerConnectsPHD2Again(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyGuider(t)
		phd2 := w.guiders.phd2("east-guider")
		phd2.Clear()
		before, _ := w.api.object(podsCollection, "east-telescope")

		w.api.deleteNamed(podsCollection, "east-telescope")
		w.until(2*time.Minute, "the Guider is not Ready on the new server", func() bool {
			p, ok := w.api.object(podsCollection, "east-telescope")
			return ok && podUID(p) != podUID(before) && strings.Contains(phd2.Received(), "set_connected") && guider(w).Status.Phase == observatory.PhaseReady
		})
		if starts := w.guiders.starts("east-guider"); starts != 1 {
			t.Errorf("the guider's pod started %d PHD2s, want 1", starts)
		}
	})
}

func TestARestartedOperatorLeavesAReadyGuiderAlone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyGuider(t)
		phd2 := w.guiders.phd2("east-guider")
		phd2.Clear()
		w.restart()
		w.until(time.Minute, "the operator did not connect again", func() bool { return phd2.Dials() == 2 })
		w.until(time.Minute, "the Guider is not Ready", func() bool { return guider(w).Status.Phase == observatory.PhaseReady })
		if got := phd2.Received(); strings.Contains(got, "set_connected") {
			t.Errorf("PHD2 received %q, want no set_connected", got)
		}
		if w.guiders.starts("east-guider") != 1 {
			t.Errorf("the guider's pod started %d times, want 1", w.guiders.starts("east-guider"))
		}
	})
}

// A Guider whose train does not exist, or whose train has no camera,
// fails StartGuider, and the step's summary names what is missing.
func TestAGuiderWithoutItsEquipmentFailsStartGuider(t *testing.T) {
	t.Parallel()
	cases := []struct {
		what   string
		change func(w *world)
		want   string
	}{
		{"a missing train", func(w *world) {
			w.put(observatory.GuiderKind, "east", map[string]any{"telescope": "east", "opticalTrain": "nowhere", "pulses": "Mount"})
		}, "Failed: missing OpticalTrain nowhere on Telescope east"},
		{"a train with no camera", func(w *world) {
			w.put(observatory.OpticalTrainKind, "east-empty", map[string]any{"telescope": "east", "opticalTube": "east-guidescope"})
			w.put(observatory.GuiderKind, "east", map[string]any{"telescope": "east", "opticalTrain": "east-empty", "pulses": "Mount"})
		}, "Failed: no Camera on OpticalTrain east-empty"},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				c.change(w)
				w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
				r := w.phase("east-tonight", observatory.ReservationFailed, 15*time.Minute)
				if s := stepOf(r, observatory.StepStartGuider); s.Summary != c.want {
					t.Errorf("the StartGuider step is %+v, want the summary %q", s, c.want)
				}
			})
		})
	}
}

// When a person deletes a Guider's train while the reservation is
// Ready, the Guider goes to Error and the reservation stays Ready.
func TestAGuiderThatLosesItsTrainIsInError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyGuider(t)
		w.api.deleteNamed(kindCollection(observatory.OpticalTrainKind), "east-guiding")
		w.until(time.Minute, "the Guider is not in Error", func() bool {
			c := conditionOf(guider(w).Status.Conditions, observatory.ConditionReady)
			return c.Reason == string(observatory.PhaseError) && c.Message == "Failed: missing OpticalTrain east-guiding on Telescope east"
		})
		if r, _ := w.reservation("east-tonight"); r.Status.Phase != observatory.ReservationReady {
			t.Errorf("the Reservation phase is %s, want Ready", r.Status.Phase)
		}
	})
}

// A guide tube that a person declares while a reservation is Ready
// is in PHD2's profile from the next reservation on.
func TestANewGuideTubeIsInTheNextReservationsProfile(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyGuider(t)
		w.put(observatory.OpticalTubeKind, "east-guidescope", map[string]any{"telescope": "east", "aperture": 60, "focalLength": 240})
		w.settle()
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.gone(observatory.ReservationKind, "east-tonight", 15*time.Minute)
		w.reserve("east-later", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-later", observatory.ReservationReady, 10*time.Minute)
		files, _ := w.api.object(configMapsCollection, "east-guider")
		if profile := files["data"].(map[string]any)[profileKey].(string); !strings.Contains(profile, "focalLength=240\n") {
			t.Errorf("the profile is:\n%s\nwant it to contain focalLength=240", profile)
		}
	})
}
