package firmware

// deploy/firmware/qhy.yaml comes from 85-qhyccd.rules and the image
// that holds indi_qhy_ccd. TestTheQHYFirmwareIsCurrent writes the file
// again from those sources and fails when the result differs, so a
// bump of the INDI images cannot leave the objects on the old image.
// `make firmware` runs it with -update, which writes the file.

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/observatory-operator/drivers"
)

var update = flag.Bool("update", false, "write deploy/firmware/qhy.yaml from 85-qhyccd.rules and drivers/generated.go")

var generatedFile = filepath.Join("..", "deploy", "firmware", "qhy.yaml")

func TestTheQHYFirmwareIsCurrent(t *testing.T) {
	want, err := QHY("85-qhyccd.rules")
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile(generatedFile, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(generatedFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("%s is stale against 85-qhyccd.rules and drivers/generated.go; run make firmware", generatedFile)
	}
}

// firmwareObject reads the fields of a generated Firmware that the
// tests check.
type firmwareObject struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Selector string `json:"selector"`
		Upload   struct {
			Image   string   `json:"image"`
			Command []string `json:"command"`
		} `json:"upload"`
	} `json:"spec"`
}

// committedObjects reads every Firmware in deploy/firmware/qhy.yaml.
func committedObjects(t *testing.T) []firmwareObject {
	t.Helper()
	raw, err := os.ReadFile(generatedFile)
	if err != nil {
		t.Fatal(err)
	}
	var objects []firmwareObject
	for _, document := range bytes.Split(raw, []byte("\n---\n")) {
		var object firmwareObject
		if err := yaml.Unmarshal(document, &object); err != nil {
			t.Fatal(err)
		}
		objects = append(objects, object)
	}
	return objects
}

// The objects and the drivers name one release of indi-qhy, so the
// pod that loads a camera's firmware and the pod that drives the
// camera run the same files.
func TestEveryFirmwareRunsTheDriversTagOfIndiQHY(t *testing.T) {
	want := drivers.Registry + "/indi-qhy:" + drivers.Tag
	for _, object := range committedObjects(t) {
		if object.Spec.Upload.Image != want {
			t.Errorf("%s runs %s, want %s", object.Metadata.Name, object.Spec.Upload.Image, want)
		}
	}
}

// The file holds one Firmware for each chip that the image's fxload
// can load, and each one names the device node that firmware-operator
// sets.
func TestTheCommittedObjectsAreWellFormed(t *testing.T) {
	objects := committedObjects(t)
	if len(objects) != 47 {
		t.Errorf("deploy/firmware/qhy.yaml holds %d objects, want 47", len(objects))
	}
	seen := map[string]bool{}
	for _, object := range objects {
		if object.APIVersion != "firmware.liken.sh/v1alpha1" || object.Kind != "Firmware" {
			t.Errorf("%s is %s %s", object.Metadata.Name, object.APIVersion, object.Kind)
		}
		if seen[object.Metadata.Name] {
			t.Errorf("%s is in the file twice", object.Metadata.Name)
		}
		seen[object.Metadata.Name] = true
		command := strings.Join(object.Spec.Upload.Command, " ")
		if !strings.HasPrefix(command, "fxload -t ") || !strings.Contains(command, " -D $(LIKEN_DEVICE)") {
			t.Errorf("%s runs %q", object.Metadata.Name, command)
		}
	}
}

// The rules name two IDs that a chip vendor gives every unprogrammed
// chip of a family, and the objects leave both out, so that no other
// device with a blank chip gets a QHY5's firmware.
func TestNoObjectMatchesAGenericChip(t *testing.T) {
	for _, object := range committedObjects(t) {
		switch object.Metadata.Name {
		case "qhy-04b4-8613", "qhy-0547-1002":
			t.Errorf("%s matches a generic chip", object.Metadata.Name)
		}
	}
}

// One bare chip of the lab, the QHY5L-II-C, gives the exact object.
func TestTheQHY5LIIObjectMatchesTheLab(t *testing.T) {
	for _, object := range committedObjects(t) {
		if object.Metadata.Name != "qhy-1618-0920" {
			continue
		}
		wantSelector := "device.attributes[\"liken.sh\"].vendor == \"1618\" &&\ndevice.attributes[\"liken.sh\"].product == \"0920\"\n"
		if object.Spec.Selector != wantSelector {
			t.Errorf("selector = %q, want %q", object.Spec.Selector, wantSelector)
		}
		wantCommand := "fxload -t fx2 -I /usr/lib/firmware/qhy/QHY5II.HEX -D $(LIKEN_DEVICE)"
		if got := strings.Join(object.Spec.Upload.Command, " "); got != wantCommand {
			t.Errorf("command = %q, want %q", got, wantCommand)
		}
		return
	}
	t.Error("deploy/firmware/qhy.yaml holds no qhy-1618-0920")
}

// writeRules writes one rules file for a test and answers its path.
func writeRules(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "85-qhyccd.rules")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestARuleOfAnotherFormIsRefused(t *testing.T) {
	cases := map[string]string{
		"a match on another attribute": `ATTRS{serial}=="1", ATTRS{idProduct}=="0920", RUN+="/sbin/fxload -t fx2 -I /usr/lib/firmware/qhy/A.HEX -D $env{DEVNAME}"`,
		"a vendor that is not hex":     `ATTRS{idVendor}=="16zz", ATTRS{idProduct}=="0920", RUN+="/sbin/fxload -t fx2 -I /usr/lib/firmware/qhy/A.HEX -D $env{DEVNAME}"`,
		"an unknown flag":              `ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0920", RUN+="/sbin/fxload -t fx2 -c 1 -I /usr/lib/firmware/qhy/A.HEX -D $env{DEVNAME}"`,
		"no device":                    `ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0920", RUN+="/sbin/fxload -t fx2 -I /usr/lib/firmware/qhy/A.HEX"`,
		"another device":               `ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0920", RUN+="/sbin/fxload -t fx2 -I /usr/lib/firmware/qhy/A.HEX -D /dev/x"`,
		"no image":                     `ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0920", RUN+="/sbin/fxload -t fx2 -D $env{DEVNAME}"`,
		"no chip":                      `ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0920", RUN+="/sbin/fxload -I /usr/lib/firmware/qhy/A.HEX -D $env{DEVNAME}"`,
		"an unknown chip":              `ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0920", RUN+="/sbin/fxload -t fx9 -I /usr/lib/firmware/qhy/A.HEX -D $env{DEVNAME}"`,
		"a file outside the image":     `ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0920", RUN+="/sbin/fxload -t fx2 -I /tmp/A.HEX -D $env{DEVNAME}"`,
		"a flag with no value":         `ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0920", RUN+="/sbin/fxload -t fx2 -I /usr/lib/firmware/qhy/A.HEX -D"`,
		"another program":              `ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0920", RUN+="/usr/bin/qhyload -D $env{DEVNAME}"`,
		"two loads that differ": `ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0920", RUN+="/sbin/fxload -t fx2 -I /usr/lib/firmware/qhy/A.HEX -D $env{DEVNAME}"
ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0920", RUN+="/sbin/fxload -t fx2lp -I /usr/lib/firmware/qhy/B.HEX -D $env{DEVNAME}"`,
		"three loads": `ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0920", RUN+="/sbin/fxload -t fx2 -I /usr/lib/firmware/qhy/A.HEX -D $env{DEVNAME}"
ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0920", RUN+="/sbin/fxload -t fx2 -I /usr/lib/firmware/qhy/B.HEX -D $env{DEVNAME}"
ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0920", RUN+="/sbin/fxload -t fx2 -I /usr/lib/firmware/qhy/C.HEX -D $env{DEVNAME}"`,
	}
	for name, rules := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := QHY(writeRules(t, rules)); err == nil {
				t.Errorf("QHY accepted %q", rules)
			}
		})
	}
}

func TestAMissingRulesFileIsRefused(t *testing.T) {
	if _, err := QHY(filepath.Join(t.TempDir(), "absent.rules")); err == nil {
		t.Error("QHY accepted a rules file that does not exist")
	}
}

// generated reads the commands of the objects that QHY writes for one
// rules file, by object name.
func generated(t *testing.T, rules string) map[string]string {
	t.Helper()
	out, err := QHY(writeRules(t, rules))
	if err != nil {
		t.Fatal(err)
	}
	commands := map[string]string{}
	for _, document := range bytes.Split(out, []byte("\n---\n")) {
		var object firmwareObject
		if err := yaml.Unmarshal(document, &object); err != nil {
			t.Fatal(err)
		}
		if object.Metadata.Name != "" {
			commands[object.Metadata.Name] = strings.Join(object.Spec.Upload.Command, " ")
		}
	}
	return commands
}

func TestRulesBecomeCommands(t *testing.T) {
	cases := map[string]struct {
		rules string
		want  map[string]string
	}{
		"a second stage": {
			`ATTRS{idVendor}=="16C0", ATTRS{idProduct}=="081A", RUN+="/sbin/fxload -t fx2 -I /usr/lib/firmware/qhy/QHY5.HEX -D $env{DEVNAME} -s /usr/lib/firmware/qhy/QHY5LOADER.HEX"`,
			map[string]string{"qhy-16c0-081a": "fxload -t fx2 -I /usr/lib/firmware/qhy/QHY5.HEX -D $(LIKEN_DEVICE) -s /usr/lib/firmware/qhy/QHY5LOADER.HEX"},
		},
		"a repeated rule": {
			`ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0482", RUN+="/sbin/fxload -t fx2lp -I /usr/lib/firmware/qhy/A.img -D $env{DEVNAME}"
ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0482", RUN+="/sbin/fxload -t fx2lp -I /usr/lib/firmware/qhy/A.img -D $env{DEVNAME}"`,
			map[string]string{"qhy-1618-0482": "fxload -t fx2lp -I /usr/lib/firmware/qhy/A.img -D $(LIKEN_DEVICE)"},
		},
		"a loader, a pause, and the image": {
			`ATTRS{idVendor}=="16c0", ATTRS{idProduct}=="296c", RUN+="/sbin/fxload -t fx2 -I /usr/lib/firmware/qhy/QHY5LOADER.HEX -D $env{DEVNAME}"
ATTRS{idVendor}=="16c0", ATTRS{idProduct}=="296c", RUN+="/bin/sleep 5"
ATTRS{idVendor}=="16c0", ATTRS{idProduct}=="296c", RUN+="/sbin/fxload -t fx2 -I /usr/lib/firmware/qhy/QHY5.HEX -D $env{DEVNAME}"`,
			map[string]string{"qhy-16c0-296c": "fxload -t fx2 -I /usr/lib/firmware/qhy/QHY5.HEX -D $(LIKEN_DEVICE) -s /usr/lib/firmware/qhy/QHY5LOADER.HEX"},
		},
		"a generic chip": {
			`ATTRS{idVendor}=="04b4", ATTRS{idProduct}=="8613", RUN+="/sbin/fxload -t fx2 -I /usr/lib/firmware/qhy/QHY5.HEX -D $env{DEVNAME}"`,
			map[string]string{},
		},
		"an FX3 chip, a comment, and the rules that set no load": {
			`# a comment
#ATTRS{idVendor}=="1618", ATTRS{idProduct}=="c411", RUN+="/sbin/fxload -t fx3 -I /usr/lib/firmware/qhy/QHY411.img -D $env{DEVNAME}"
ACTION!="add", GOTO="qhy_end"
ATTRS{idVendor}=="1618", ATTRS{idProduct}=="c184", RUN+="/sbin/fxload -t fx3 -I /usr/lib/firmware/qhy/QHY183.img -D $env{DEVNAME}"
ATTRS{idVendor}=="1618", MODE="0666"
ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0920", RUN+="/sbin/fxload -t fx2 -I /usr/lib/firmware/qhy/QHY5II.HEX -D $env{DEVNAME}"
LABEL="qhy_end"`,
			map[string]string{"qhy-1618-0920": "fxload -t fx2 -I /usr/lib/firmware/qhy/QHY5II.HEX -D $(LIKEN_DEVICE)"},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := generated(t, c.rules)
			if len(got) != len(c.want) {
				t.Errorf("objects = %v, want %v", got, c.want)
			}
			for object, command := range c.want {
				if got[object] != command {
					t.Errorf("%s runs %q, want %q", object, got[object], command)
				}
			}
		})
	}
}

// kustomization reads the resources that one kustomization.yaml lists.
func kustomization(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var k struct {
		Resources []string `json:"resources"`
	}
	if err := yaml.Unmarshal(raw, &k); err != nil {
		t.Fatal(err)
	}
	return k.Resources
}

// deploy/firmware/ lists the objects, and the base does not name the
// directory, because the base must apply on a cluster that has no
// Firmware CRD.
func TestOnlyTheFirmwareKustomizationListsTheObjects(t *testing.T) {
	if got := kustomization(t, filepath.Join("..", "deploy", "firmware", "kustomization.yaml")); len(got) != 1 || got[0] != "qhy.yaml" {
		t.Errorf("deploy/firmware/kustomization.yaml lists %v, want [qhy.yaml]", got)
	}
	for _, resource := range kustomization(t, filepath.Join("..", "deploy", "kustomization.yaml")) {
		if strings.Contains(resource, "firmware") {
			t.Errorf("deploy/kustomization.yaml lists %s", resource)
		}
	}
}
