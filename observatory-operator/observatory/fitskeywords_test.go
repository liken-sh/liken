package observatory

import (
	"bufio"
	"bytes"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/observatory-operator/indi/inditest"
)

func keyword(name, value, comment string) FITSKeyword {
	return FITSKeyword{Name: name, Value: value, Comment: comment}
}

func TestMergeFITSKeywords(t *testing.T) {
	cases := []struct {
		name                                        string
		observatory, telescope, camera, reservation []FITSKeyword
		want                                        []FITSKeyword
	}{
		{"no levels declare keywords", nil, nil, nil, nil, nil},
		{"one level", []FITSKeyword{keyword("OBSERVER", "Ada", "")}, nil, nil, nil,
			[]FITSKeyword{keyword("OBSERVER", "Ada", "")}},
		{"each level adds a name in order",
			[]FITSKeyword{keyword("SITENAME", "Yard", "")},
			[]FITSKeyword{keyword("TELESCOP", "Esprit 100", "")},
			[]FITSKeyword{keyword("INSTRUME", "ASI2600MM", "")},
			[]FITSKeyword{keyword("OBSERVER", "Ada", "")},
			[]FITSKeyword{keyword("SITENAME", "Yard", ""), keyword("TELESCOP", "Esprit 100", ""),
				keyword("INSTRUME", "ASI2600MM", ""), keyword("OBSERVER", "Ada", "")}},
		{"the reservation wins over every other level",
			[]FITSKeyword{keyword("OBSERVER", "Observatory", "site")},
			[]FITSKeyword{keyword("OBSERVER", "Telescope", "scope")},
			[]FITSKeyword{keyword("OBSERVER", "Camera", "camera")},
			[]FITSKeyword{keyword("OBSERVER", "Reservation", "")},
			[]FITSKeyword{keyword("OBSERVER", "Reservation", "")}},
		{"the camera wins over the telescope and keeps the telescope's place",
			nil,
			[]FITSKeyword{keyword("TELESCOP", "Esprit 100", ""), keyword("FOCUSER", "EAF", "")},
			[]FITSKeyword{keyword("FOCUSER", "Celestron", "on this camera's train"), keyword("TELESCOP", "RedCat", "")},
			nil,
			[]FITSKeyword{keyword("TELESCOP", "RedCat", ""), keyword("FOCUSER", "Celestron", "on this camera's train")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MergeFITSKeywords(c.observatory, c.telescope, c.camera, c.reservation)
			if !slices.Equal(got, c.want) {
				t.Errorf("merged = %+v, want %+v", got, c.want)
			}
		})
	}
}

// headerKeywords returns the keyword of each card of
// inditest.FrameHeader, in order.
func headerKeywords(t *testing.T) []string {
	t.Helper()
	var out []string
	lines := bufio.NewScanner(bytes.NewReader(inditest.FrameHeader()))
	for lines.Scan() {
		// A card's keyword is in its first eight columns.
		card := lines.Text()
		out = append(out, strings.TrimSpace(card[:min(8, len(card))]))
	}
	if err := lines.Err(); err != nil {
		t.Fatalf("reading the captured header: %v", err)
	}
	return out
}

// DriverFITSKeywords must list every keyword of the captured header
// except OBJECT, which the capture declared, and COMMENT and END. So a
// capture taken after a bump of the INDI images fails this test until
// DriverFITSKeywords lists each new keyword. The API server must refuse
// each keyword except INSTRUME, TELESCOP, and OBJECT.
func TestTheDriversKeywordsAreRefused(t *testing.T) {
	declarable := []string{"INSTRUME", "TELESCOP", "OBJECT"}
	for _, name := range headerKeywords(t) {
		t.Run(name, func(t *testing.T) {
			if !slices.Contains(DriverFITSKeywords, name) && !slices.Contains([]string{"OBJECT", "COMMENT", "END"}, name) {
				t.Errorf("the driver writes %s, and DriverFITSKeywords does not name it", name)
			}
			refusal := "is a keyword that the camera's driver writes"
			if slices.Contains([]string{"COMMENT", "END"}, name) {
				refusal = "is a keyword that FITS reserves"
			}
			if slices.Contains(declarable, name) {
				refusal = ""
			}
			spec := camera(map[string]any{"fitsKeywords": []any{map[string]any{"name": name, "value": "1"}}})
			errs := validate(t, CameraKind, resource(CameraKind, spec), nil)
			if got := errs.ToAggregate(); !refuses(got, refusal) {
				t.Errorf("errors = %v, want %q", got, refusal)
			}
		})
	}
}

// schema/ generates spec.fitsKeywords into the CRDs of the Observatory,
// the Telescope, and the Camera. The Reservation's CRD is written by
// hand, and its copy must stay equal to the generated one.
func TestEachLevelHasTheSameFITSKeywordsSchema(t *testing.T) {
	want := schemaOf(t, ObservatoryKind).Properties["spec"].Properties["fitsKeywords"]
	for _, kind := range []Kind{TelescopeKind, CameraKind, ReservationKind} {
		t.Run(kind.Name, func(t *testing.T) {
			got := schemaOf(t, kind).Properties["spec"].Properties["fitsKeywords"]
			if !reflect.DeepEqual(got, want) {
				t.Errorf("spec.fitsKeywords of %s differs from the Observatory's", kind.Name)
			}
		})
	}
}

func TestEveryLevelRefusesFRAMEID(t *testing.T) {
	frameID := []any{fits("FRAMEID", "0199d3a0-0000-7000-8000-000000000001", "")}
	cases := []struct {
		kind Kind
		spec map[string]any
	}{
		{ObservatoryKind, map[string]any{"location": map[string]any{"latitude": 51.5, "longitude": 0.0, "elevation": 40.0}, "fitsKeywords": frameID}},
		{TelescopeKind, map[string]any{"observatory": "lab", "fitsKeywords": frameID}},
		{CameraKind, camera(map[string]any{"fitsKeywords": frameID})},
		{ReservationKind, map[string]any{"telescope": "east", "holder": "desktop", "fitsKeywords": frameID}},
	}
	for _, c := range cases {
		t.Run(c.kind.Name, func(t *testing.T) {
			errs := validate(t, c.kind, resource(c.kind, c.spec), nil)
			if got := errs.ToAggregate(); !refuses(got, "FRAMEID is a keyword that astrophotography-operator writes") {
				t.Errorf("errors = %v, want one that holds \"FRAMEID is a keyword that astrophotography-operator writes\"", got)
			}
		})
	}
}
