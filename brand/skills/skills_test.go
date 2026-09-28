package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// base is the fixture site's base URL. Every expected URL in this
// file is written against it.
const base = "https://media.liken.sh"

// The golden file is the contract: one guide that holds every link
// shape, and the exact skill it must produce.
func TestGenerateMatchesGolden(t *testing.T) {
	got, err := Generate(read(t, "testdata/guides/pair-a-remote.md"), "pair-a-remote", base)
	if err != nil {
		t.Fatal(err)
	}
	want := read(t, "testdata/pair-a-remote-skill.md")
	if string(got) != string(want) {
		t.Errorf("generated skill does not match testdata/pair-a-remote-skill.md:\n%s", string(got))
	}
}

// read returns one fixture file, and fails the test if it is
// missing.
func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestGenerateRefusesAGuideWithNoDescription(t *testing.T) {
	_, err := Generate(read(t, "testdata/broken/no-description.md"), "no-description", base)
	if err == nil {
		t.Fatal("a guide with no description must be refused")
	}
	if !strings.Contains(err.Error(), "no-description.md has no description") {
		t.Errorf("error does not name the guide: %v", err)
	}
}

func TestGenerateRefusesAGuideWithNoFrontMatter(t *testing.T) {
	_, err := Generate([]byte("# Pair a remote\n"), "pair-a-remote", base)
	if err == nil {
		t.Fatal("a guide with no front matter must be refused")
	}
}

func TestGenerateRefusesUnterminatedFrontMatter(t *testing.T) {
	_, err := Generate([]byte("---\ntitle: Pair a remote\ndescription: Pair a remote.\n"), "pair-a-remote", base)
	if err == nil {
		t.Fatal("front matter with no closing line must be refused")
	}
}

func TestGenerateRefusesBrokenFrontMatter(t *testing.T) {
	_, err := Generate([]byte("---\ndescription: [\n---\n\n# Pair\n"), "pair-a-remote", base)
	if err == nil {
		t.Fatal("front matter that is not YAML must be refused")
	}
}

func TestGenerateRefusesANameThatIsNotASkillName(t *testing.T) {
	guide := read(t, "testdata/guides/pair-a-remote.md")
	for _, tc := range []struct {
		name string
		slug string
	}{
		{"empty", ""},
		{"uppercase", "Pair-A-Remote"},
		{"space", "pair a remote"},
		{"underscore", "pair_a_remote"},
		{"two hyphens", "pair--remote"},
		{"leading hyphen", "-pair-remote"},
		{"trailing hyphen", "pair-remote-"},
		{"dot", "pair.remote"},
		{"too long", strings.Repeat("a", 65)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Generate(guide, tc.slug, base)
			if err == nil {
				t.Fatalf("the name %q must be refused", tc.slug)
			}
			if !strings.Contains(err.Error(), "is not a skill name") {
				t.Errorf("error does not give the rule: %v", err)
			}
		})
	}
}

func TestGenerateRefusesABaseThatIsNotAURL(t *testing.T) {
	_, err := Generate(read(t, "testdata/guides/pair-a-remote.md"), "pair-a-remote", "://media.liken.sh")
	if err == nil {
		t.Fatal("a base that is not a URL must be refused")
	}
}

func TestGenerateRewritesLinks(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			"absolute",
			"Read [the reference](/docs/reference/remote/).",
			"Read [the reference](https://media.liken.sh/docs/reference/remote/).",
		},
		{
			"absolute with a fragment",
			"Read [the field](/docs/reference/remote/#spec).",
			"Read [the field](https://media.liken.sh/docs/reference/remote/#spec).",
		},
		{
			"relative to the section",
			"Read [the adapter guide](../claim/).",
			"Read [the adapter guide](https://media.liken.sh/docs/guides/claim/).",
		},
		{
			"relative to the guide",
			"Read [the key map](keys/).",
			"Read [the key map](https://media.liken.sh/docs/guides/pair-a-remote/keys/).",
		},
		{
			"relative with a fragment",
			"Read [the keys](../claim/#keys).",
			"Read [the keys](https://media.liken.sh/docs/guides/claim/#keys).",
		},
		{
			"fragment only",
			"Read [the keys](#keys).",
			"Read [the keys](#keys).",
		},
		{
			"external",
			"Read [the notes](https://example.com/remotes/).",
			"Read [the notes](https://example.com/remotes/).",
		},
		{
			"mailto",
			"Mail [the list](mailto:remotes@example.com).",
			"Mail [the list](mailto:remotes@example.com).",
		},
		{
			"host relative",
			"Read [the notes](//example.com/remotes/).",
			"Read [the notes](//example.com/remotes/).",
		},
		{
			"empty target",
			"Read [the notes]().",
			"Read [the notes]().",
		},
		{
			"target that is not a URL",
			"Read [the notes](%zz).",
			"Read [the notes](%zz).",
		},
		{
			"image with a title",
			`![the mark](/brand/liken.svg "A patch of crustose lichen.")`,
			`![the mark](https://media.liken.sh/brand/liken.svg "A patch of crustose lichen.")`,
		},
		{
			"fenced code",
			"```sh\n# [the reference](/docs/reference/remote/)\n```",
			"```sh\n# [the reference](/docs/reference/remote/)\n```",
		},
		{
			"indented code",
			"    kubectl get [remotes](/docs/reference/remote/)",
			"    kubectl get [remotes](https://media.liken.sh/docs/reference/remote/)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Generate(guideWith(tc.body), "pair-a-remote", base)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), tc.want) {
				t.Errorf("skill is missing %q:\n%s", tc.want, string(got))
			}
		})
	}
}

// guideWith wraps one body in the front matter every guide has, so
// each case above is one line of Markdown.
func guideWith(body string) []byte {
	return []byte("---\ntitle: Pair a remote\n" +
		"description: Pair a remote with a machine.\nweight: 20\n---\n\n" + body + "\n")
}

func TestGenerateTrimsATrailingSlashFromTheBase(t *testing.T) {
	got, err := Generate(guideWith("Read [the reference](/docs/reference/remote/)."), "pair-a-remote", base+"/")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "(https://media.liken.sh/docs/reference/remote/)") {
		t.Errorf("the base's trailing slash survived:\n%s", string(got))
	}
}

func TestGenerateQuotesTheDescription(t *testing.T) {
	guide := []byte("---\ndescription: 'Pair a remote: the \"living room\" one.'\n---\n\n# Pair\n")
	got, err := Generate(guide, "pair-a-remote", base)
	if err != nil {
		t.Fatal(err)
	}
	want := "description: \"Pair a remote: the \\\"living room\\\" one.\"\n"
	if !strings.Contains(string(got), want) {
		t.Errorf("skill is missing %q:\n%s", want, string(got))
	}
}

func TestEmitWritesOneSkillPerGuide(t *testing.T) {
	out := t.TempDir()
	if err := Emit("testdata/guides", out, base); err != nil {
		t.Fatal(err)
	}
	got := read(t, filepath.Join(out, "pair-a-remote", "SKILL.md"))
	want := read(t, "testdata/pair-a-remote-skill.md")
	if string(got) != string(want) {
		t.Errorf("written skill does not match testdata/pair-a-remote-skill.md:\n%s", string(got))
	}
}

// The fixture directory holds _index.md, the section page; notes.txt,
// which is not Markdown; and sub/, a directory. Emit skips all three
// and reports no error.
func TestEmitSkipsWhatIsNotAGuide(t *testing.T) {
	out := t.TempDir()
	if err := Emit("testdata/guides", out, base); err != nil {
		t.Fatal(err)
	}
	got := names(t, out)
	want := []string{"pair-a-remote", "roll-back"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the output directory holds %v, want %v", got, want)
	}
}

// names lists one directory's entries in the sorted order ReadDir
// returns them.
func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, entry := range entries {
		found = append(found, entry.Name())
	}
	return found
}

// A renamed or deleted guide leaves a stale skill directory, and the
// run removes it. A file at the top of the output directory stays,
// because it is the repository's.
func TestEmitSweepsAStaleSkillAndKeepsFiles(t *testing.T) {
	out := t.TempDir()
	stale(t, out)
	if err := Emit("testdata/guides", out, base); err != nil {
		t.Fatal(err)
	}
	got := names(t, out)
	want := []string{"README.md", "pair-a-remote", "roll-back"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the output directory holds %v, want %v", got, want)
	}
}

// stale puts the skill of a guide that no longer exists into an
// output directory, with a file beside it.
func stale(t *testing.T, out string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(out, "renamed-guide"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "renamed-guide", "SKILL.md"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "README.md"), []byte("kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEmitRefusesAGuidesDirectoryThatIsNotThere(t *testing.T) {
	if err := Emit("testdata/nowhere", t.TempDir(), base); err == nil {
		t.Error("a guides directory that is not there must be refused")
	}
}

func TestEmitRefusesAGuideItCannotRender(t *testing.T) {
	if err := Emit("testdata/broken", t.TempDir(), base); err == nil {
		t.Error("a guide with no description must fail the run")
	}
}

func TestEmitRefusesAnOutputDirectoryItCannotCreate(t *testing.T) {
	out := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(out, []byte("a file, not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Emit("testdata/guides", filepath.Join(out, "skills"), base); err == nil {
		t.Error("an output directory under a file must be refused")
	}
}

func TestRunEmitsTheGuides(t *testing.T) {
	out := t.TempDir()
	if err := run([]string{"-base", base, "testdata/guides", out}); err != nil {
		t.Fatal(err)
	}
	got := read(t, filepath.Join(out, "pair-a-remote", "SKILL.md"))
	want := read(t, "testdata/pair-a-remote-skill.md")
	if string(got) != string(want) {
		t.Errorf("written skill does not match testdata/pair-a-remote-skill.md:\n%s", string(got))
	}
}

func TestRunRefusesTheWrongArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"nothing", []string{}},
		{"no base", []string{"testdata/guides", "skills"}},
		{"empty base", []string{"-base", "", "testdata/guides", "skills"}},
		{"one directory", []string{"-base", base, "testdata/guides"}},
		{"three directories", []string{"-base", base, "testdata/guides", "skills", "more"}},
		{"unknown flag", []string{"-nope", "testdata/guides", "skills"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := run(tc.args); err == nil {
				t.Errorf("run(%q) must be refused", tc.args)
			}
		})
	}
}

// -h prints the usage text, and that is not a failure.
func TestRunPrintsTheUsage(t *testing.T) {
	if err := run([]string{"-h"}); err != nil {
		t.Errorf("-h is not a failure: %v", err)
	}
}
