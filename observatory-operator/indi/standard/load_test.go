package standard

// These cases build the sources of a small standard set in a
// directory of their own: captures, a page, a conventions table, and
// simulator transcripts. Each one shows a disagreement that Load
// reports, or a mistake in a source that fails the read.

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// sources writes files into a new directory, by their paths in it,
// and answers the directory. Every set has an empty conventions table
// unless the case gives its own.
func sources(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if _, given := files[ConventionsFile]; !given {
		files[ConventionsFile] = "[]\n"
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// focuserSpeed is a capture of one focuser variant that defines
// FOCUS_SPEED with a permission.
func focuserSpeed(perm string) string {
	return `<capture><phase name="connect"><defNumberVector name="FOCUS_SPEED" perm="` + perm +
		`"><defNumber name="FOCUS_SPEED_VALUE" label="Speed"/></defNumberVector></phase></capture>`
}

func TestLoadReportsEachDisagreement(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"two variants with other permissions", map[string]string{
			"focuser.xml": focuserSpeed("rw"), "focuser-timer.xml": focuserSpeed("ro"),
		}, `FOCUS_SPEED: focuser.xml gives the permission "rw", and focuser-timer.xml gives "ro"`},
		{"a simulator that defines a convention another way", map[string]string{
			"focuser.xml":      focuserSpeed("rw"),
			ConventionsFile:    "- {name: FOCUS_TEMPERATURE, interface: focuser, type: Number, perm: ro, label: T, members: [{name: TEMPERATURE, label: C}], simulators: [focus]}\n",
			"sims/focus/a.xml": `<defNumberVector name="FOCUS_TEMPERATURE" perm="rw"><defNumber name="TEMPERATURE"/></defNumberVector>`,
		}, `FOCUS_TEMPERATURE: conventions.yaml gives the permission "ro", and the focus simulator gives "rw"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := sources(t, c.files)
			_, report, err := Load(dir, filepath.Join(dir, "sims"))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(report, c.want) {
				t.Errorf("report = %q, want it to hold %q", report, c.want)
			}
		})
	}
}

func TestLoadRefusesAMistakeInASource(t *testing.T) {
	speed := focuserSpeed("rw")
	cases := []struct {
		name  string
		files map[string]string
		error string
	}{
		{"a capture that is no XML", map[string]string{"focuser.xml": "<capture><phase>"}, "focuser.xml"},
		{"a conventions table that is no table", map[string]string{"focuser.xml": speed, ConventionsFile: "name: X\n"}, "conventions.yaml"},
		{"a convention of an interface with no capture", map[string]string{"focuser.xml": speed,
			ConventionsFile: "- {name: X, interface: dome, type: Number, perm: ro, label: X, members: [], simulators: []}\n"}, `in the interface "dome"`},
		{"a convention that the capture defines", map[string]string{"focuser.xml": speed,
			ConventionsFile: "- {name: FOCUS_SPEED, interface: focuser, type: Number, perm: rw, label: S, members: [], simulators: []}\n"}, "the capture of focuser defines it"},
		{"a convention that names the wrong simulators", map[string]string{"focuser.xml": speed,
			ConventionsFile: "- {name: X, interface: focuser, type: Number, perm: ro, label: X, members: [], simulators: [focus]}\n"}, "names the simulators [focus] for X"},
		{"a transcript that is no XML", map[string]string{"focuser.xml": speed, "sims/focus/a.xml": "<def"}, "a.xml"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := sources(t, c.files)
			_, _, err := Load(dir, filepath.Join(dir, "sims"))
			if err == nil || !strings.Contains(err.Error(), c.error) {
				t.Errorf("Load = %v, want an error with %q", err, c.error)
			}
		})
	}
}

// The committed captures merge into one set for each interface: the
// park position of the telescope holds the members of every park
// type, and the camera's set holds the conventions of the CCD.
func TestLoadMergesTheVariantsAndTheConventions(t *testing.T) {
	sets, report, err := Load(".", "../inditest/testdata")
	if err != nil {
		t.Fatal(err)
	}
	park, _ := sets["telescope"].Property("TELESCOPE_PARK_POSITION")
	var members []string
	for _, m := range park.Members {
		members = append(members, m.Name)
	}
	gain, _ := sets["ccd"].Property("CCD_GAIN")
	cases := []struct {
		name      string
		got, want any
	}{
		{"the park position's members", members, []string{"PARK_AZ", "PARK_ALT", "PARK_HA", "PARK_DEC", "PARK_RA"}},
		{"the gain is a convention", gain.Convention, true},
		{"the focus simulator's temperature is reported", slices.Contains(report,
			`FOCUS_TEMPERATURE: conventions.yaml gives the permission "ro", and the focus simulator gives "rw"`), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !reflect.DeepEqual(c.got, c.want) {
				t.Errorf("got %v, want %v", c.got, c.want)
			}
		})
	}
}
