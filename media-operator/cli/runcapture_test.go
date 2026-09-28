package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// A clientcmd.ClientConfig whose only live method is
// Namespace, the one runCapture reads to resolve an unset -n.
type fakeLoader struct {
	namespace string
	err       error
}

func (l fakeLoader) RawConfig() (clientcmdapi.Config, error) { return clientcmdapi.Config{}, nil }
func (l fakeLoader) ClientConfig() (*rest.Config, error)     { return nil, nil }
func (l fakeLoader) Namespace() (string, bool, error)        { return l.namespace, false, l.err }
func (l fakeLoader) ConfigAccess() clientcmd.ConfigAccess    { return nil }

// A RESTClientGetter that hands runCapture a namespace
// loader and a fixed ToRESTConfig outcome, so a test drives the reach
// path up to the first cluster call without a cluster.
type fakeGetter struct {
	loader    clientcmd.ClientConfig
	config    *rest.Config
	configErr error
}

func (g fakeGetter) ToRESTConfig() (*rest.Config, error)                            { return g.config, g.configErr }
func (g fakeGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) { return nil, nil }
func (g fakeGetter) ToRESTMapper() (meta.RESTMapper, error)                         { return nil, nil }
func (g fakeGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig                  { return g.loader }

func TestRunCaptureResolvesTheNamespaceFromTheContext(t *testing.T) {
	getter := fakeGetter{
		loader:    fakeLoader{namespace: "house"},
		configErr: errors.New("no reachable cluster"),
	}
	err := runCapture(context.Background(), getter,
		captureOptions{Name: "living-room", Format: "mp4"}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("runCapture returned no error when the config is unreachable")
	}
	if !errors.Is(err, getter.configErr) {
		t.Fatalf("error %q does not carry the config failure", err)
	}
}

func TestRunCaptureReportsANamespaceThatWillNotResolve(t *testing.T) {
	getter := fakeGetter{loader: fakeLoader{err: errors.New("no current context")}}
	err := runCapture(context.Background(), getter,
		captureOptions{Name: "living-room", Format: "mp4"}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("runCapture returned no error when the namespace will not resolve")
	}
}

func TestRunCaptureRequiresACredential(t *testing.T) {
	getter := fakeGetter{
		loader: fakeLoader{namespace: "house"},
		config: &rest.Config{Host: "https://127.0.0.1:1"},
	}
	err := runCapture(context.Background(), getter,
		captureOptions{Name: "living-room", Format: "mp4"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "neither a bearer token nor a client certificate") {
		t.Fatalf("runCapture error = %v, want the missing-credential error", err)
	}
}
