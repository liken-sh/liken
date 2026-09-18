package main

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestImageTag(t *testing.T) {
	cases := []struct {
		image string
		tag   string
	}{
		{"ghcr.io/liken-sh/bluetooth-operator:2026.09.03-007", "2026.09.03-007"},
		{"ghcr.io/liken-sh/bluetooth-operator", ""},
		{"ghcr.io/liken-sh/bluetooth-operator@sha256:abcd", ""},
		{"localhost:5000/bluetooth-operator:dev", "dev"},
		{"localhost:5000/bluetooth-operator", ""},
	}
	for _, tc := range cases {
		t.Run(tc.image, func(t *testing.T) {
			if got := imageTag(tc.image); got != tc.tag {
				t.Fatalf("imageTag(%q) = %q, want %q", tc.image, got, tc.tag)
			}
		})
	}
}

func labeledDaemonSet(image string) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bluetooth-operator",
			Namespace: operatorNamespace,
			Labels:    map[string]string{pluginLabel: pluginDomain},
		},
		Spec: appsv1.DaemonSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "operator", Image: image}},
				},
			},
		},
	}
}

func TestOperatorVersionReadsTheImageTag(t *testing.T) {
	clientset := fake.NewSimpleClientset(labeledDaemonSet("ghcr.io/liken-sh/bluetooth-operator:2026.09.03-007"))
	got, err := operatorVersion(context.Background(), clientset)
	if err != nil {
		t.Fatalf("operatorVersion: %v", err)
	}
	if got != "2026.09.03-007" {
		t.Fatalf("operatorVersion = %q, want 2026.09.03-007", got)
	}
}

func TestOperatorVersionWithoutADaemonSet(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	if _, err := operatorVersion(context.Background(), clientset); err == nil {
		t.Fatal("operatorVersion returned no error with no labeled DaemonSet")
	}
}

func TestOperatorVersionWithNoContainer(t *testing.T) {
	set := labeledDaemonSet("")
	set.Spec.Template.Spec.Containers = nil
	clientset := fake.NewSimpleClientset(set)
	if _, err := operatorVersion(context.Background(), clientset); err == nil {
		t.Fatal("operatorVersion returned no error with no container")
	}
}
