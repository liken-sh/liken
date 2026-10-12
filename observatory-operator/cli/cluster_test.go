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
		{"ghcr.io/liken-sh/observatory-operator:2026.09.03-007", "2026.09.03-007"},
		{"ghcr.io/liken-sh/observatory-operator", ""},
		{"ghcr.io/liken-sh/observatory-operator@sha256:abcd", ""},
		{"localhost:5000/observatory-operator:dev", "dev"},
		{"localhost:5000/observatory-operator", ""},
	}
	for _, tc := range cases {
		t.Run(tc.image, func(t *testing.T) {
			if got := imageTag(tc.image); got != tc.tag {
				t.Fatalf("imageTag(%q) = %q, want %q", tc.image, got, tc.tag)
			}
		})
	}
}

func labeledDeployment(image string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "observatory-operator",
			Namespace: "liken-system",
			Labels:    map[string]string{pluginLabel: pluginDomain},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "operator", Image: image}},
				},
			},
		},
	}
}

func TestOperatorVersionReadsTheImageTag(t *testing.T) {
	clientset := fake.NewSimpleClientset(labeledDeployment("ghcr.io/liken-sh/observatory-operator:2026.09.03-007"))
	got, err := operatorVersion(context.Background(), clientset)
	if err != nil {
		t.Fatalf("operatorVersion: %v", err)
	}
	if got != "2026.09.03-007" {
		t.Fatalf("operatorVersion = %q, want 2026.09.03-007", got)
	}
}

func TestOperatorVersionWithoutADeployment(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	if _, err := operatorVersion(context.Background(), clientset); err == nil {
		t.Fatal("operatorVersion returned no error with no labeled Deployment")
	}
}

func TestOperatorVersionWithNoContainer(t *testing.T) {
	deployment := labeledDeployment("")
	deployment.Spec.Template.Spec.Containers = nil
	clientset := fake.NewSimpleClientset(deployment)
	if _, err := operatorVersion(context.Background(), clientset); err == nil {
		t.Fatal("operatorVersion returned no error with no container")
	}
}

// --by wins, and a review that names no user falls back to the
// kubeconfig's user. The fake clientset answers a review with the
// review it was sent, which names no user, as a cluster that refuses
// the review does in effect.
func TestPersonFallsBackToTheKubeconfigsUser(t *testing.T) {
	c := clients{typed: fake.NewSimpleClientset(), kubeUser: "admin"}
	if got := person(context.Background(), c, "Ada"); got != "Ada" {
		t.Errorf("person with --by = %q", got)
	}
	if got := person(context.Background(), c, ""); got != "admin" {
		t.Errorf("person with no review = %q", got)
	}
}
