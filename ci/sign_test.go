package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// signCommand is the cosign run that signs one reference.
func signCommand(reference string) string {
	return fmt.Sprintf("docker run --rm --user %d:%d -e HOME=/tmp"+
		" -e ACTIONS_ID_TOKEN_REQUEST_URL -e ACTIONS_ID_TOKEN_REQUEST_TOKEN"+
		" -e DOCKER_CONFIG=/docker -v %s:/docker:ro %s sign --yes %s",
		os.Getuid(), os.Getgid(), fixtureDockerConfig, fixtureCosign, reference)
}

// signCommands lists the cosign runs among the recorded commands.
func signCommands(commands []string) []string {
	var signs []string
	for _, command := range commands {
		if strings.Contains(command, " "+fixtureCosign+" sign ") {
			signs = append(signs, command)
		}
	}
	return signs
}

// The publish signs each image it pushes by the digest that the push
// wrote, once for every name the image is pushed under. A dry run signs
// nothing. When the version is already published, the publish signs the
// image by its tag, so that rerunning after a failed signature signs
// the image.
func TestThePublishSignsEachImageThatItPushes(t *testing.T) {
	byDigest := func(name, target string) string { return "ghcr.io/liken-sh/" + name + "@" + fakeDigest(target) }
	cases := []struct {
		name, version, mode string
		tags                []string
		signed              []string
	}{
		{"a release", "2026.10.02-001", publishRelease, nil, []string{
			byDigest("operator", "operator"), byDigest("operator-sidecar", "operator"), byDigest("operator-cli", "operator-cli"),
		}},
		{"a development build", "2026.10.02-001-dev-017-abcdef01", publishDev, nil, []string{
			byDigest("operator", "operator"), byDigest("operator-sidecar", "operator"), byDigest("operator-cli", "operator-cli"),
		}},
		{"a dry run", "2026.10.02-001", publishDry, nil, nil},
		{"a version that is already published", "2026.10.02-001", publishRelease, []string{"2026.10.02-001"}, []string{
			"ghcr.io/liken-sh/operator:2026.10.02-001", "ghcr.io/liken-sh/operator-sidecar:2026.10.02-001", byDigest("operator-cli", "operator-cli"),
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, rec, component := publishFixture(t, map[string][]string{"operator": c.tags})
			if err := p.Publish(component, c.version, c.mode); err != nil {
				t.Fatalf("publishing the component: %v", err)
			}
			var want []string
			for _, reference := range c.signed {
				want = append(want, signCommand(reference))
			}
			if got := signCommands(rec.commands); !reflect.DeepEqual(got, want) {
				t.Errorf("the publish ran these signatures:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// Every build of a publish, including a dry run, attaches an SBOM and
// max-mode provenance to its target, and writes the metadata file that
// the signing step reads the digest from.
func TestEveryPublishBuildAttestsItsTarget(t *testing.T) {
	for _, mode := range []string{publishRelease, publishDry} {
		p, rec, c := publishFixture(t, nil)
		if err := p.Publish(c, "2026.10.02-001", mode); err != nil {
			t.Fatalf("%s: publishing the component: %v", mode, err)
		}
		builds := 0
		for _, command := range rec.commands {
			if !strings.HasPrefix(command, "docker buildx bake ") {
				continue
			}
			builds++
			target := command[strings.LastIndex(command, " ")+1:]
			want := " --set " + target + ".attest=type=sbom,generator=" + fixtureScanner + " --set " + target + ".attest=type=provenance,mode=max --metadata-file <metadata> " + target
			if !strings.HasSuffix(command, want) {
				t.Errorf("%s: the build of %s is\n%s\nwant it to end in%s", mode, target, command, want)
			}
		}
		if builds != 2 {
			t.Errorf("%s: the publish ran %d builds, want 2:\n%s", mode, builds, strings.Join(rec.commands, "\n"))
		}
	}
}

// failingCosign records each command and fails each signature.
type failingCosign struct{ recorder }

func (f *failingCosign) run(dir, name string, args ...string) error {
	if err := f.recorder.run(dir, name, args...); err != nil {
		return err
	}
	if strings.Contains(strings.Join(args, " "), " sign --yes ") {
		return errors.New("the OIDC token request returned an error")
	}
	return nil
}

// A cluster follows the deploy artifact, so when an image's signature
// fails, the publish stops before it writes a deploy artifact that
// refers to the image.
func TestASignatureThatFailsStopsThePublish(t *testing.T) {
	p, _, c := publishFixture(t, nil)
	runner := &failingCosign{}
	p.Run = runner.run
	if err := p.Publish(c, "2026.10.02-001", publishRelease); err == nil {
		t.Fatal("Publish returned no error, want one because the signature failed")
	}
	for _, command := range runner.commands {
		if strings.HasPrefix(command, "flux ") || strings.Contains(command, "imagetools create") {
			t.Errorf("the publish ran %q after the signature failed, want it to stop first", command)
		}
	}
}

// The publish runs the cosign and SBOM generator images that
// toolchains/images.toml pins, so a tree that lacks either entry
// cannot publish.
func TestAPublishWithoutItsPinnedToolsPushesNothing(t *testing.T) {
	for name, images := range map[string]string{
		"no cosign":         "[build]\nbuildkit-syft-scanner = \"" + fixtureScanner + "\"\n",
		"no SBOM generator": "[build]\ncosign = \"" + fixtureCosign + "\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			p, rec, c := publishFixture(t, nil)
			if err := os.WriteFile(p.Root+"/toolchains/images.toml", []byte(images), 0o644); err != nil {
				t.Fatalf("writing toolchains/images.toml: %v", err)
			}
			if err := p.Publish(c, "2026.10.02-001", publishRelease); err == nil {
				t.Fatal("Publish returned no error, want one because the tools are not pinned")
			}
			for _, command := range rec.commands {
				if strings.Contains(command, "--push") {
					t.Errorf("the publish pushed with %q, want no push without the pinned tools", command)
				}
			}
		})
	}
}
