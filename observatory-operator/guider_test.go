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
				t.Errorf("profile:\n%s\nwant:\n%s", got, c.want)
			}
		})
	}
}

// The guider's pod runs PHD2 beside the pinned weston as a native
// sidecar, with no device claim, on the server's node.
func TestTheGuiderPodRunsPHD2BesideItsCompositor(t *testing.T) {
	t.Parallel()
	guider := &observatory.Guider{Metadata: observatory.ObjectMeta{Name: "east", UID: "u1"}, Spec: observatory.GuiderSpec{Telescope: "east", OpticalTrain: "east-guiding", Pulses: observatory.PulsesMount}}
	files, p, svc, err := guiderPod(testNamespace, serverRef{observatory.TelescopeKind, "east"}, guider, guiderEquipment{camera: "Guide Simulator", mount: "Telescope Simulator", focalLength: 200})
	if err != nil {
		t.Fatal(err)
	}
	if p.Metadata.Name != "east-guider" || files.Metadata.Name != "east-guider" || svc.Metadata.Name != "east-guider" {
		t.Errorf("names = %s, %s, %s", p.Metadata.Name, files.Metadata.Name, svc.Metadata.Name)
	}
	sidecar, phd2 := p.Spec.InitContainers[0], p.Spec.Containers[0]
	if sidecar.RestartPolicy != "Always" || sidecar.StartupProbe == nil || !strings.HasPrefix(sidecar.Image, "ghcr.io/liken-sh/weston:") {
		t.Errorf("sidecar = %s", mustJSON(sidecar))
	}
	if !strings.HasPrefix(phd2.Image, "ghcr.io/liken-sh/indi-phd2:") || phd2.ReadinessProbe == nil || phd2.Ports[0].ContainerPort != 4400 {
		t.Errorf("phd2 = %s", mustJSON(phd2))
	}
	for _, c := range []container{sidecar, phd2} {
		if !c.SecurityContext.ReadOnlyRootFilesystem || c.SecurityContext.RunAsUser != 1000 || c.Resources != nil {
			t.Errorf("%s: security %+v, resources %+v", c.Name, c.SecurityContext, c.Resources)
		}
		if !slices.ContainsFunc(c.VolumeMounts, func(m volumeMount) bool { return m.MountPath == waylandDir }) {
			t.Errorf("%s does not share the Wayland socket", c.Name)
		}
	}
	if len(p.Spec.ResourceClaims) != 0 || *p.Spec.TerminationGracePeriodSeconds != 1 || mustJSON(p.Spec.Affinity) != mustJSON(besideEast) {
		t.Errorf("spec = %s", mustJSON(p.Spec))
	}
	if svc.Spec.Ports[0].Port != 4400 || p.Metadata.OwnerReferences[0].Kind != "Guider" {
		t.Errorf("service %s, owners %+v", mustJSON(svc.Spec), p.Metadata.OwnerReferences)
	}
	if !strings.Contains(files.Data[westonKey], "require-input=false") || !strings.Contains(files.Data[profileKey], "ConfigVersion=2001") {
		t.Errorf("files = %v", files.Data)
	}
}

// A change to the profile replaces the pod, because PHD2 reads its
// profile only when it starts.
func TestAnotherProfileStampsAnotherPod(t *testing.T) {
	t.Parallel()
	guider := &observatory.Guider{Metadata: observatory.ObjectMeta{Name: "east"}, Spec: observatory.GuiderSpec{Pulses: observatory.PulsesMount}}
	ref := serverRef{observatory.TelescopeKind, "east"}
	_, a, _, _ := guiderPod(testNamespace, ref, guider, guiderEquipment{camera: "Guide Simulator", mount: "Telescope Simulator", focalLength: 200})
	_, b, _, _ := guiderPod(testNamespace, ref, guider, guiderEquipment{camera: "Guide Simulator", mount: "Telescope Simulator", focalLength: 400})
	if current(a, b) {
		t.Error("two profiles stamped one digest")
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
			t.Errorf("StartGuider = %+v", s)
		}
		phd2 := w.guiders.phd2("east-guider")
		if got := phd2.Received(); !strings.Contains(got, "set_connected") || strings.Contains(got, "loop") || strings.Contains(got, "guide") {
			t.Errorf("PHD2 received %q", got)
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
			t.Errorf("status = %s", mustJSON(s))
		}
		if c := conditionOf(s.Conditions, observatory.ConditionReady); c.Status != observatory.ConditionTrue || c.Message != "PHD2 is connected to its camera and mount" {
			t.Errorf("Ready = %+v", c)
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
			t.Errorf("StartGuider = %+v", s)
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
			t.Errorf("StartGuider = %+v", s)
		}
		w.until(time.Minute, "the Guider is not in Error", func() bool {
			c := conditionOf(guider(w).Status.Conditions, observatory.ConditionReady)
			return c.Reason == string(observatory.PhaseError) && c.Message == "Reservation east-tonight failed"
		})
	})
}

// Abort stops PHD2 before anything else, and StopGuider deletes the
// guider's pod while the camera and the mount are still connected, so
// PHD2 never sees them drop.
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
			t.Errorf("PHD2 received %q", phd2.Received())
		}
		if !slices.Contains(atStop, "Guide Simulator") || !slices.Contains(atStop, "Telescope Simulator") {
			t.Errorf("connected when PHD2 stopped: %v", atStop)
		}
		if names := w.api.names(configMapsCollection); len(names) != 0 {
			t.Errorf("ConfigMaps left: %v", names)
		}
	})
}

func TestAbortNamesThePHD2ItStopped(t *testing.T) {
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
		if s := stepOf(r, observatory.StepAbort); s.State != observatory.StepDone || s.Summary != "Stopped PHD2, which was Looping" {
			t.Errorf("Abort = %+v", s)
		}
		if s := stepOf(r, observatory.StepStopGuider); s.State != observatory.StepDone || s.Summary != "Stopped east-guider" {
			t.Errorf("StopGuider = %+v", s)
		}
	})
}

// printerRow answers what kubectl get prints for an object: each
// column of the kind's CRD with priority 0, from its JSONPath.
func printerRow(t *testing.T, file string, object any) map[string]string {
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}
	row := map[string]string{}
	for _, column := range crd.Spec.Versions[0].AdditionalPrinterColumns {
		if column.Priority != 0 || column.Type == "date" {
			continue
		}
		path := jsonpath.New(column.Name).AllowMissingKeys(true)
		if err := path.Parse("{" + column.JSONPath + "}"); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := path.Execute(&out, asJSON(object)); err != nil {
			t.Fatal(err)
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
			t.Errorf("rms = %+v, display %q", s.RMS, s.Display.RMS)
		}
		if s.State != observatory.GuiderGuiding || s.Calibrated == nil || !*s.Calibrated || *s.PixelScale != 2.5 {
			t.Errorf("status = %s", mustJSON(s))
		}
		if s.Star == nil || s.Star.SNR != 41.25 || s.Star.HFD != 2.31 || s.LastStepTime == nil || !s.LastStepTime.Equal(time.Unix(1790000000, 0)) {
			t.Errorf("star %+v at %v", s.Star, s.LastStepTime)
		}
		row := printerRow(t, "deploy/guiders-crd.yaml", g)
		t.Logf("kubectl get guider: %v", row)
		if row["Phase"] != "Ready" || row["State"] != "Guiding" || row["RMS"] != "1.25 arcsec" || row["Telescope"] != "east" {
			t.Errorf("row = %v", row)
		}
		east, _ := decode[observatory.Telescope](t, w.api, kindCollection(observatory.TelescopeKind), "east")
		if g := east.Status.Guider; g == nil || g.Phase != observatory.PhaseReady || g.State != observatory.GuiderGuiding || east.Status.Display.Guider != "Ready, Guiding" {
			t.Errorf("telescope guider = %+v, column %q", g, east.Status.Display.Guider)
		}

		phd2.Broadcast(phd2test.Event("Alert", map[string]any{"Msg": "Star lost - low HFD", "Type": "warning"}))
		w.until(time.Minute, "the Guider shows no alert", func() bool { return guider(w).Status.Alert != nil })
		if a := guider(w).Status.Alert; a.Message != "Star lost - low HFD" || a.Type != "warning" {
			t.Errorf("alert = %+v", a)
		}
	})
}

// A PHD2 that restarts starts with nothing connected, and the runner
// connects it again. A PHD2 whose holder disconnected its equipment
// stays disconnected.
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
			t.Errorf("the runner connected a PHD2 that it had connected once: %q", first.Received())
		}

		w.api.deleteNamed(podsCollection, "east-guider")
		w.until(time.Minute, "the Guider is not Ready again", func() bool {
			return w.guiders.starts("east-guider") == 2 && guider(w).Status.Phase == observatory.PhaseReady
		})
		if got := w.guiders.phd2("east-guider").Received(); !strings.Contains(got, "set_connected") {
			t.Errorf("the new PHD2 received %q", got)
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
			t.Errorf("PHD2 received %q", got)
		}
		if w.guiders.starts("east-guider") != 1 {
			t.Errorf("the guider's pod started %d times", w.guiders.starts("east-guider"))
		}
	})
}

// A Guider that names what the telescope lacks fails StartGuider with
// a message that names the missing piece.
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
					t.Errorf("StartGuider = %+v", s)
				}
			})
		})
	}
}

// While the reservation is Ready, a Guider that loses its train turns
// to Error, and the reservation stays Ready.
func TestAGuiderThatLosesItsTrainIsInError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyGuider(t)
		w.put(observatory.GuiderKind, "east", map[string]any{"telescope": "east", "opticalTrain": "nowhere", "pulses": "Mount"})
		w.until(time.Minute, "the Guider is not in Error", func() bool {
			c := conditionOf(guider(w).Status.Conditions, observatory.ConditionReady)
			return c.Reason == string(observatory.PhaseError) && c.Message == "Failed: missing OpticalTrain nowhere on Telescope east"
		})
		if r, _ := w.reservation("east-tonight"); r.Status.Phase != observatory.ReservationReady {
			t.Errorf("reservation = %s", r.Status.Phase)
		}
	})
}

// A profile change while the reservation is Ready, such as a new guide
// tube, writes the ConfigMap and replaces the guider's pod.
func TestANewGuideTubeReplacesTheGuiderPod(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyGuider(t)
		w.put(observatory.OpticalTubeKind, "east-guidescope", map[string]any{"telescope": "east", "aperture": 60, "focalLength": 240})
		w.until(time.Minute, "the guider's pod did not start again", func() bool {
			return w.guiders.starts("east-guider") == 2 && guider(w).Status.Phase == observatory.PhaseReady
		})
		files, _ := w.api.object(configMapsCollection, "east-guider")
		if profile := files["data"].(map[string]any)[profileKey].(string); !strings.Contains(profile, "focalLength=240\n") {
			t.Errorf("profile:\n%s", profile)
		}
	})
}
