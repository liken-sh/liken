package main

// This file reads from the cluster what the CLI needs before it
// streams a capture: the operator's version from the image tag of its
// Deployment, the CA certificate that the API publishes, a Ready pod
// behind the API Service, and the caller's bearer token or client
// certificate.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	// The namespace that `liken` installs the operator and its API in.
	operatorNamespace = "liken-system"

	// The label that the base binary selects on to find an operator's
	// Deployment, and the value for this operator.
	pluginLabel  = "cli.liken.sh/plugin"
	pluginDomain = "media"

	// The Service and Deployment that serve the capture API, and the
	// DNS name in the API's certificate.
	apiServiceName = "media-api"
	apiServiceDNS  = "media-api.liken-system.svc"

	// The port that the API container serves HTTPS on.
	apiContainerPort = 8443

	// The ConfigMap that the API publishes its CA certificate in, and
	// the key of the PEM in it.
	trustConfigMap = "media-api-ca"
	trustKey       = "ca.crt"
)

var pluginSelector = pluginLabel + "=" + pluginDomain

// playerGVR identifies Players to the dynamic client that shell
// completion lists them with. The dynamic client needs no typed client
// or generated code.
var playerGVR = schema.GroupVersionResource{
	Group:    "media.liken.sh",
	Version:  "v1alpha1",
	Resource: "players",
}

func listPlayers(ctx context.Context, client dynamic.Interface, namespace string) ([]string, error) {
	list, err := client.Resource(playerGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.GetName())
	}
	return names, nil
}

// operatorVersion returns the tag of the operator Deployment's image,
// which is the operator's version.
func operatorVersion(ctx context.Context, clientset kubernetes.Interface) (string, error) {
	deployments, err := clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{
		LabelSelector: pluginSelector,
	})
	if err != nil {
		return "", err
	}
	if len(deployments.Items) == 0 {
		return "", fmt.Errorf("no Deployment has the label %s", pluginSelector)
	}
	containers := deployments.Items[0].Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return "", fmt.Errorf("the operator Deployment has no containers")
	}
	return imageTag(containers[0].Image), nil
}

// imageTag returns the tag of an image reference, or "" when the
// reference has no tag. A digest is ignored, so name:tag@sha256:... has
// the tag.
func imageTag(image string) string {
	if at := strings.LastIndex(image, "@"); at >= 0 {
		image = image[:at]
	}
	colon := strings.LastIndex(image, ":")
	if colon < 0 || strings.Contains(image[colon+1:], "/") {
		return ""
	}
	return image[colon+1:]
}

// trustAnchor returns a pool with the CA certificate that the API
// publishes, for the capture client to verify the API's certificate.
func trustAnchor(ctx context.Context, clientset kubernetes.Interface) (*x509.CertPool, error) {
	configmap, err := clientset.CoreV1().ConfigMaps(operatorNamespace).Get(ctx, trustConfigMap, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	anchor := configmap.Data[trustKey]
	if anchor == "" {
		return nil, fmt.Errorf("the ConfigMap %s has no key %s", trustConfigMap, trustKey)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(anchor)) {
		return nil, fmt.Errorf("the key %[2]s of the ConfigMap %[1]s has no PEM certificate", trustConfigMap, trustKey)
	}
	return pool, nil
}

// pickReadyPod returns the name of a Ready pod behind the API Service,
// for the port-forward.
func pickReadyPod(ctx context.Context, clientset kubernetes.Interface) (string, error) {
	pods, err := clientset.CoreV1().Pods(operatorNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=" + apiServiceName,
	})
	if err != nil {
		return "", err
	}
	for _, pod := range pods.Items {
		if podReady(pod) {
			return pod.Name, nil
		}
	}
	return "", fmt.Errorf("no Ready %s pod runs in %s", apiServiceName, operatorNamespace)
}

func podReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// bearerToken returns the caller's token from the REST config, inline
// or from its file, or "" when the kubeconfig context has none. A
// context that authenticates with a client certificate has no token.
func bearerToken(config *rest.Config) (string, error) {
	if config.BearerToken != "" {
		return config.BearerToken, nil
	}
	if config.BearerTokenFile != "" {
		token, err := os.ReadFile(config.BearerTokenFile)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(token)), nil
	}
	return "", nil
}

// clientCertificate returns a function that supplies the caller's
// client certificate from the REST config, or nil when the context
// authenticates another way. The capture API verifies the certificate
// against the cluster's client CA and reads the caller's identity from
// it, so a person can capture with the kubeconfig they already use.
func clientCertificate(config *rest.Config) (func(*tls.CertificateRequestInfo) (*tls.Certificate, error), error) {
	kube, err := rest.TLSConfigFor(config)
	if err != nil {
		return nil, err
	}
	if kube == nil {
		return nil, nil
	}
	if kube.GetClientCertificate != nil {
		return kube.GetClientCertificate, nil
	}
	if len(kube.Certificates) > 0 {
		certificates := kube.Certificates
		return func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &certificates[0], nil
		}, nil
	}
	return nil, nil
}
