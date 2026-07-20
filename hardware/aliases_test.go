package hardware

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatchModalias(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		value   string
		want    bool
	}{
		{"exact", "usb:v0403p6001", "usb:v0403p6001", true},
		{"trailing star", "usb:v0403p6001d*", "usb:v0403p6001d0600", true},
		{"star matches empty", "usb:v0403*", "usb:v0403", true},
		{"interior stars", "usb:v*p*d*dc*dsc*dp*ic08isc06ip50in*",
			"usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00", true},
		{"interior stars miss", "usb:v*p*d*dc*dsc*dp*ic08isc06ip50in*",
			"usb:v46F4p0001d0100dc00dsc00dp00icFFisc42ip00in00", false},
		{"pci device", "pci:v00001AF4d00001050sv*sd*bc*sc*i*",
			"pci:v00001AF4d00001050sv00001AF4sd00001100bc03sc80i00", true},
		{"pci wrong device", "pci:v00001AF4d00001050sv*sd*bc*sc*i*",
			"pci:v00001AF4d00001041sv00001AF4sd00001100bc02sc00i00", false},
		{"question mark", "usb:v040?p*", "usb:v0403p6001", true},
		{"prefix only is not a match", "usb:v0403", "usb:v0403p6001", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchModalias(tc.pattern, tc.value); got != tc.want {
				t.Errorf("matchModalias(%q, %q) = %v, want %v", tc.pattern, tc.value, got, tc.want)
			}
		})
	}
}

// aliasFile writes a file shaped like modules.alias and returns its path.
func aliasFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "modules.alias")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAliasTable(t *testing.T) {
	path := aliasFile(t, `# Aliases extracted from modules themselves.
alias usb:v*p*d*dc*dsc*dp*ic08isc06ip50in* usb_storage
alias usb:v*p*d*dc*dsc*dp*ic08isc06ip62in* uas
alias usb:v0403p6001d*dc*dsc*dp*ic*isc*ip*in* ftdi_sio
alias pci:v00001AF4d00001050sv*sd*bc*sc*i* virtio_pci
`)
	table, err := LoadAliasTable(path)
	if err != nil {
		t.Fatal(err)
	}

	got := table.Candidates("usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00")
	want := []string{"usb_storage"}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("Candidates = %v, want %v", got, want)
	}

	if got := table.Candidates("pci:v00008086d00001237sv00000000sd00000000bc06sc00i00"); got != nil {
		t.Errorf("Candidates for unmatched modalias = %v, want nil", got)
	}
}

func TestCandidatesMatchMoreThanOneModule(t *testing.T) {
	path := aliasFile(t, `alias usb:v*p*d*dc*dsc*dp*ic08isc06ip50in* usb_storage
alias usb:v*p*d*dc*dsc*dp*ic08isc06ip*in* uas
`)
	table, err := LoadAliasTable(path)
	if err != nil {
		t.Fatal(err)
	}
	got := table.Candidates("usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00")
	if len(got) != 2 || got[0] != "usb_storage" || got[1] != "uas" {
		t.Errorf("Candidates = %v, want [usb_storage uas]", got)
	}
}

func TestCandidatesDeduplicate(t *testing.T) {
	path := aliasFile(t, `alias usb:v0403p6001d*dc*dsc*dp*ic*isc*ip*in* ftdi_sio
alias usb:v0403p*d*dc*dsc*dp*ic*isc*ip*in* ftdi_sio
`)
	table, err := LoadAliasTable(path)
	if err != nil {
		t.Fatal(err)
	}
	got := table.Candidates("usb:v0403p6001d0600dc00dsc00dp00icFFiscFFipFFin00")
	if len(got) != 1 || got[0] != "ftdi_sio" {
		t.Errorf("Candidates = %v, want [ftdi_sio]", got)
	}
}

func TestLoadAliasTableMissingFile(t *testing.T) {
	_, err := LoadAliasTable(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
}
