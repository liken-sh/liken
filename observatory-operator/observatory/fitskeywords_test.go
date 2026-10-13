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

// A later level replaces the value and the comment of a name, the name
// keeps its first place, and a level with no keywords changes nothing.
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

// headerKeywords answers the keyword of each card of the frame header
// that inditest.FrameHeader answers, in order.
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
		t.Fatal(err)
	}
	return out
}

// Every keyword of the captured header is on the driver's list, except
// OBJECT, which the capture declared, and COMMENT and END, which hold
// no value. The API server refuses each one in spec.fitsKeywords,
// except INSTRUME, TELESCOP, and OBJECT. A capture after a bump of the
// INDI images that holds a keyword the driver did not write before
// fails here, until DriverFITSKeywords names it.
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

// The four kinds that declare keywords hold one schema for the field.
// schema/ writes it into three of the CRDs, and the Reservation's CRD
// is written by hand, so this holds its copy equal to theirs.
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

// astrophotography-operator writes FRAMEID, a new identity for each
// frame that it takes. A declared FRAMEID would put one identity on
// every frame of a session, the frames of a desktop client included,
// so every level refuses it, with a message that names the writer.
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
				t.Errorf("errors = %v", got)
			}
		})
	}
}
