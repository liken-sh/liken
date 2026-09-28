package main

// How the CLI reaches the in-cluster API. The API server's
// services/proxy door strips the caller's identity, so the CLI
// forwards a local port to a Ready API pod, the same reach kubectl
// port-forward makes, and dials the forwarded port over the
// operator's own TLS.

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

// portForward opens a forwarded port to one API pod
// and returns the local port and a function that closes the forward.
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
