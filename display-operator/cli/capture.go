package main

// The capture verb writes the screen of one Display to stdout, and
// writes nothing there but the media bytes, so that stdout can be piped
// into a player.

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

// displaysResource is the plural name of the Display CRD, and
// screenAspect is the part of a Display that the capture routes serve.
const (
	displaysResource = "displays"
	screenAspect     = "screen"
)

type captureTarget struct {
	Name   string
	Format string
}

// captureOptions holds the flags of the capture verb. Name is its
// positional argument, the name of a Display.
type captureOptions struct {
	Name   string
	Format string
	Force  bool
}

// captureFormats maps a --format value to the extension of the API
// route and the media type to send in Accept. An mp4 capture streams
// until the client disconnects, and a png capture is one frame.
var captureFormats = map[string][2]string{
	"mp4": {"mp4", "video/mp4"},
	"png": {"png", "image/png"},
}

func targetFromOptions(opts captureOptions) (captureTarget, error) {
	if opts.Name == "" {
		return captureTarget{}, fmt.Errorf("no output named, as in: capture hdmi-a-1")
	}
	if _, ok := captureFormats[opts.Format]; !ok {
		return captureTarget{}, fmt.Errorf("unknown format %q, want mp4 or png", opts.Format)
	}
	return captureTarget{Name: opts.Name, Format: opts.Format}, nil
}

func captureRoute(target captureTarget) (path, accept string, err error) {
	form, ok := captureFormats[target.Format]
	if !ok {
		return "", "", fmt.Errorf("unknown format %q, want mp4 or png", target.Format)
	}
	path = "/v1/display/" + displaysResource + "/" + target.Name + "/" + screenAspect + "." + form[0]
	return path, form[1], nil
}

// streamCapture requests the capture and copies the response body to
// out. On a non-2xx status it writes nothing to out and returns an
// error that includes the start of the response body.
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
		return fmt.Errorf("requesting %s %s: the API responded %s: %s",
			http.MethodGet, path, response.Status, strings.TrimSpace(string(body)))
	}
	_, err = io.Copy(out, response.Body)
	return err
}

// captureClient returns the HTTPS client for the capture request. It
// connects to a port-forwarded address on 127.0.0.1, so it sets the
// TLS server name to the Service's DNS name, which the serving
// certificate holds, and it trusts the operator's CA.
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

// runCapture captures one Display to stdout. It checks the operator's
// version against this binary's, reads the operator's CA certificate,
// forwards a local port to a Ready pod of the API, and streams the
// capture through that port.
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
		return fmt.Errorf("the kubeconfig context has no bearer token and no client certificate, and the API requires one of them to capture")
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
