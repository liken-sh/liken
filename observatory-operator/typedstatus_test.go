package main

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// A NaN or infinite number, such as a failed sensor's reading, is left
// out, because a status that JSON cannot encode is never written.
func TestANumberThatJSONCannotHoldStaysAbsent(t *testing.T) {
	t.Parallel()
	nan := math.NaN()
	r := newReader([]indi.Property{
		{Name: "CCD_TEMPERATURE", Type: indi.NumberType, Perm: indi.ReadWrite, State: indi.Ok,
			Members: []indi.Member{{Name: "CCD_TEMPERATURE_VALUE", Number: nan, Min: -50, Max: math.Inf(1)}}},
		{Name: "WEATHER_PARAMETERS", Type: indi.NumberType, Perm: indi.ReadOnly, State: indi.Ok,
			Members: []indi.Member{{Name: "WEATHER_TEMPERATURE", Number: nan}}},
	})
	camera := standardStatus(observatory.CameraKind, r).(*observatory.CameraStandard)
	if camera.Temperature == nil || camera.Temperature.State != indi.Ok || camera.Temperature.Value != nil {
		t.Errorf("temperature = %+v, want its state and no value", camera.Temperature)
	}
	if got := limits(r); got != nil {
		t.Errorf("limits = %v, want none for a range with no end", got)
	}
	station := standardStatus(observatory.WeatherStationKind, r).(*observatory.WeatherStationStandard)
	if station.Parameters == nil || len(station.Parameters.Elements) != 1 || station.Parameters.Elements[0].Value != nil {
		t.Errorf("parameters = %+v, want one element with no value", station.Parameters)
	}
	status := deviceStatus{Standard: camera}
	if _, err := json.Marshal(status); err != nil {
		t.Errorf("encoding the status: %v, want no error", err)
	}
}

// The typed fields encode at the top level of the status, beside the
// shared fields, and absent typed fields encode to nothing.
func TestTheTypedFieldsAreWrittenBesideTheRest(t *testing.T) {
	t.Parallel()
	value := 3.0
	cases := []struct {
		name   string
		status deviceStatus
		want   string
	}{
		{"no typed fields", deviceStatus{DeviceStatus: observatory.DeviceStatus{Phase: observatory.DeviceIdle}}, `{"phase":"Idle"}`},
		{"typed fields that are all absent", deviceStatus{DeviceStatus: observatory.DeviceStatus{Phase: observatory.DeviceIdle}, Standard: &observatory.FilterWheelStandard{}}, `{"phase":"Idle"}`},
		{"typed fields alone", deviceStatus{Standard: &observatory.FilterWheelStandard{Slot: &observatory.FilterWheelSlotStatus{Value: &value}}}, `{"slot":{"value":3}}`},
		{"both", deviceStatus{DeviceStatus: observatory.DeviceStatus{Phase: observatory.DeviceConnected}, Standard: &observatory.FilterWheelStandard{Slot: &observatory.FilterWheelSlotStatus{Value: &value}}},
			`{"phase":"Connected","slot":{"value":3}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, err := json.Marshal(c.status)
			if err != nil || string(body) != c.want {
				t.Errorf("JSON = %s, %v, want %s", body, err, c.want)
			}
		})
	}
}

func TestTheLimitsHoldOnlyTheRangesAClientMeets(t *testing.T) {
	t.Parallel()
	r := newReader([]indi.Property{
		{Name: "CCD_FRAME", Type: indi.NumberType, Perm: indi.ReadWrite,
			Members: []indi.Member{{Name: "X", Min: 0, Max: 0}, {Name: "WIDTH", Min: 1, Max: 4096, Step: 1}}},
		{Name: "CCD_INFO", Type: indi.NumberType, Perm: indi.ReadOnly,
			Members: []indi.Member{{Name: "CCD_MAX_X", Min: 1, Max: 16000}}},
	})
	want := map[string]map[string]observatory.Limit{"CCD_FRAME": {"WIDTH": {Min: 1, Max: 4096, Step: 1}}}
	if got := limits(r); !reflect.DeepEqual(got, want) {
		t.Errorf("limits = %v, want %v", got, want)
	}
}
