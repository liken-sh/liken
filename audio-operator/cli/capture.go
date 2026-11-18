package main

// The capture verb is a client of audio-operator's capture API. It
// writes only the sound of one Sink or Source to stdout, and every
// message to stderr, so the sound can go through a pipe or into a file.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
)

// Resource is sinks or sources, as the API path spells it.
type captureTarget struct {
	Resource string
	Name     string
	Format   string
}

// Name is a Sink name, or a Source name when Source is set.
type captureOptions struct {
	Name   string
	Source bool
	Format string
	Force  bool
}

// captureFormats maps each --format value to the file extension at the
// end of the API path and the media type for the Accept header.
var captureFormats = map[string][2]string{
	"wav":  {"wav", "audio/wav"},
	"flac": {"flac", "audio/flac"},
	"opus": {"opus", "audio/ogg"},
}

func targetFromOptions(opts captureOptions) (captureTarget, error) {
	if opts.Name == "" {
		return captureTarget{}, fmt.Errorf("capture needs the name of a Sink, or of a Source with --source, as in: kubectl liken audio capture living-room > living-room.wav")
	}
	if _, ok := captureFormats[opts.Format]; !ok {
		return captureTarget{}, unknownFormat(opts.Format)
	}
	resource := "sinks"
	if opts.Source {
		resource = "sources"
	}
	return captureTarget{Resource: resource, Name: opts.Name, Format: opts.Format}, nil
}

func unknownFormat(format string) error {
	return fmt.Errorf("unknown format %q: --format takes %s", format, strings.Join(formatNames(), ", "))
}

func captureRoute(target captureTarget) (path, accept string, err error) {
	form, ok := captureFormats[target.Format]
	if !ok {
		return "", "", unknownFormat(target.Format)
	}
	path = "/v1/audio/" + target.Resource + "/" + target.Name + "/audio." + form[0]
	return path, form[1], nil
}

// A status outside 2xx becomes an error that includes the first 4096
// bytes of the response body.
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
		return fmt.Errorf("the capture API returned %s for %s %s: %s",
			response.Status, http.MethodGet, path, strings.TrimSpace(string(body)))
	}
	_, err = io.Copy(out, response.Body)
	return err
}

// captureClient trusts only the operator's CA. It connects to a
// forwarded local port, so it sets the TLS server name to apiServiceDNS,
// the name in the API's certificate.
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

// runCapture streams the capture to stdout through a port-forward to a
// Ready audio-api pod. It reads the CA before the operator's version, so
// an unreachable cluster fails once, on the CA, and prints no version
// warning first. A version mismatch only prints a warning, and --force
// suppresses the warning.
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

	token, err := bearerToken(config)
	if err != nil {
		return err
	}
	certificate, err := clientCertificate(config)
	if err != nil {
		return err
	}
	if token == "" && certificate == nil {
		return fmt.Errorf("the current kubeconfig context has no bearer token and no client certificate, and the capture API needs one of them to authenticate a request")
	}
	anchor, err := trustAnchor(ctx, clientset)
	if err != nil {
		return fmt.Errorf("reading the capture API's CA: %w", err)
	}

	operator, err := operatorVersion(ctx, clientset)
	if err != nil {
		fmt.Fprintf(stderr, "%s: skipping the version check: reading the operator's version: %v\n", programName, err)
	}
	switch action, message := decideVersionAction(version, operator, ""); action {
	case actionRefuse:
		return errors.New(message)
	case actionWarn:
		if !opts.Force {
			fmt.Fprintf(stderr, "%s: %s\n", programName, message)
		}
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
