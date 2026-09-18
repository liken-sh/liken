package main

// What the CLI reads from the cluster before it streams: the
// operator's version from its Deployment image tag, the trust anchor
// the operator publishes, a Ready pod behind the API Service, and the
// caller's own credential, a bearer token or a client certificate. The
// label selector and the names here are the contract the sibling CLIs
// copy.

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
	// Where liken installs the operator and its API.
	operatorNamespace = "liken-system"

	// The label the base binary selects on to find an
	// operator's Deployment, and this operator's value for it.
	pluginLabel  = "cli.liken.sh/plugin"
	pluginDomain = "display"

	// The Service and Deployment that serve the capture
	// API, and the DNS name its leaf certificate carries.
	apiServiceName = "display-api"
	apiServiceDNS  = "display-api.liken-system.svc"

	// The port the API container serves HTTPS on.
	apiContainerPort = 8443

	// The ConfigMap the operator publishes its CA in,
	// and the key that holds the PEM.
	trustConfigMap = "display-api-ca"
	trustKey       = "ca.crt"
)

// The label selector for the operator Deployment.
var pluginSelector = pluginLabel + "=" + pluginDomain

// The Display resource the capture positional names, as the
// group, version, and resource a dynamic client lists. A Display is
// cluster-scoped, so the list carries no namespace, and its name is the
// output the capture route addresses. Completion reads these with no
// typed client and no generated code, so the sibling CLIs change only
// the three strings for their own object.
var displaysGVR = schema.GroupVersionResource{
	Group:    "display.liken.sh",
	Version:  "v1alpha1",
	Resource: displaysResource,
}

// listDisplays lists the Display names through a dynamic client.
// Completion filters the names it returns against the word under the
// cursor.
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

// operatorVersion reads the version tag off the
// operator Deployment's image, the one source every CLI can read with
// no new API.
func operatorVersion(ctx context.Context, clientset kubernetes.Interface) (string, error) {
	deployments, err := clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{
		LabelSelector: pluginSelector,
	})
	if err != nil {
		return "", err
	}
	if len(deployments.Items) == 0 {
		return "", fmt.Errorf("no Deployment carries the label %s", pluginSelector)
	}
	containers := deployments.Items[0].Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return "", fmt.Errorf("the operator Deployment declares no container")
	}
	return imageTag(containers[0].Image), nil
}

// imageTag reads the tag off an image reference, and
// reports an empty tag for a bare name or a digest reference.
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

// trustAnchor reads the CA the operator publishes and
// builds the pool the capture client trusts.
func trustAnchor(ctx context.Context, clientset kubernetes.Interface) (*x509.CertPool, error) {
	configmap, err := clientset.CoreV1().ConfigMaps(operatorNamespace).Get(ctx, trustConfigMap, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	anchor := configmap.Data[trustKey]
	if anchor == "" {
		return nil, fmt.Errorf("the ConfigMap %s holds no %s", trustConfigMap, trustKey)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(anchor)) {
		return nil, fmt.Errorf("the ConfigMap %s holds no certificate in %s", trustConfigMap, trustKey)
	}
	return pool, nil
}

// pickReadyPod finds one Ready pod behind the API
// Service, which is the pod the port-forward targets.
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

// podReady is true when the pod's Ready condition is
// true.
func podReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// bearerToken reads the caller's own token from the REST config, and
// reports an empty token when the context carries none. A context that
// authenticates with a client certificate names no token, and the
// capture client presents that certificate instead.
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

// clientCertificate reads the caller's client certificate from the
// REST config. The capture API verifies it against the cluster's own
// client authority and reads the caller's identity from it, so a
// person captures with the kubeconfig they already hold. It reports a
// nil accessor when the context authenticates some other way.
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
