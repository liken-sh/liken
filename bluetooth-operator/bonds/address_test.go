package bonds

import "testing"

func TestParseAddressReadsBothForms(t *testing.T) {
	// BlueZ names its directories in the colon form, and a Kubernetes
	// name and a Secret key use the dash form.
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
				t.Errorf("ParseAddress(%q).Directory() = %q, want 04:4A:69:66:92:27", form, got)
			}
			if got := address.Key(); got != "04-4a-69-66-92-27" {
				t.Errorf("ParseAddress(%q).Key() = %q, want 04-4a-69-66-92-27", form, got)
			}
		})
	}
}

// A caller that walks BlueZ's storage tree skips entries that fail to
// parse, so cache and settings must be rejected.
func TestParseAddressRejectsNamesThatAreNotAddresses(t *testing.T) {
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
				t.Errorf("ParseAddress(%q) returned no error, want one because the name is not an address", name)
			}
		})
	}
}

func TestAddressIsZero(t *testing.T) {
	if !(Address{}).IsZero() {
		t.Error("Address{}.IsZero() = false, want true")
	}
	real, err := ParseAddress("04:4A:69:66:92:27")
	if err != nil {
		t.Fatalf("ParseAddress(04:4A:69:66:92:27): %v", err)
	}
	if real.IsZero() {
		t.Error("IsZero() = true for 04:4A:69:66:92:27, want false")
	}
}
