package linkcheck

import (
	"slices"
	"testing"
)

func TestCheckManualResolvesEveryLink(t *testing.T) {
	problems := CheckManual("testdata/pass", nil)
	if len(problems) != 0 {
		t.Errorf("the problems are %v, want none", problems)
	}
}

func TestCheckManualReportsBrokenLinks(t *testing.T) {
	problems := CheckManual("testdata/broken", nil)
	want := []string{
		"docs/guides/install.md links /docs/guides/upgrade/: no content file serves /docs/guides/upgrade/",
		`docs/guides/install.md links /docs/guides/install/#the-second-boot: /docs/guides/install/ has no heading with the id "the-second-boot"`,
	}
	if !slices.Equal(problems, want) {
		t.Errorf("the problems are %v, want %v", problems, want)
	}
}

// A link to a file that is not content fails until the caller lists
// its path in exceptions.
func TestCheckManualReportsAnUnexceptedAsset(t *testing.T) {
	problems := CheckManual("testdata/exception", nil)
	want := []string{
		"_index.md links /release.txt: no content file serves /release.txt",
	}
	if !slices.Equal(problems, want) {
		t.Errorf("the problems are %v, want %v", problems, want)
	}
}

func TestCheckManualAcceptsAnExceptedAsset(t *testing.T) {
	problems := CheckManual("testdata/exception", []string{"/release.txt"})
	if len(problems) != 0 {
		t.Errorf("the problems are %v, want none", problems)
	}
}
