package main

// The CLI reads the operator's version from the image tag of its
// Deployment, and the name of the person who runs the command. The CLIs of the other operators find their Deployment and
// read the version in the same way.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const (
	// `liken plugins` finds each operator that ships a CLI by this
	// label, and pluginDomain is this operator's value.
	pluginLabel  = "cli.liken.sh/plugin"
	pluginDomain = "observatory"
)

// pluginSelector selects the operator's Deployment.
var pluginSelector = pluginLabel + "=" + pluginDomain

// operatorVersion returns the tag of the first container's image in the
// operator's Deployment, which is the operator's version.
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
		return "", errors.New("the operator Deployment has no container")
	}
	return imageTag(containers[0].Image), nil
}

// clients are the connections and settings that a subcommand uses.
// typed reads the version and the person's name, dynamic reads and
// writes the observatory resources, and kubeUser is the kubeconfig's
// user, which person falls back to.
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

// person returns the name that the condition's message records: --by
// when the person gave one, else the user that the API server
// authenticated, from the same SelfSubjectReview that `kubectl auth
// whoami` creates, else the kubeconfig's user. The API server's name
// comes first because a person cannot mistype it. When the cluster
// does not serve the review or refuses it, person returns the
// kubeconfig's user.
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

// imageTag returns the tag of an image reference, or "" for a bare name
// or a reference that has only a digest.
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
