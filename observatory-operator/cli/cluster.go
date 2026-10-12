package main

// What the CLI reads from the cluster before it writes: the
// operator's version from its Deployment image tag, and who the
// person is. The label selector and the version read here are the
// contract the sibling CLIs copy.

import (
	"context"
	"fmt"
	"strings"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const (
	// The label the base binary selects on to find an
	// operator's workload, and this operator's value for it.
	pluginLabel  = "cli.liken.sh/plugin"
	pluginDomain = "observatory"
)

// The label selector for the operator Deployment.
var pluginSelector = pluginLabel + "=" + pluginDomain

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

// clients are what a verb reads and writes through, built from the
// standard kube flags: the typed clients for the version and the
// person's name, the dynamic client for the observatory's resources,
// the namespace, and the name of the kubeconfig's user, which names
// the person when the cluster does not.
type clients struct {
	typed     kubernetes.Interface
	dynamic   dynamic.Interface
	namespace string
	kubeUser  string
}

// observatoryClients builds the clients from the standard kube flags.
func observatoryClients(getter genericclioptions.RESTClientGetter) (clients, error) {
	config, err := getter.ToRESTConfig()
	if err != nil {
		return clients{}, err
	}
	typed, err := kubernetes.NewForConfig(config)
	if err != nil {
		return clients{}, err
	}
	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return clients{}, err
	}
	loader := getter.ToRawKubeConfigLoader()
	namespace, _, err := loader.Namespace()
	if err != nil {
		return clients{}, err
	}
	user := ""
	if raw, err := loader.RawConfig(); err == nil {
		if current, ok := raw.Contexts[raw.CurrentContext]; ok {
			user = current.AuthInfo
		}
	}
	return clients{typed: typed, dynamic: dyn, namespace: namespace, kubeUser: user}, nil
}

// person answers the name that the condition's message gives: --by
// when the person gave one, else the user that the API server
// authenticated, as `kubectl auth whoami` reads it, else the
// kubeconfig's user. The API server's answer is the one a person
// cannot mistype, and an older cluster or a refused review falls back.
func person(ctx context.Context, c clients, by string) string {
	if by != "" {
		return by
	}
	review, err := c.typed.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err == nil && review.Status.UserInfo.Username != "" {
		return review.Status.UserInfo.Username
	}
	return c.kubeUser
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
