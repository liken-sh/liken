package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestImageTag(t *testing.T) {
	cases := []struct {
		image string
		tag   string
	}{
		{"ghcr.io/liken-sh/display-api:2026.09.03-007", "2026.09.03-007"},
		{"ghcr.io/liken-sh/display-api", ""},
		{"ghcr.io/liken-sh/display-api@sha256:abcd", ""},
		{"localhost:5000/display-api:dev", "dev"},
		{"localhost:5000/display-api", ""},
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
			Name:      "display-operator",
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
	clientset := fake.NewSimpleClientset(labeledDaemonSet("ghcr.io/liken-sh/display-operator:2026.09.03-007"))
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

func caPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating a certificate: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func caConfigMap(data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: trustConfigMap, Namespace: operatorNamespace},
		Data:       data,
	}
}

func TestTrustAnchorReadsTheCA(t *testing.T) {
	clientset := fake.NewSimpleClientset(caConfigMap(map[string]string{trustKey: caPEM(t)}))
	pool, err := trustAnchor(context.Background(), clientset)
	if err != nil {
		t.Fatalf("trustAnchor: %v", err)
	}
	if pool == nil {
		t.Fatal("trustAnchor returned a nil pool")
	}
}

func TestTrustAnchorRejectsAnEmptyOrBadCA(t *testing.T) {
	cases := map[string]map[string]string{
		"missing key":       {"other": "x"},
		"not a certificate": {trustKey: "not a certificate"},
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset(caConfigMap(data))
			if _, err := trustAnchor(context.Background(), clientset); err == nil {
				t.Fatal("trustAnchor returned no error")
			}
		})
	}
}

func apiPod(name string, ready bool) *corev1.Pod {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: operatorNamespace,
			Labels:    map[string]string{"app": apiServiceName},
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}},
		},
	}
}

func TestPickReadyPod(t *testing.T) {
	clientset := fake.NewSimpleClientset(apiPod("down", false), apiPod("up", true))
	got, err := pickReadyPod(context.Background(), clientset)
	if err != nil {
		t.Fatalf("pickReadyPod: %v", err)
	}
	if got != "up" {
		t.Fatalf("pickReadyPod = %q, want up", got)
	}
}

func TestPickReadyPodWithNoneReady(t *testing.T) {
	clientset := fake.NewSimpleClientset(apiPod("down", false))
	if _, err := pickReadyPod(context.Background(), clientset); err == nil {
		t.Fatal("pickReadyPod returned no error with no Ready pod")
	}
}

func TestBearerToken(t *testing.T) {
	if got, err := bearerToken(&rest.Config{BearerToken: "inline"}); err != nil || got != "inline" {
		t.Fatalf("bearerToken(inline) = (%q, %v)", got, err)
	}

	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("writing the token file: %v", err)
	}
	if got, err := bearerToken(&rest.Config{BearerTokenFile: file}); err != nil || got != "from-file" {
		t.Fatalf("bearerToken(file) = (%q, %v)", got, err)
	}

	if got, err := bearerToken(&rest.Config{}); err != nil || got != "" {
		t.Fatalf("bearerToken(none) = (%q, %v), want an empty token and no error", got, err)
	}
}

// clientKeyPairPEM returns a self-signed certificate and its private
// key, both PEM, for a config that authenticates with a client
// certificate.
func clientKeyPairPEM(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "caller"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating a certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshaling the key: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

func TestClientCertificate(t *testing.T) {
	certPEM, keyPEM := clientKeyPairPEM(t)
	config := &rest.Config{TLSClientConfig: rest.TLSClientConfig{CertData: certPEM, KeyData: keyPEM}}
	accessor, err := clientCertificate(config)
	if err != nil {
		t.Fatalf("clientCertificate: %v", err)
	}
	if accessor == nil {
		t.Fatal("clientCertificate found no certificate in a client-certificate context")
	}
	certificate, err := accessor(&tls.CertificateRequestInfo{})
	if err != nil || len(certificate.Certificate) == 0 {
		t.Fatalf("the accessor returned (%v, %v), want a certificate", certificate, err)
	}
}

func TestClientCertificateAbsent(t *testing.T) {
	accessor, err := clientCertificate(&rest.Config{})
	if err != nil {
		t.Fatalf("clientCertificate: %v", err)
	}
	if accessor != nil {
		t.Fatal("clientCertificate found a certificate in a context that carries none")
	}
}
