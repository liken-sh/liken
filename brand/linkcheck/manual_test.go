package linkcheck

import (
	"slices"
	"testing"
)

func TestCheckManualResolvesEveryLink(t *testing.T) {
	problems := CheckManual("testdata/pass", nil)
	if len(problems) != 0 {
		t.Errorf("expected no problems, got %v", problems)
	}
}

func TestCheckManualReportsBrokenLinks(t *testing.T) {
	problems := CheckManual("testdata/broken", nil)
	want := []string{
		"docs/guides/install.md links /docs/guides/upgrade/: no content file answers for /docs/guides/upgrade/",
		`docs/guides/install.md links /docs/guides/install/#the-second-boot: no heading in /docs/guides/install/ renders the id "the-second-boot"`,
	}
	if !slices.Equal(problems, want) {
		t.Errorf("got %v, want %v", problems, want)
	}
}

// An excepted path has no content file behind it, so the check
// reports it as broken until the caller names it in exceptions.
func TestCheckManualReportsAnUnexceptedAsset(t *testing.T) {
	problems := CheckManual("testdata/exception", nil)
	want := []string{
		"_index.md links /release.txt: no content file answers for /release.txt",
	}
	if !slices.Equal(problems, want) {
		t.Errorf("got %v, want %v", problems, want)
	}
}

func TestCheckManualAcceptsAnExceptedAsset(t *testing.T) {
	problems := CheckManual("testdata/exception", []string{"/release.txt"})
	if len(problems) != 0 {
		t.Errorf("expected no problems, got %v", problems)
	}
}
