package observatory

// Each closed vocabulary is a named string type in Go, with one
// constant for each value, and an enum in the CRD. The API server
// refuses a status write with a value that the enum lacks, so the two
// lists must hold the same values.

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func strs[T ~string](values ...T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}

var (
	devicePhases = strs(DeviceInventory, DeviceIdle, DeviceStarting, DeviceConnecting,
		DeviceConnected, DeviceDisconnecting, DeviceError)
	phases = strs(PhaseIdle, PhaseActivating, PhaseReady,
		PhaseDeactivating, PhaseError)
	safeties          = strs(SafetySafe, SafetyWarning, SafetyDanger, SafetyUnknown)
	reservationPhases = strs(ReservationScheduled, ReservationActivating,
		ReservationReady, ReservationDeactivating, ReservationReleased, ReservationFailed)
	stepNames    = strs(append(slices.Clone(ActivationSteps), DeactivationSteps...)...)
	guiderStates = strs(GuiderStopped, GuiderSelected, GuiderCalibrating, GuiderGuiding,
		GuiderLostLock, GuiderPaused, GuiderLooping)
	stepStates = strs(StepPending, StepRunning, StepDone, StepFailed, StepSkipped)
	covers     = []string{CoverOpen, CoverClosed, CoverMoving}
	refKinds   = func() []string {
		var names []string
		for _, kind := range Kinds {
			if kind != ReservationKind {
				names = append(names, kind.Name)
			}
		}
		return names
	}()
	conditionStatuses = strs(ConditionTrue, ConditionFalse, ConditionUnknown)
	deviceKind        = func() []string {
		var names []string
		for _, kind := range DeviceKinds {
			names = append(names, kind.Name)
		}
		return names
	}()
)

func TestEachEnumHoldsTheGoConstants(t *testing.T) {
	cases := []struct {
		kind Kind
		path string
		want []string
	}{
		{MountKind, "status.phase", devicePhases},
		{ObservatoryKind, "status.phase", phases},
		{TelescopeKind, "status.phase", phases},
		{GuiderKind, "status.phase", phases},
		{ObservatoryKind, "status.weather", safeties},
		{ObservatoryKind, "status.devices[].kind", deviceKind},
		{ObservatoryKind, "status.devices[].phase", devicePhases},
		{TelescopeKind, "status.devices[].phase", devicePhases},
		{OpticalTrainKind, "status.devices[].kind", deviceKind},
		{OpticalTrainKind, "status.devices[].phase", devicePhases},
		{TelescopeKind, "status.trains[].devices[].phase", devicePhases},
		{WeatherStationKind, "status.readings.safety", safeties},
		{WeatherStationKind, "status.readings.parameters[].safety", safeties},
		{DustCapKind, "status.readings.cover", covers},
		{DomeKind, "status.readings.shutter", covers},
		{GuiderKind, "spec.pulses", strs(PulsesMount, PulsesCamera)},
		{GuiderKind, "status.state", guiderStates},
		{TelescopeKind, "status.guider.phase", phases},
		{TelescopeKind, "status.guider.state", guiderStates},
		{ReservationKind, "status.phase", reservationPhases},
		{ReservationKind, "status.step", stepNames},
		{ReservationKind, "status.steps[].name", stepNames},
		{ReservationKind, "status.steps[].state", stepStates},
		{ReservationKind, "status.conditions[].status", conditionStatuses},
		{ReservationKind, "status.steps[].actions[].state", stepStates},
		{DomeKind, "spec.activation[].state", strs(StateParked, StateUnparked)},
		{MountKind, "spec.deactivation[].state", strs(StateParked, StateUnparked)},
		{DustCapKind, "spec.activation[].state", strs(StateOpen, StateClosed)},
		{FlatPanelKind, "spec.deactivation[].state", strs(StateLit, StateDark)},
		{CameraKind, "spec.activation[].requires[].kind", refKinds},
		{CameraKind, "spec.activation[].requires[].status", conditionStatuses},
		{ObservatoryKind, "spec.deactivation[].after[].kind", refKinds},
		{TelescopeKind, "status.procedures[].state", stepStates},
		{DomeKind, "spec.triggers[].when.kind", refKinds},
		{DomeKind, "spec.triggers[].when.status", conditionStatuses},
		{MountKind, "spec.triggers[].run[].state", strs(StateParked, StateUnparked)},
		{MountKind, "status.procedures[].actions[].state", stepStates},
	}
	for _, c := range cases {
		t.Run(c.kind.Name+"."+c.path, func(t *testing.T) {
			field := resolve(schemaOf(t, c.kind), strings.Split(c.path, "."))
			if got := enumOf(t, field); !slices.Equal(got, c.want) {
				t.Errorf("enum = %v, want %v", got, c.want)
			}
		})
	}
}

func enumOf(t *testing.T, field *apiextensionsv1.JSONSchemaProps) []string {
	t.Helper()
	var values []string
	for _, raw := range field.Enum {
		var value string
		if err := json.Unmarshal(raw.Raw, &value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	return values
}
