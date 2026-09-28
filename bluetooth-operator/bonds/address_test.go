package bonds

import "testing"

func TestParseAddressReadsBothForms(t *testing.T) {
	// BlueZ names its directories in the first form, and a Kubernetes
	// name and a Secret key use the second.
	for _, form := range []string{
		"04:4A:69:66:92:27",
		"04-4a-69-66-92-27",
		"04:4a:69:66:92:27",
		"04-4A-69-66-92-27",
	} {
		t.Run(form, func(t *testing.T) {
			address, err := ParseAddress(form)
			if err != nil {
				t.Fatalf("ParseAddress(%q): %v", form, err)
			}
			if got := address.Directory(); got != "04:4A:69:66:92:27" {
				t.Errorf("Directory = %q", got)
			}
			if got := address.Key(); got != "04-4a-69-66-92-27" {
				t.Errorf("Key = %q", got)
			}
		})
	}
}

// A device directory that does not parse is skipped rather than
// fatal, so the rejections here keep cache/ and settings out of the
// API.
func TestParseAddressRejectsAnythingElse(t *testing.T) {
	for _, name := range []string{
		"",
		"cache",
		"settings",
		"04:4A:69:66:92",
		"04:4A:69:66:92:27:80",
		"04:4A:69:66:92:2G",
		"4:4A:69:66:92:27",
		"044A69669227",
		"04:4A:69:66:92:+7",
		"04:4A:69:66:92:0x",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseAddress(name); err == nil {
				t.Errorf("ParseAddress(%q) accepted a name that is not an address", name)
			}
		})
	}
}

func TestAddressIsZero(t *testing.T) {
	if !(Address{}).IsZero() {
		t.Error("the all-zero address did not report itself zero")
	}
	real, err := ParseAddress("04:4A:69:66:92:27")
	if err != nil {
		t.Fatal(err)
	}
	if real.IsZero() {
		t.Error("a real address reported itself zero")
	}
}
