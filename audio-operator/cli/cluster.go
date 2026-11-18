package main

// capture reads these from the cluster before it opens a stream: the
// operator's version, the CA that signs the capture API's certificate,
// a Ready audio-api pod to forward to, and the caller's own credential.
// The names here must match the operator's manifests in deploy/.

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
	operatorNamespace = "liken-system"

	// kubectl liken plugins finds each operator's workload by this label
	// and installs the CLI of the same version. This CLI reads the same
	// label to find its operator's version. pluginDomain is
	// audio-operator's value.
	pluginLabel  = "cli.liken.sh/plugin"
	pluginDomain = "audio"

	// apiServiceName names the capture API's Service and Deployment, and
	// its pods have the label app=audio-api. apiServiceDNS is the name in
	// the API's serving certificate.
	apiServiceName = "audio-api"
	apiServiceDNS  = "audio-api.liken-system.svc"

	apiContainerPort = 8443

	trustConfigMap = "audio-api-ca"
	trustKey       = "ca.crt"
)

var pluginSelector = pluginLabel + "=" + pluginDomain

// The CLI reads Sink and Source through the dynamic client, so it needs
// no generated client for these types.
var sinkGVR = schema.GroupVersionResource{
	Group:    "audio.liken.sh",
	Version:  "v1alpha1",
	Resource: "sinks",
}

var sourceGVR = schema.GroupVersionResource{
	Group:    "audio.liken.sh",
	Version:  "v1alpha1",
	Resource: "sources",
}

// Sink and Source are cluster-scoped, so listEndpoints names no
// namespace.
func listEndpoints(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource) ([]string, error) {
	list, err := client.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.GetName())
	}
	return names, nil
}

// operatorVersion returns the tag of the first container image of the
// DaemonSet that has the plugin label. The tag is the operator's release
// version, so the CLI reads it without calling the operator. The label
// is on the audio-operator DaemonSet, not on the audio-api Deployment.
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

// imageTag returns the tag of an image reference. It returns "" when
// the reference has no tag, and ignores a digest after the tag.
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

func trustAnchor(ctx context.Context, clientset kubernetes.Interface) (*x509.CertPool, error) {
	configmap, err := clientset.CoreV1().ConfigMaps(operatorNamespace).Get(ctx, trustConfigMap, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	anchor := configmap.Data[trustKey]
	if anchor == "" {
		return nil, fmt.Errorf("the ConfigMap %s in %s has no key %s", trustConfigMap, operatorNamespace, trustKey)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(anchor)) {
		return nil, fmt.Errorf("the key %s of the ConfigMap %s in %s contains no PEM certificate", trustKey, trustConfigMap, operatorNamespace)
	}
	return pool, nil
}

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
	return "", fmt.Errorf("no pod with the label app=%s in %s is Ready", apiServiceName, operatorNamespace)
}

func podReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// bearerToken returns "" and no error when the config has no token, as
// for a context that authenticates with a client certificate.
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

// clientCertificate returns nil when the context has no client
// certificate. The capture API verifies the certificate against the
// cluster's client CA and reads the caller's identity from its subject,
// so a person captures with the kubeconfig they already have.
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
