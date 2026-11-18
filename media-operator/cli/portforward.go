package main

// The CLI cannot reach the media API through the API server's service
// proxy, because the proxy does not pass on the caller's credentials. So
// the CLI forwards a local port to a Ready API pod, as kubectl
// port-forward does, and connects to that port over TLS.

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// portForward forwards a local port to pod and returns the local port
// and a function that closes the forward.
func portForward(config *rest.Config, pod string) (int, func(), error) {
	roundTripper, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return 0, nil, err
	}
	host, err := url.Parse(config.Host)
	if err != nil {
		return 0, nil, err
	}
	target := &url.URL{
		Scheme: "https",
		Host:   host.Host,
		Path:   path.Join(host.Path, "api/v1/namespaces", operatorNamespace, "pods", pod, "portforward"),
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, target)

	stopChannel := make(chan struct{})
	readyChannel := make(chan struct{})
	ports := []string{fmt.Sprintf("0:%d", apiContainerPort)}
	forwarder, err := portforward.New(dialer, ports, stopChannel, readyChannel, io.Discard, io.Discard)
	if err != nil {
		return 0, nil, err
	}

	errChannel := make(chan error, 1)
	go func() { errChannel <- forwarder.ForwardPorts() }()

	select {
	case <-readyChannel:
	case err := <-errChannel:
		return 0, nil, err
	}

	forwarded, err := forwarder.GetPorts()
	if err != nil {
		close(stopChannel)
		return 0, nil, err
	}
	return int(forwarded[0].Local), func() { close(stopChannel) }, nil
}
