package main

// The capture verb is a thin client of the media operator's public
// capture API. It streams one Player's composed media, the picture the
// Player shows together with its sound, to a writer. It writes only the
// media bytes, so the output can go through a pipe.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"strings"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
)

// captureTarget is the Player to capture and the container format of
// the stream.
type captureTarget struct {
	Namespace string
	Name      string
	Format    string
}

// captureOptions holds the flags and the positional argument of the
// capture verb.
type captureOptions struct {
	Namespace string
	Name      string
	Format    string
	Force     bool
}

// captureFormats maps a format name to the file extension that ends the
// API path and the media type that the request sends in its Accept
// header.
var captureFormats = map[string][2]string{
	"mp4": {"mp4", "video/mp4"},
	"mkv": {"mkv", "video/matroska"},
}

// targetFromOptions turns the parsed flags into a target. It returns
// an error when the flags do not identify a Player or name an unknown
// format.
func targetFromOptions(opts captureOptions) (captureTarget, error) {
	if opts.Namespace == "" {
		return captureTarget{}, fmt.Errorf("the namespace of the Player is empty, so capture does not know which Player to read")
	}
	if opts.Name == "" {
		return captureTarget{}, fmt.Errorf("the name of the Player is empty, so capture does not know which Player to read")
	}
	if _, ok := captureFormats[opts.Format]; !ok {
		return captureTarget{}, fmt.Errorf("unknown format %q, want mp4 or mkv", opts.Format)
	}
	return captureTarget{Namespace: opts.Namespace, Name: opts.Name, Format: opts.Format}, nil
}

// captureRoute returns the API path of a Player's composed media, and
// the media type for the Accept header.
func captureRoute(target captureTarget) (path, accept string, err error) {
	form, ok := captureFormats[target.Format]
	if !ok {
		return "", "", fmt.Errorf("unknown format %q, want mp4 or mkv", target.Format)
	}
	path = "/v1/media/namespaces/" + target.Namespace + "/players/" + target.Name + "/media." + form[0]
	return path, form[1], nil
}

// streamCapture requests the capture and copies only the media bytes
// to out. A response that is not 2xx becomes an error that includes the
// first 4096 bytes of the API's response body.
func streamCapture(ctx context.Context, client *http.Client, base, token string, target captureTarget, out io.Writer) error {
	path, accept, err := captureRoute(target)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", accept)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("%s %s: %s: %s",
			http.MethodGet, path, response.Status, strings.TrimSpace(string(body)))
	}
	_, err = io.Copy(out, response.Body)
	return err
}

// captureClient returns the HTTPS client for the capture request. It
// trusts only the operator's CA. It dials a forwarded local port, so it
// sets the API Service's DNS name as the TLS server name to match the
// operator's certificate.
func captureClient(anchor *x509.CertPool, certificate func(*tls.CertificateRequestInfo) (*tls.Certificate, error)) *http.Client {
	config := &tls.Config{
		RootCAs:    anchor,
		ServerName: apiServiceDNS,
	}
	if certificate != nil {
		config.GetClientCertificate = certificate
	}
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: config,
		},
	}
}

// runCapture streams the capture to stdout through a local port that it
// forwards to a ready operator pod. When the CLI and the operator have
// different versions, it prints a warning unless --force is set, and
// runs the capture either way.
func runCapture(ctx context.Context, getter genericclioptions.RESTClientGetter, opts captureOptions, stdout, stderr io.Writer) error {
	if opts.Namespace == "" {
		namespace, _, err := getter.ToRawKubeConfigLoader().Namespace()
		if err != nil {
			return err
		}
		opts.Namespace = namespace
	}
	target, err := targetFromOptions(opts)
	if err != nil {
		return err
	}

	config, err := getter.ToRESTConfig()
	if err != nil {
		return err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	operator, err := operatorVersion(ctx, clientset)
	if err != nil {
		fmt.Fprintf(stderr, "reading the version of media-operator: %v\n", err)
	}
	switch action, message := decideVersionAction(version, operator, ""); action {
	case actionRefuse:
		return fmt.Errorf("%s", message)
	case actionWarn:
		if !opts.Force {
			fmt.Fprintln(stderr, message)
		}
	}

	token, err := bearerToken(config)
	if err != nil {
		return err
	}
	certificate, err := clientCertificate(config)
	if err != nil {
		return err
	}
	if token == "" && certificate == nil {
		return fmt.Errorf("the current kubeconfig context has neither a bearer token nor a client certificate, and the API authenticates a capture with one of them")
	}
	anchor, err := trustAnchor(ctx, clientset)
	if err != nil {
		return err
	}
	pod, err := pickReadyPod(ctx, clientset)
	if err != nil {
		return err
	}
	local, stop, err := portForward(config, pod)
	if err != nil {
		return err
	}
	defer stop()

	base := fmt.Sprintf("https://127.0.0.1:%d", local)
	return streamCapture(ctx, captureClient(anchor, certificate), base, token, target, stdout)
}
