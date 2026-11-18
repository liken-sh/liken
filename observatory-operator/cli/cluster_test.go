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
		t.Fatalf("reading the operator version: %v", err)
	}
	if got != "2026.09.03-007" {
		t.Fatalf("operatorVersion returns %q, want 2026.09.03-007", got)
	}
}

func TestOperatorVersionWithoutADeployment(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	if _, err := operatorVersion(context.Background(), clientset); err == nil {
		t.Fatal("operatorVersion returns no error when no Deployment has the label, want an error")
	}
}

func TestOperatorVersionWithNoContainer(t *testing.T) {
	deployment := labeledDeployment("")
	deployment.Spec.Template.Spec.Containers = nil
	clientset := fake.NewSimpleClientset(deployment)
	if _, err := operatorVersion(context.Background(), clientset); err == nil {
		t.Fatal("operatorVersion returns no error when the Deployment has no container, want an error")
	}
}

// --by takes precedence, and a review that returns no user falls back
// to the kubeconfig's user. The fake clientset returns the review it
// was sent, which has no user, so it behaves like a cluster that
// refuses the review.
func TestPersonFallsBackToTheKubeconfigsUser(t *testing.T) {
	c := clients{typed: fake.NewSimpleClientset(), kubeUser: "admin"}
	if got := person(context.Background(), c, "Ada"); got != "Ada" {
		t.Errorf("person with --by Ada returns %q, want Ada", got)
	}
	if got := person(context.Background(), c, ""); got != "admin" {
		t.Errorf("person with no review and no --by returns %q, want admin", got)
	}
}
