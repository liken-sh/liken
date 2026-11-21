package main

import (
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestParsePathsAccepts(t *testing.T) {
	for _, c := range []struct {
		name  string
		value string
		want  []string
	}{
		{name: "the comma form", value: "media-operator/transect,audio-operator/transect",
			want: []string{"audio-operator/transect", "media-operator/transect"}},
		{name: "the block scalar form", value: "media-operator/transect\naudio-operator/transect\n",
			want: []string{"audio-operator/transect", "media-operator/transect"}},
		{name: "commas and newlines mixed", value: "a, b\nc",
			want: []string{"a", "b", "c"}},
		{name: "spaces around an entry", value: "  a/b  ,\t c  ",
			want: []string{"a/b", "c"}},
		{name: "a trailing slash", value: "docs/,README.md",
			want: []string{"README.md", "docs"}},
		{name: "a duplicate and an order", value: "b,a,b,a/",
			want: []string{"a", "b"}},
		{name: "a bracket that is part of a name", value: "pages/[id]",
			want: []string{"pages/[id]"}},
		{name: "a colon in a name", value: "odd:name",
			want: []string{"odd:name"}},
		{name: "a leading colon", value: ":(top)x",
			want: []string{":(top)x"}},
		{name: "a glob character and a backslash", value: `*.md,a\b`,
			want: []string{"*.md", `a\b`}},
		{name: "a name that starts with .git", value: ".github/workflows",
			want: []string{".github/workflows"}},
		{name: "empty entries between others", value: ",a,,\n\nb,",
			want: []string{"a", "b"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := parsePaths(c.value)
			if err != nil {
				t.Fatalf("parsePaths(%q): %v", c.value, err)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("parsePaths(%q) returned %q, want %q", c.value, got, c.want)
			}
		})
	}
}

func TestParsePathsRefuses(t *testing.T) {
	for _, c := range []struct {
		name        string
		value       string
		wantMessage string
	}{
		{name: "an absolute entry", value: "a,/etc",
			wantMessage: `paths: "/etc" starts with a slash, and an entry is relative to the root of the repository`},
		{name: "a dot", value: ".",
			wantMessage: `paths: "." has an empty, . or .. component`},
		{name: "a dot component", value: "a/./b",
			wantMessage: `paths: "a/./b" has an empty, . or .. component`},
		{name: "a parent component", value: "a/../b",
			wantMessage: `paths: "a/../b" has an empty, . or .. component`},
		{name: "a leading parent component", value: "../a",
			wantMessage: `paths: "../a" has an empty, . or .. component`},
		{name: "an empty component", value: "a//b",
			wantMessage: `paths: "a//b" has an empty, . or .. component`},
		{name: "a .git entry", value: ".git",
			wantMessage: `paths: ".git" has .git as a component, and the driver never checks out git's own directory`},
		{name: "a .git component", value: "a/.git/config",
			wantMessage: `paths: "a/.git/config" has .git as a component, and the driver never checks out git's own directory`},
		{name: "a trailing slash on a .git entry", value: ".git/",
			wantMessage: `paths: ".git/" has .git as a component, and the driver never checks out git's own directory`},
		{name: "a value with no entry", value: " , ",
			wantMessage: `paths: " , " has no entry, and an empty list would check out the whole tree`},
		{name: "an empty value", value: "",
			wantMessage: `paths: "" has no entry, and an empty list would check out the whole tree`},
		{name: "only a slash", value: "/",
			wantMessage: `paths: "/" starts with a slash, and an entry is relative to the root of the repository`},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := parsePaths(c.value)
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("parsePaths(%q) returned the code %v, want %v", c.value, got, codes.InvalidArgument)
			}
			if got := status.Convert(err).Message(); got != c.wantMessage {
				t.Errorf("parsePaths(%q) message is %q, want %q", c.value, got, c.wantMessage)
			}
		})
	}
}

func TestAVolumeWithoutPathsChecksOutTheWholeTree(t *testing.T) {
	parsed, err := parseVolumeContext(map[string]string{"url": "https://example.com/data.git"})
	if err != nil {
		t.Fatalf("parsing the volume context: %v", err)
	}
	if parsed.paths != nil {
		t.Errorf("a volume with no paths attribute has paths %q, want nil", parsed.paths)
	}
}

func TestEveryKindOfVolumeAcceptsPaths(t *testing.T) {
	for _, c := range []struct {
		name string
		kind volumeKind
	}{
		{name: "a writeable volume", kind: writeableVolume},
		{name: "a read-only claim", kind: readOnlyClaim},
	} {
		t.Run(c.name, func(t *testing.T) {
			parsed, err := parseStageAttributes(c.kind, map[string]string{
				"url": "https://example.com/data.git", "paths": "b,a",
			})
			if err != nil {
				t.Fatalf("parsing the attributes: %v", err)
			}
			if want := []string{"a", "b"}; !slices.Equal(parsed.paths, want) {
				t.Errorf("paths is %q, want %q", parsed.paths, want)
			}
		})
	}
}

func TestAVolumeWithAnInvalidEntryIsRefused(t *testing.T) {
	_, err := parseVolumeContext(map[string]string{
		"url": "https://example.com/data.git", "paths": "a,../x",
	})
	want := `paths: "../x" has an empty, . or .. component`
	if status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != want {
		t.Errorf("parsing the volume context returned %v, want InvalidArgument with %q", err, want)
	}
}
