package main

// The capture verb, a thin client over the display operator's
// public capture API. It streams one output's screen to a writer and
// puts only the media bytes there, so a pipe stays clean.

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

// The displays resource is the plural the CRD names,
// and screen is the aspect the capture routes serve.
const (
	displaysResource = "displays"
	screenAspect     = "screen"
)

// One capture the API serves and the form it returns.
type captureTarget struct {
	Name   string
	Format string
}

// The capture verb's flags and its one positional
// argument.
type captureOptions struct {
	Name   string
	Format string
	Force  bool
}

// A format name to its extension route and the media
// type the request accepts, in the operator's own spelling. mp4 is a
// clip until the client hangs up; png is one frame.
var captureFormats = map[string][2]string{
	"mp4": {"mp4", "video/mp4"},
	"png": {"png", "image/png"},
}

// targetFromOptions turns the parsed flags into a
// target, or reports why they name no output.
func targetFromOptions(opts captureOptions) (captureTarget, error) {
	if opts.Name == "" {
		return captureTarget{}, fmt.Errorf("capture needs an output")
	}
	if _, ok := captureFormats[opts.Format]; !ok {
		return captureTarget{}, fmt.Errorf("unknown format %q", opts.Format)
	}
	return captureTarget{Name: opts.Name, Format: opts.Format}, nil
}

// captureRoute builds the API path that taps one
// output's screen and the media type the request accepts.
func captureRoute(target captureTarget) (path, accept string, err error) {
	form, ok := captureFormats[target.Format]
	if !ok {
		return "", "", fmt.Errorf("unknown format %q", target.Format)
	}
	path = "/v1/display/" + displaysResource + "/" + target.Name + "/" + screenAspect + "." + form[0]
	return path, form[1], nil
}

// streamCapture issues the tap request and copies only
// the media bytes to out; a non-2xx answer becomes an error that
// carries the API's own words.
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

// The HTTPS client for the capture request. It trusts the
// operator's own CA and sends the API Service's DNS name as the
// server name, while it dials a forwarded local port.
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

// runCapture wires the cluster reach to the stream: it
// reads the operator version and warns or refuses on drift, opens the
// trust anchor and a forwarded port to the API, and streams the media
// to stdout.
func runCapture(ctx context.Context, getter genericclioptions.RESTClientGetter, opts captureOptions, stdout, stderr io.Writer) error {
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
		fmt.Fprintf(stderr, "reading the operator version: %v\n", err)
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
		return fmt.Errorf("the current context carries neither a bearer token nor a client certificate; the API authenticates a tap with one")
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
