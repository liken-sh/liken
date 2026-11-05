package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// A device that lacks an attribute fails a selector that reads it with
// a CEL error, and the scheduler then fails every claim of the class on
// that node, not only the claim of that device. So each read of an
// attribute or a capacity needs a has() of it earlier in the selector.
func TestASelectorReadsAnAttributeOnlyAfterItsHas(t *testing.T) {
	cases := []struct {
		expression string
		want       []string
	}{
		{`device.driver == "liken.sh" && device.attributes["liken.sh"].driver == "btusb"`,
			[]string{`device.attributes["liken.sh"].driver`}},
		{`device.driver == "liken.sh" && has(device.attributes["liken.sh"].driver) && device.attributes["liken.sh"].driver == "btusb"`,
			nil},
		{`has(device.attributes["sound.liken.sh"].supportsSound) && device.attributes["sound.liken.sh"].supportsSound`,
			nil},
		{`device.driver == "liken.sh" && has(device.attributes["liken.sh"].subsystem) && device.attributes["liken.sh"].subsystem == "i2c-dev" && device.attributes["liken.sh"].class == "display"`,
			[]string{`device.attributes["liken.sh"].class`}},
		{`device.attributes["liken.sh"].bus == "usb" && has(device.attributes["liken.sh"].bus)`,
			[]string{`device.attributes["liken.sh"].bus`}},
		{`device.attributes["liken.sh"].?bus.orValue("") == "usb"`, nil},
		{`device.capacity["liken.sh"].memory.compareTo(quantity("1Gi")) >= 0`,
			[]string{`device.capacity["liken.sh"].memory`}},
		{`device.driver == "display.liken.sh"`, nil},
	}
	for _, c := range cases {
		t.Run(c.expression, func(t *testing.T) {
			if got := unguardedReads(c.expression); !reflect.DeepEqual(got, c.want) {
				t.Errorf("unguarded %q, want %q", got, c.want)
			}
		})
	}
}

// The check reads every DeviceClass in the repository's YAML files and
// in the YAML blocks of its pages, because a reader copies a class from
// a guide as often as from a deploy base. A plan is a record and is
// left as it was written.
func TestEveryDeviceClassInTheTreeIsChecked(t *testing.T) {
	class := "apiVersion: resource.k8s.io/v1\nkind: DeviceClass\nmetadata:\n  name: %s\nspec:\n  selectors:\n    - cel:\n        expression: device.attributes[\"liken.sh\"].bus == \"usb\"\n"
	root := writeTree(t, map[string]string{
		"op/deploy/classes.yaml":       fmt.Sprintf(class, "upload") + "---\nkind: ConfigMap\nmetadata:\n  name: x\n",
		"op/docs/guide.md":             "# A guide\n\n```yaml\n" + fmt.Sprintf(class, "guide") + "```\n\n```sh\nkubectl get deviceclasses\n```\n",
		"op/plans/01-old.md":           "```yaml\n" + fmt.Sprintf(class, "plan") + "```\n",
		"op/deploy/claim.yaml":         "kind: ResourceClaimTemplate\nspec:\n  spec:\n    devices:\n      requests:\n        - exactly:\n            selectors:\n              - cel:\n                  expression: device.attributes[\"liken.sh\"].vendor == \"0403\"\n",
		"op/testdata/broken.yaml":      fmt.Sprintf(class, "fixture"),
		"op/deploy/kustomization.yaml": "resources: [classes.yaml]\n",
	})
	findings, err := checkSelectors(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`op/deploy/classes.yaml: the DeviceClass upload reads device.attributes["liken.sh"].bus with no has() of it before`,
		`op/docs/guide.md: the DeviceClass guide reads device.attributes["liken.sh"].bus with no has() of it before`,
	}
	if !reflect.DeepEqual(findings, want) {
		t.Errorf("findings:\n%q\nwant:\n%q", findings, want)
	}
}

// The command fails on a finding and names it, and passes on a tree
// whose classes guard every read.
func TestTheSelectorsCommandFailsOnAnUnguardedRead(t *testing.T) {
	cases := []struct {
		name, expression string
		fails            bool
	}{
		{"unguarded", `device.attributes["liken.sh"].bus == "usb"`, true},
		{"guarded", `has(device.attributes["liken.sh"].bus) && device.attributes["liken.sh"].bus == "usb"`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := writeTree(t, map[string]string{
				"op/deploy/classes.yaml": "kind: DeviceClass\nmetadata:\n  name: upload\nspec:\n  selectors:\n    - cel:\n        expression: '" + c.expression + "'\n",
				"op/docs/notes.md":       "```yaml\nkey: [unclosed\n```\n",
			})
			w := newWorld(t, &fakeRegistry{})
			err := w.run([]string{"selectors", "-root", root})
			if (err != nil) != c.fails || c.fails != strings.Contains(w.out.String(), "op/deploy/classes.yaml: the DeviceClass upload") {
				t.Errorf("err %v, out %q", err, w.out.String())
			}
		})
	}
}
