package main

// These tests run FirmwareCatalogs and discovered Firmware objects through the
// CRDs' schemas, so a schema edit that refuses what a person or the
// operator writes, or prunes a field the operator writes, fails here.

import (
	"slices"
	"testing"
	"time"
)

func TestCatalogCRDIdentity(t *testing.T) {
	crd := loadCRD(t, catalogCRD)
	var columns []string
	for _, column := range crd.Spec.Versions[0].AdditionalPrinterColumns {
		columns = append(columns, column.Name)
	}
	cases := []struct {
		name string
		ok   bool
	}{
		{"cluster-scoped", crd.Spec.Scope == "Cluster"},
		{"the group", crd.Spec.Group == "firmware.liken.sh"},
		{"the kind", crd.Spec.Names.Kind == catalogKind},
		{"the plural", crd.Spec.Names.Plural == "firmwarecatalogs"},
		{"the status subresource", crd.Spec.Versions[0].Subresources.Status != nil},
		{"the printer columns", slices.Equal(columns, []string{"File", "Ready", "Checked", "Age"})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("%s is wrong", c.name)
			}
		})
	}
}

func TestCatalogCRDValidatesExamples(t *testing.T) {
	cases := []struct {
		name    string
		spec    map[string]any
		wantErr bool
	}{
		{"lvfs", map[string]any{"url": lvfsURL}, false},
		{"a mirror over HTTP", map[string]any{"url": "http://mirror.local/firmware.xml.zst"}, false},
		{"no URL", map[string]any{}, true},
		{"a path", map[string]any{"url": "/downloads/firmware.xml.zst"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			object := map[string]any{"apiVersion": firmwareAPIVersion, "kind": catalogKind, "metadata": map[string]any{"name": "lvfs"}, "spec": c.spec}
			if errs := validateObject(t, catalogCRD, object); (len(errs) > 0) != c.wantErr {
				t.Errorf("got errors %v, want an error: %v", errs, c.wantErr)
			}
		})
	}
}

// The status the operator writes to a FirmwareCatalog is one the schema takes
// and keeps whole.
func TestTheOperatorsCatalogStatusValidates(t *testing.T) {
	when := time.Date(2026, 10, 20, 15, 12, 4, 0, time.UTC)
	c := catalog{APIVersion: firmwareAPIVersion, Kind: catalogKind, Spec: catalogSpec{URL: lvfsURL}}
	c.Metadata.Name = "lvfs"
	c.Status = catalogStatus{
		File: "firmware-09138-stable.xml.zst", SHA256: "5ad15d60", ETag: `"1"`, Checked: stamp(when),
		Conditions: []condition{{Type: ready, Status: "True", Reason: reasonFetched, Message: "the jcat names it", ObservedGeneration: 1, LastTransitionTime: when}},
		Machines: []machineRecord{{Machine: "node-2", Boot: firstBoot, Devices: "3f9a", File: "firmware-09138-stable.xml.zst",
			Releases: "77c1", GUIDs: []string{biosGUID}, Result: resultScanned, Message: "1 device, 1 with updates", Time: stamp(when)}},
	}
	assertTheSchemaKeeps(t, catalogCRD, c)
}

// A discovered Firmware, as the operator creates it and writes its
// status, is one the schema takes and keeps whole.
func TestADiscoveredFirmwareValidates(t *testing.T) {
	f := firmware{APIVersion: firmwareAPIVersion, Kind: firmwareKind, Spec: firmwareSpec{Update: &update{Component: frameworkBIOS().id}}}
	f.Metadata.Name = biosObjectName
	f.Metadata.Labels = map[string]string{discoveredLabel: discoveredLabelValue}
	f.Status.Machines = []machineEntry{{Machine: "node-2", Device: "System Firmware", DeviceID: "3fcf", Version: "0.0.3.5", Catalog: "lvfs",
		File: "firmware-09138-stable.xml.zst", Releases: systemFirmware("0.0.3.5", "0.0.4.2").Releases, Time: stamp(time.Now())}}
	f.Status.Counts = counts{Machines: 1, Behind: 1}
	assertTheSchemaKeeps(t, firmwareCRD, f)
}
