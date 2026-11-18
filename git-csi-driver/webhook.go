package main

// The controller's HTTP listener receives push webhooks from a forge at
// /webhook/<namespace>/<name>. It verifies the request's signature with
// that Secret, then writes the pull demand annotation on each read-only
// PersistentVolume that names the Secret in its webhookSecret attribute
// and follows the pushed repository and ref.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const webhookPath = "/webhook/"

// webhookSecretKey is the key of the Secret that holds the secret
// string entered in the forge's webhook settings.
const webhookSecretKey = "secret"

const webhookSecretAttribute = "webhookSecret"

// webhookBodyLimit is the largest request body the listener reads. The
// payload of one push is far smaller than a megabyte.
const webhookBodyLimit = 1 << 20

// webhookDeadline limits one request and the Secret reads it makes, so
// an unverified caller cannot tie up the controller.
const webhookDeadline = 10 * time.Second

// webhookSecretTTL is how long the listener caches a Secret's value, so
// a rotated secret takes effect within this time without a restart.
const webhookSecretTTL = 30 * time.Second

// restartDeadline limits the pull demands that demandAll writes when the
// controller starts.
const restartDeadline = time.Minute

// The result label values of git_csi_webhook_requests_total.
const (
	resultAccepted        = "accepted"
	resultUnauthenticated = "unauthenticated"
	resultMalformed       = "malformed"
	resultFailed          = "failed"
)

// errUnconfigured marks a Secret that does not exist or has no secret
// key. Both are configuration mistakes, so the listener responds 401.
// Any other failure to read the Secret is a cluster fault, and the
// listener responds 500.
var errUnconfigured = errors.New("the Secret is not configured")

type webhook struct {
	client   kubernetes.Interface
	listener net.Listener
	metrics  *metrics
	logger   *slog.Logger
	now      func() time.Time

	// secrets caches Secret values by "namespace/name".
	mu      sync.Mutex
	secrets map[string]cachedSecret
}

type cachedSecret struct {
	value string
	read  time.Time
}

// newWebhook creates the client from the pod's in-cluster credentials
// and listens on the address of --webhook. An empty --webhook opens no
// listener.
func newWebhook(cfg *config, registry *metrics, logger *slog.Logger) (*webhook, error) {
	registry.registerWebhook()
	hooks := &webhook{
		client:  clusterClient(logger, rest.InClusterConfig),
		metrics: registry,
		logger:  logger,
		now:     time.Now,
		secrets: map[string]cachedSecret{},
	}
	if cfg.webhook == "" {
		return hooks, nil
	}
	listener, err := net.Listen("tcp", cfg.webhook)
	if err != nil {
		return nil, err
	}
	hooks.listener = listener
	return hooks, nil
}

// serve calls demandAll once at startup, because a push sent while the
// controller was down is lost. Then it serves requests until ctx ends.
func (w *webhook) serve(ctx context.Context) {
	w.demandAll(ctx)
	if w.listener == nil {
		return
	}
	// The timeouts limit how long an unverified caller can keep a
	// connection open.
	serving := &http.Server{
		Handler:           http.HandlerFunc(w.handle),
		ReadTimeout:       webhookDeadline,
		ReadHeaderTimeout: webhookDeadline,
		WriteTimeout:      webhookDeadline,
		IdleTimeout:       webhookDeadline,
	}
	go func() {
		<-ctx.Done()
		_ = serving.Close()
	}()
	if err := serving.Serve(w.listener); err != nil && ctx.Err() == nil {
		w.logger.WarnContext(ctx, "The webhook listener stopped", "error", err)
	}
}

func (w *webhook) handle(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), webhookDeadline)
	defer cancel()

	namespace, name, named := webhookTarget(request.URL.Path)
	if !named {
		w.refuse(ctx, writer, request.URL.Path, "the path is not /webhook/<namespace>/<name>")
		return
	}
	secretName := namespace + "/" + name

	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, webhookBodyLimit))
	if err != nil {
		w.malformed(ctx, writer, secretName, "reading the request body: "+err.Error())
		return
	}
	secret, err := w.secretOf(ctx, namespace, name)
	switch {
	case errors.Is(err, errUnconfigured):
		w.refuse(ctx, writer, secretName, "reading the Secret: "+err.Error())
		return
	case err != nil:
		w.failed(ctx, writer, secretName, "reading the Secret: "+err.Error())
		return
	}
	forge, verified := verify(request.Header, body, secret)
	if !verified {
		w.refuse(ctx, writer, secretName, "the request has no signature that matches the Secret")
		return
	}
	pushed, isPush := parsePush(body)
	if !isPush {
		w.malformed(ctx, writer, secretName, "the body is not the JSON of a push")
		return
	}

	marked, err := w.mark(ctx, namespace, name, pushed)
	if err != nil {
		w.failed(ctx, writer, secretName, "listing the PersistentVolumes: "+err.Error())
		return
	}
	w.metrics.countWebhookRequest(resultAccepted)
	w.logger.InfoContext(ctx, "Accepted the webhook",
		"secret", secretName, "forge", forge, "ref", pushed.ref, "marked", marked)
	writer.WriteHeader(http.StatusAccepted)
	// The count shows a person who is setting up a webhook when the push
	// matched no volume, as "marked 0".
	fmt.Fprintf(writer, "marked %d\n", marked)
}

// refuse responds 401 with an empty body, so an unverified caller learns
// nothing about the Secret. The log line has the Secret's name and the
// reason, never the secret value.
func (w *webhook) refuse(ctx context.Context, writer http.ResponseWriter, secretName, reason string) {
	w.metrics.countWebhookRequest(resultUnauthenticated)
	w.logger.InfoContext(ctx, "Refused the webhook", "secret", secretName, "reason", reason)
	writer.WriteHeader(http.StatusUnauthorized)
}

// failed responds 500 when the listener cannot read the Secret or list
// the volumes. A 202 with "marked 0" would tell the person that no
// volume follows the repository.
func (w *webhook) failed(ctx context.Context, writer http.ResponseWriter, secretName, reason string) {
	w.metrics.countWebhookRequest(resultFailed)
	w.logger.WarnContext(ctx, "The webhook was not served", "secret", secretName, "reason", reason)
	writer.WriteHeader(http.StatusInternalServerError)
}

func (w *webhook) malformed(ctx context.Context, writer http.ResponseWriter, secretName, reason string) {
	w.metrics.countWebhookRequest(resultMalformed)
	w.logger.InfoContext(ctx, "The webhook body was not read", "secret", secretName, "reason", reason)
	writer.WriteHeader(http.StatusBadRequest)
}

// webhookTarget returns the Secret's namespace and name from a path of
// the form /webhook/<namespace>/<name>, and false for any other path.
func webhookTarget(path string) (string, string, bool) {
	rest, found := strings.CutPrefix(path, webhookPath)
	if !found {
		return "", "", false
	}
	namespace, name, found := strings.Cut(rest, "/")
	if !found || namespace == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return namespace, name, true
}

// secretOf returns the Secret's value from the cache, or reads it from
// the API server when the cache has no value younger than
// webhookSecretTTL.
func (w *webhook) secretOf(ctx context.Context, namespace, name string) (string, error) {
	if w.client == nil {
		return "", errors.New("the controller has no client for the API server")
	}
	key := namespace + "/" + name
	if value, fresh := w.cached(key); fresh {
		return value, nil
	}
	found, err := w.client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return "", fmt.Errorf("%w: the Secret %s does not exist", errUnconfigured, key)
	}
	if err != nil {
		return "", err
	}
	value := string(found.Data[webhookSecretKey])
	if value == "" {
		return "", fmt.Errorf("%w: the Secret %s has no key %s", errUnconfigured, key, webhookSecretKey)
	}
	w.remember(key, value)
	return value, nil
}

func (w *webhook) cached(key string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	entry, found := w.secrets[key]
	if !found || w.now().Sub(entry.read) >= webhookSecretTTL {
		return "", false
	}
	return entry.value, true
}

func (w *webhook) remember(key, value string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.secrets[key] = cachedSecret{value: value, read: w.now()}
}
