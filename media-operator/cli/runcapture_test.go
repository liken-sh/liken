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

// fakeLoader is a clientcmd.ClientConfig that implements only
// Namespace, which runCapture calls when -n is not set.
type fakeLoader struct {
	namespace string
	err       error
}

func (l fakeLoader) RawConfig() (clientcmdapi.Config, error) { return clientcmdapi.Config{}, nil }
func (l fakeLoader) ClientConfig() (*rest.Config, error)     { return nil, nil }
func (l fakeLoader) Namespace() (string, bool, error)        { return l.namespace, false, l.err }
func (l fakeLoader) ConfigAccess() clientcmd.ConfigAccess    { return nil }

// fakeGetter is a RESTClientGetter that returns a fixed loader and a
// fixed ToRESTConfig result. With it, a test runs runCapture as far as
// the credential check with no cluster. The version request in between
// fails, and runCapture only prints that error.
type fakeGetter struct {
	loader    clientcmd.ClientConfig
	config    *rest.Config
	configErr error
}

func (g fakeGetter) ToRESTConfig() (*rest.Config, error)                            { return g.config, g.configErr }
func (g fakeGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) { return nil, nil }
func (g fakeGetter) ToRESTMapper() (meta.RESTMapper, error)                         { return nil, nil }
func (g fakeGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig                  { return g.loader }

func TestRunCaptureReadsTheNamespaceFromTheKubeconfigWhenNoneIsSet(t *testing.T) {
	getter := fakeGetter{
		loader:    fakeLoader{namespace: "media"},
		configErr: errors.New("no reachable cluster"),
	}
	err := runCapture(context.Background(), getter,
		captureOptions{Name: "player-1", Format: "mp4"}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("runCapture returned nil, want the error of the unreachable config")
	}
	if !errors.Is(err, getter.configErr) {
		t.Fatalf("the error is %q, want one that wraps the ToRESTConfig error", err)
	}
}

func TestRunCaptureReturnsTheNamespaceLookupError(t *testing.T) {
	getter := fakeGetter{loader: fakeLoader{err: errors.New("no current context")}}
	err := runCapture(context.Background(), getter,
		captureOptions{Name: "player-1", Format: "mp4"}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("runCapture returned nil, want the error of the failed namespace lookup")
	}
}

func TestRunCaptureFailsWhenTheConfigHasNoCredential(t *testing.T) {
	getter := fakeGetter{
		loader: fakeLoader{namespace: "media"},
		config: &rest.Config{Host: "https://127.0.0.1:1"},
	}
	err := runCapture(context.Background(), getter,
		captureOptions{Name: "player-1", Format: "mp4"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "neither a bearer token nor a client certificate") {
		t.Fatalf("the error is %v, want one that says the config has no bearer token or client certificate", err)
	}
}
