package main

import (
	"context"
	"io"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestPeripheralName(t *testing.T) {
	cases := []struct {
		device string
		name   string
	}{
		{"A0:AB:51:33:B7:12", "a0-ab-51-33-b7-12"},
		{"a0-ab-51-33-b7-12", "a0-ab-51-33-b7-12"},
		{" A0:AB:51:33:B7:12 ", "a0-ab-51-33-b7-12"},
	}
	for _, tc := range cases {
		t.Run(tc.device, func(t *testing.T) {
			if got := peripheralName(tc.device); got != tc.name {
				t.Fatalf("peripheralName(%q) = %q, want %q", tc.device, got, tc.name)
			}
		})
	}
}

func peripheralObject(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": driverGroup + "/" + driverVersion,
		"kind":       "Peripheral",
		"metadata":   map[string]any{"name": name},
	}}
}

func TestDeletePeripheralRemovesTheObject(t *testing.T) {
	client := fakeDynamic(peripheralObject("a0-ab-51-33-b7-12"))
	if err := deletePeripheral(context.Background(), client, "a0-ab-51-33-b7-12"); err != nil {
		t.Fatalf("deletePeripheral: %v", err)
	}
	if _, err := client.Resource(peripheralGVR).Get(context.Background(), "a0-ab-51-33-b7-12", metav1.GetOptions{}); err == nil {
		t.Fatal("the Peripheral is still present after the delete")
	}
}

func TestDeletePeripheralReportsAMissingOne(t *testing.T) {
	client := fakeDynamic()
	if err := deletePeripheral(context.Background(), client, "a0-ab-51-33-b7-12"); err == nil {
		t.Fatal("deletePeripheral returned no error for a device that is not paired")
	}
}

func TestRunUnpairNeedsADevice(t *testing.T) {
	if err := runUnpair(context.Background(), nil, unpairOptions{}, io.Discard); err == nil {
		t.Fatal("runUnpair returned no error with no device")
	}
}
