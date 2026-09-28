package main

// webhook.go is the listener the controller serves. A forge posts a
// push to /webhook/<namespace>/<name>, the controller verifies it
// against that Secret, and the volumes that name the Secret are marked.

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

// webhookPath is the path a forge posts to. What follows it names the
// Secret's namespace and the Secret.
const webhookPath = "/webhook/"

// webhookSecretKey is the one key the Secret holds. Its value is the
// string the person typed into the forge's webhook form.
const webhookSecretKey = "secret"

// webhookSecretAttribute is the attribute a volume names its Secret
// with.
const webhookSecretAttribute = "webhookSecret"

// webhookBodyLimit is the largest body the listener reads. A push
// payload names the repository and the commits of one push, and no
// forge sends a megabyte of that.
const webhookBodyLimit = 1 << 20

// webhookDeadline bounds one request and the reads it makes, so an
// unverified caller cannot make the controller wait.
const webhookDeadline = 10 * time.Second

// webhookSecretTTL is how long a Secret is held after it is read. A
// person rotates the secret, and the listener takes the new one inside
// this window with no restart of the Deployment.
const webhookSecretTTL = 30 * time.Second

// restartDeadline bounds the mark a restart makes.
const restartDeadline = time.Minute

// The four answers, which are the counter's label values.
const (
	resultAccepted        = "accepted"
	resultUnauthenticated = "unauthenticated"
	resultMalformed       = "malformed"
	resultFailed          = "failed"
)

// errUnconfigured marks a Secret a person has not written yet: a Secret
// that is not there, or a Secret with no secret key. Both are the
// person's configuration, so both refuse the request. Every other
// failure to read a Secret is the cluster's, and the listener answers
// that it could not serve.
var errUnconfigured = errors.New("the Secret is not configured")

// webhook is the listener and the client it reads the cluster with.
// The controller holds one for its whole run.
type webhook struct {
	client   kubernetes.Interface
	listener net.Listener
	readings *metrics
	logger   *slog.Logger
	now      func() time.Time

	// held is the Secrets read inside the last webhookSecretTTL, by
	// namespace and name.
	mu   sync.Mutex
	held map[string]cachedSecret
}

// cachedSecret is one Secret's value and when it was read.
type cachedSecret struct {
	value string
	read  time.Time
}

// newWebhook reads the controller's own credentials from the pod it
// runs in and takes the address --webhook names. An empty address
// serves no listener, the way an empty --metrics does.
func newWebhook(cfg *config, readings *metrics, logger *slog.Logger) (*webhook, error) {
	readings.registerWebhook()
	hooks := &webhook{
		client:   clusterClient(logger, rest.InClusterConfig),
		readings: readings,
		logger:   logger,
		now:      time.Now,
		held:     map[string]cachedSecret{},
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

// serve makes the restart's demand, then answers on the listener until
// the run ends.
func (w *webhook) serve(ctx context.Context) {
	w.demandAll(ctx)
	if w.listener == nil {
		return
	}
	// The timeouts bound how long an unverified caller holds a
	// connection.
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
		w.logger.WarnContext(ctx, "the webhook listener stopped", "error", err)
	}
}

// handle answers one request. The path names the Secret, the Secret
// verifies the body, and the body names what to mark.
func (w *webhook) handle(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), webhookDeadline)
	defer cancel()

	namespace, name, named := webhookTarget(request.URL.Path)
	if !named {
		w.refuse(ctx, writer, request.URL.Path, "the path names no Secret")
		return
	}
	held := namespace + "/" + name

	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, webhookBodyLimit))
	if err != nil {
		w.malformed(ctx, writer, held, "the body was not read: "+err.Error())
		return
	}
	secret, err := w.secretOf(ctx, namespace, name)
	switch {
	case errors.Is(err, errUnconfigured):
		w.refuse(ctx, writer, held, "the Secret was not read: "+err.Error())
		return
	case err != nil:
		w.failed(ctx, writer, held, "the Secret was not read: "+err.Error())
		return
	}
	forge, verified := verify(request.Header, body, secret)
	if !verified {
		w.refuse(ctx, writer, held, "the request is not signed with the Secret")
		return
	}
	pushed, isPush := parsePush(body)
	if !isPush {
		w.malformed(ctx, writer, held, "the body is not the JSON of a push")
		return
	}

	marked, err := w.mark(ctx, namespace, name, pushed)
	if err != nil {
		w.failed(ctx, writer, held, "the volumes were not listed: "+err.Error())
		return
	}
	w.readings.webhookAnswered(resultAccepted)
	w.logger.InfoContext(ctx, "the webhook was accepted",
		"secret", held, "forge", forge, "ref", pushed.ref, "marked", marked)
	writer.WriteHeader(http.StatusAccepted)
	// The count is what a person configuring a webhook reads, so a URL
	// that matches nothing reads 0.
	fmt.Fprintf(writer, "marked %d\n", marked)
}

// refuse answers 401 with an empty body. The log line names the Secret
// and the reason, and never the secret itself.
func (w *webhook) refuse(ctx context.Context, writer http.ResponseWriter, held, reason string) {
	w.readings.webhookAnswered(resultUnauthenticated)
	w.logger.InfoContext(ctx, "the webhook was refused", "secret", held, "reason", reason)
	writer.WriteHeader(http.StatusUnauthorized)
}

// failed answers 500 with an empty body. The listener verified the
// request and could not act on it. A 202 with a count of zero would
// read as a repository nothing follows, which is the wrong diagnosis.
func (w *webhook) failed(ctx context.Context, writer http.ResponseWriter, held, reason string) {
	w.readings.webhookAnswered(resultFailed)
	w.logger.WarnContext(ctx, "the webhook was not served", "secret", held, "reason", reason)
	writer.WriteHeader(http.StatusInternalServerError)
}

// malformed answers 400 with an empty body.
func (w *webhook) malformed(ctx context.Context, writer http.ResponseWriter, held, reason string) {
	w.readings.webhookAnswered(resultMalformed)
	w.logger.InfoContext(ctx, "the webhook was not read", "secret", held, "reason", reason)
	writer.WriteHeader(http.StatusBadRequest)
}

// webhookTarget reads the namespace and the Secret out of the path.
// Any other path names no Secret.
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

// secretOf reads the Secret the path named, from the cache while it is
// fresh and from the API server otherwise.
func (w *webhook) secretOf(ctx context.Context, namespace, name string) (string, error) {
	if w.client == nil {
		return "", errors.New("the controller found no cluster to read it from")
	}
	key := namespace + "/" + name
	if value, fresh := w.cached(key); fresh {
		return value, nil
	}
	found, err := w.client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return "", fmt.Errorf("%w: %s is not there", errUnconfigured, key)
	}
	if err != nil {
		return "", err
	}
	value := string(found.Data[webhookSecretKey])
	if value == "" {
		return "", fmt.Errorf("%w: %s holds no %s", errUnconfigured, key, webhookSecretKey)
	}
	w.remember(key, value)
	return value, nil
}

// cached is the Secret read inside the last webhookSecretTTL, and
// false when none was.
func (w *webhook) cached(key string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	holding, found := w.held[key]
	if !found || w.now().Sub(holding.read) >= webhookSecretTTL {
		return "", false
	}
	return holding.value, true
}

// remember holds the Secret until the window passes.
func (w *webhook) remember(key, value string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.held[key] = cachedSecret{value: value, read: w.now()}
}
