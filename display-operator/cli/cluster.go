package main

// This file reads what the CLI needs from the cluster before it
// connects to the capture API: the operator's version, the operator's
// CA certificate, a Ready pod of the display-api Deployment, and the
// caller's bearer token or client certificate.

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
	// The namespace of the operator and its API.
	operatorNamespace = "liken-system"

	// The label that the `liken` CLI selects on to find an operator's
	// workload, and this operator's value for it.
	pluginLabel  = "cli.liken.sh/plugin"
	pluginDomain = "display"

	// The name of the Service and the Deployment of the capture API,
	// and the DNS name in its serving certificate.
	apiServiceName = "display-api"
	apiServiceDNS  = "display-api.liken-system.svc"

	apiContainerPort = 8443

	// The ConfigMap where the operator publishes its CA certificate,
	// and the key of the PEM in it.
	trustConfigMap = "display-api-ca"
	trustKey       = "ca.crt"
)

var pluginSelector = pluginLabel + "=" + pluginDomain

// The capture command completes its argument from the names of the
// Displays, because a Display's name is the output that the capture
// route addresses. Displays are cluster-scoped.
var displaysGVR = schema.GroupVersionResource{
	Group:    "display.liken.sh",
	Version:  "v1alpha1",
	Resource: displaysResource,
}

// listDisplays returns the names of all Displays. Shell completion
// filters them against the word being typed.
func listDisplays(ctx context.Context, client dynamic.Interface) ([]string, error) {
	list, err := client.Resource(displaysGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.GetName())
	}
	return names, nil
}

// operatorVersion returns the image tag of the first container of the
// DaemonSet with the plugin label. The label is on the operator's
// DaemonSet and not on the display-api Deployment, because only the
// operator's image has a -cli image at the same tag.
func operatorVersion(ctx context.Context, clientset kubernetes.Interface) (string, error) {
	daemonSets, err := clientset.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{
		LabelSelector: pluginSelector,
	})
	if err != nil {
		return "", err
	}
	if len(daemonSets.Items) == 0 {
		return "", fmt.Errorf("no DaemonSet has the label %s", pluginSelector)
	}
	containers := daemonSets.Items[0].Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return "", fmt.Errorf("the operator DaemonSet has no container")
	}
	return imageTag(containers[0].Image), nil
}

// imageTag returns the tag of an image reference, or an empty string
// for a reference with no tag or with a digest.
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

// trustAnchor returns a pool with the operator's CA certificate, which
// the capture client verifies the server against.
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
		return nil, fmt.Errorf("the ConfigMap %s has no PEM certificate in key %s", trustConfigMap, trustKey)
	}
	return pool, nil
}

// pickReadyPod returns the name of a Ready display-api pod for the
// port-forward to connect to.
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

// bearerToken returns the caller's token from the REST config, or an
// empty string when there is none, as in a kubeconfig context that
// uses a client certificate.
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
// client certificate from the REST config, or nil when the context has
// none. The capture API verifies the certificate against the cluster's
// client CA and reads the caller from it, so a person can capture with
// the kubeconfig they already use.
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
