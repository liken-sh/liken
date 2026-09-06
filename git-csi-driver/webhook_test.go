package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd/api"
)

// webhookTime is the moment the listener writes on every mark.
var webhookTime = time.Unix(1757000000, 0).UTC()

// testWebhook is the controller's listener on a fake cluster, with a
// clock a test moves.
func testWebhook(t *testing.T, logs io.Writer) *webhook {
	t.Helper()
	at := webhookTime
	readings := newMetrics()
	readings.registerWebhook()
	return &webhook{
		client:   fake.NewClientset(),
		readings: readings,
		logger:   slog.New(slog.NewTextHandler(logs, nil)),
		now:      func() time.Time { return at },
		held:     map[string]cachedSecret{},
	}
}

// listening serves the webhook on a socket of the test's own and
// answers its address.
func listening(t *testing.T, hooks *webhook) string {
	t.Helper()
	served := httptest.NewServer(http.HandlerFunc(hooks.handle))
	t.Cleanup(served.Close)
	return served.URL
}

// webhookSecret writes the Secret a path names.
func webhookSecret(t *testing.T, hooks *webhook, namespace, name, value string) {
	t.Helper()
	held := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Data:       map[string][]byte{webhookSecretKey: []byte(value)},
	}
	if _, err := hooks.client.CoreV1().Secrets(namespace).
		Create(t.Context(), held, metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the Secret: %v", err)
	}
}

// followingVolume is a read-only PersistentVolume of this driver, bound
// to a claim in the namespace, with the attributes a test names.
func followingVolume(
	t *testing.T, hooks *webhook, name, namespace string, attributes map[string]string,
) {
	t.Helper()
	writeVolume(t, hooks, &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PersistentVolumeSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:           driverName,
					VolumeHandle:     name,
					VolumeAttributes: attributes,
				},
			},
			ClaimRef: &corev1.ObjectReference{Namespace: namespace, Name: name},
		},
	})
}

// writeVolume puts one PersistentVolume in the fake cluster.
func writeVolume(t *testing.T, hooks *webhook, held *corev1.PersistentVolume) {
	t.Helper()
	if _, err := hooks.client.CoreV1().PersistentVolumes().
		Create(t.Context(), held, metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the PersistentVolume: %v", err)
	}
}

// demandedAt is the annotation plan 10's channel carries, and "" where
// the volume carries none.
func demandedAt(t *testing.T, hooks *webhook, name string) string {
	t.Helper()
	held, err := hooks.client.CoreV1().PersistentVolumes().
		Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the PersistentVolume: %v", err)
	}
	return held.Annotations[demandAnnotation]
}

// githubPush is the payload GitHub sends for a push to the URL, and the
// header that signs it with the secret.
func githubPush(url, ref, secret string) (string, map[string]string) {
	body := `{"ref":"` + ref + `","repository":{"clone_url":"` + url + `"}}`
	return body, map[string]string{githubSignatureHeader: githubPrefix + signature(body, secret)}
}

// post sends one request to the listener and answers its status and its
// body.
func post(t *testing.T, address, path string, header map[string]string, body string) (int, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost, address+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("making the request: %v", err)
	}
	for key, value := range header {
		request.Header.Set(key, value)
	}
	answer, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("posting the request: %v", err)
	}
	defer answer.Body.Close()
	read, err := io.ReadAll(answer.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	return answer.StatusCode, string(read)
}

func TestAVerifiedPushMarksTheVolume(t *testing.T) {
	logs := &logbook{}
	hooks := testWebhook(t, logs)
	webhookSecret(t, hooks, "sites", "x-webhook", "what-the-forge-was-told")
	followingVolume(t, hooks, "x", "sites", map[string]string{
		"url": "https://code.example.com/data/x.git", "ref": "main",
		webhookSecretAttribute: "x-webhook",
	})
	body, header := githubPush(
		"https://code.example.com/data/x.git", "refs/heads/main", "what-the-forge-was-told")

	code, answered := post(t, listening(t, hooks), "/webhook/sites/x-webhook", header, body)

	if code != http.StatusAccepted {
		t.Fatalf("the listener answered %d, want 202 (%q)", code, answered)
	}
	if !strings.Contains(answered, "marked 1") {
		t.Errorf("the listener answered %q, want the count in it", answered)
	}
	if got := demandedAt(t, hooks, "x"); got != webhookTime.Format(time.RFC3339) {
		t.Errorf("the volume is marked %q, want %q", got, webhookTime.Format(time.RFC3339))
	}
	for _, want := range []string{
		"secret=sites/x-webhook", "forge=github", "ref=refs/heads/main", "marked=1",
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("the log is %q, want %q in it", logs, want)
		}
	}
	if strings.Contains(logs.String(), "what-the-forge-was-told") {
		t.Errorf("the log is %q, want the secret's value out of it", logs)
	}
}

func TestAPushThatMatchesNothingIsAccepted(t *testing.T) {
	hooks := testWebhook(t, io.Discard)
	webhookSecret(t, hooks, "sites", "x-webhook", "s")
	body, header := githubPush("https://code.example.com/data/other.git", "refs/heads/main", "s")

	code, answered := post(t, listening(t, hooks), "/webhook/sites/x-webhook", header, body)

	if code != http.StatusAccepted {
		t.Fatalf("the listener answered %d, want 202 (%q)", code, answered)
	}
	if !strings.Contains(answered, "marked 0") {
		t.Errorf("the listener answered %q, want a count of zero", answered)
	}
}

func TestTheListenerRefusesAndSaysNothing(t *testing.T) {
	body, header := githubPush("https://code.example.com/data/x.git", "refs/heads/main", "s")

	for _, c := range []struct {
		name   string
		path   string
		header map[string]string
		body   string
	}{
		{
			name: "a path that names no Secret", path: "/", header: header, body: body,
		},
		{
			name: "a path with no name after the namespace",
			path: "/webhook/sites", header: header, body: body,
		},
		{
			name: "a path with an empty namespace",
			path: "/webhook//x-webhook", header: header, body: body,
		},
		{
			name: "a path with an empty name",
			path: "/webhook/sites/", header: header, body: body,
		},
		{
			name: "a path with more than a name",
			path: "/webhook/sites/x-webhook/again", header: header, body: body,
		},
		{
			name: "a Secret that is not there",
			path: "/webhook/sites/absent", header: header, body: body,
		},
		{
			name: "a signature of another secret",
			path: "/webhook/sites/x-webhook",
			header: map[string]string{
				githubSignatureHeader: githubPrefix + signature(body, "other"),
			},
			body: body,
		},
		{
			name: "no header the driver verifies",
			path: "/webhook/sites/x-webhook", header: nil, body: body,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			hooks := testWebhook(t, io.Discard)
			webhookSecret(t, hooks, "sites", "x-webhook", "s")

			code, answered := post(t, listening(t, hooks), c.path, c.header, c.body)

			if code != http.StatusUnauthorized {
				t.Errorf("the listener answered %d, want 401", code)
			}
			if answered != "" {
				t.Errorf("the listener answered %q, want an empty body", answered)
			}
		})
	}
}

func TestASecretWithNoValueRefusesTheRequest(t *testing.T) {
	hooks := testWebhook(t, io.Discard)
	if _, err := hooks.client.CoreV1().Secrets("sites").Create(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "sites", Name: "x-webhook"},
		Data:       map[string][]byte{"token": []byte("s")},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the Secret: %v", err)
	}
	body, header := githubPush("https://code.example.com/data/x.git", "refs/heads/main", "s")

	code, _ := post(t, listening(t, hooks), "/webhook/sites/x-webhook", header, body)

	if code != http.StatusUnauthorized {
		t.Errorf("the listener answered %d, want 401", code)
	}
}

func TestTheListenerReportsWhatItCouldNotServe(t *testing.T) {
	body, header := githubPush("https://code.example.com/data/x.git", "refs/heads/main", "s")
	refused := func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("the API server said no")
	}

	for _, c := range []struct {
		name  string
		cross func(*webhook)
	}{
		{
			name: "a Secret the API server would not answer for",
			cross: func(hooks *webhook) {
				hooks.client.(*fake.Clientset).PrependReactor("get", "secrets", refused)
			},
		},
		{
			name: "a list of volumes the API server would not answer for",
			cross: func(hooks *webhook) {
				hooks.client.(*fake.Clientset).PrependReactor("list", "persistentvolumes", refused)
			},
		},
		{
			name:  "a controller that found no cluster",
			cross: func(hooks *webhook) { hooks.client = nil },
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs := &logbook{}
			hooks := testWebhook(t, logs)
			webhookSecret(t, hooks, "sites", "x-webhook", "s")
			c.cross(hooks)

			code, answered := post(t, listening(t, hooks), "/webhook/sites/x-webhook", header, body)

			if code != http.StatusInternalServerError {
				t.Errorf("the listener answered %d, want 500", code)
			}
			if answered != "" {
				t.Errorf("the listener answered %q, want an empty body", answered)
			}
			if !strings.Contains(logs.String(), "the webhook was not served") {
				t.Errorf("the log is %q, want the request it did not serve in it", logs)
			}
			if got := webhookCounter(t, hooks.readings,
				"git_csi_webhook_requests_total", resultFailed); got != 1 {
				t.Errorf("git_csi_webhook_requests_total{result=\"failed\"} reads %v, want 1", got)
			}
		})
	}
}

func TestTheListenerRefusesABodyItCannotRead(t *testing.T) {
	for _, c := range []struct {
		name string
		body func() string
	}{
		{
			name: "a body over the size limit",
			body: func() string { return strings.Repeat("a", webhookBodyLimit+1) },
		},
		{
			name: "a body that is not the JSON of a push",
			body: func() string { return `{"zen":"design for failure"}` },
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			hooks := testWebhook(t, io.Discard)
			webhookSecret(t, hooks, "sites", "x-webhook", "s")
			body := c.body()
			header := map[string]string{
				githubSignatureHeader: githubPrefix + signature(body, "s"),
			}

			code, answered := post(t, listening(t, hooks), "/webhook/sites/x-webhook", header, body)

			if code != http.StatusBadRequest {
				t.Errorf("the listener answered %d, want 400", code)
			}
			if answered != "" {
				t.Errorf("the listener answered %q, want an empty body", answered)
			}
		})
	}
}

func TestTheListenerCountsWhatItAnswered(t *testing.T) {
	hooks := testWebhook(t, io.Discard)
	webhookSecret(t, hooks, "sites", "x-webhook", "s")
	followingVolume(t, hooks, "x", "sites", map[string]string{
		"url": "https://code.example.com/data/x.git", webhookSecretAttribute: "x-webhook",
	})
	body, header := githubPush("https://code.example.com/data/x.git", "refs/heads/main", "s")
	address := listening(t, hooks)

	post(t, address, "/webhook/sites/x-webhook", header, body)
	post(t, address, "/webhook/sites/x-webhook", nil, body)
	post(t, address, "/webhook/sites/x-webhook",
		map[string]string{githubSignatureHeader: githubPrefix + signature("{}", "s")}, "{}")

	for result, want := range map[string]float64{
		resultAccepted: 1, resultUnauthenticated: 1, resultMalformed: 1,
	} {
		if got := webhookCounter(t, hooks.readings, "git_csi_webhook_requests_total", result); got != want {
			t.Errorf("git_csi_webhook_requests_total{result=%q} reads %v, want %v", result, got, want)
		}
	}
	if got := webhookCounter(t, hooks.readings, "git_csi_webhook_marked_total", ""); got != 1 {
		t.Errorf("git_csi_webhook_marked_total reads %v, want 1", got)
	}
}

// webhookCounter is what one of the controller's counters reads.
func webhookCounter(t *testing.T, readings *metrics, name, result string) float64 {
	t.Helper()
	families, err := readings.registry.Gather()
	if err != nil {
		t.Fatalf("gathering the metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["result"] == result {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func TestTheSecretIsReadOnceInsideTheWindow(t *testing.T) {
	hooks := testWebhook(t, io.Discard)
	webhookSecret(t, hooks, "sites", "x-webhook", "s")
	reads := 0
	hooks.client.(*fake.Clientset).PrependReactor("get", "secrets",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			reads++
			return false, nil, nil
		})
	at := webhookTime
	hooks.now = func() time.Time { return at }
	body, header := githubPush("https://code.example.com/data/x.git", "refs/heads/main", "s")
	address := listening(t, hooks)

	post(t, address, "/webhook/sites/x-webhook", header, body)
	post(t, address, "/webhook/sites/x-webhook", header, body)
	if reads != 1 {
		t.Errorf("the listener read the Secret %d times inside the window, want 1", reads)
	}

	at = at.Add(webhookSecretTTL)
	post(t, address, "/webhook/sites/x-webhook", header, body)
	if reads != 2 {
		t.Errorf("the listener read the Secret %d times after the window, want 2", reads)
	}
}

func TestNewWebhookTakesTheAddressItIsGiven(t *testing.T) {
	logs := &logbook{}
	logger := slog.New(slog.NewTextHandler(logs, nil))

	none, err := newWebhook(&config{}, newMetrics(), logger)
	if err != nil || none.listener != nil {
		t.Errorf("newWebhook answered %v, %v, want no listener and no error", none.listener, err)
	}

	taken, err := newWebhook(&config{webhook: "127.0.0.1:0"}, newMetrics(), logger)
	if err != nil {
		t.Fatalf("newWebhook: %v", err)
	}
	if err := taken.listener.Close(); err != nil {
		t.Fatalf("closing the listener: %v", err)
	}

	if _, err := newWebhook(&config{webhook: "127.0.0.1:-1"}, newMetrics(), logger); err == nil {
		t.Error("newWebhook answered no error for an address it cannot take")
	}
}

func TestTheListenerServesUntilTheRunEnds(t *testing.T) {
	hooks := testWebhook(t, io.Discard)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	hooks.listener = listener
	ctx, stop := context.WithCancel(t.Context())
	served := make(chan struct{})
	go func() {
		defer close(served)
		hooks.serve(ctx)
	}()

	code, _ := post(t, "http://"+listener.Addr().String(), "/", nil, "")
	if code != http.StatusUnauthorized {
		t.Errorf("the listener answered %d, want 401", code)
	}

	stop()
	select {
	case <-served:
	case <-time.After(30 * time.Second):
		t.Fatal("the listener did not stop with the run")
	}
}

func TestAWebhookListenerThatStopsOnItsOwnIsReported(t *testing.T) {
	logs := &logbook{}
	hooks := testWebhook(t, logs)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("closing the listener: %v", err)
	}
	hooks.listener = listener

	hooks.serve(t.Context())

	if !strings.Contains(logs.String(), "the webhook listener stopped") {
		t.Errorf("the log is %q, want the listener that stopped in it", logs)
	}
}

func TestTheControllerReadsItsOwnCredentials(t *testing.T) {
	logs := &logbook{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	if clusterClient(logger, func() (*rest.Config, error) {
		return &rest.Config{Host: "https://10.43.0.1:443"}, nil
	}) == nil {
		t.Error("clusterClient answered no client for a cluster it can reach")
	}
	if clusterClient(logger, func() (*rest.Config, error) {
		return nil, errors.New("not in a cluster")
	}) != nil {
		t.Error("clusterClient answered a client with no cluster")
	}
	if clusterClient(logger, func() (*rest.Config, error) {
		return &rest.Config{Host: "https://10.43.0.1:443",
			ExecProvider: &api.ExecConfig{},
			AuthProvider: &api.AuthProviderConfig{Name: "gcp"}}, nil
	}) != nil {
		t.Error("clusterClient answered a client for a configuration client-go refuses")
	}
	if !strings.Contains(logs.String(), "no webhook") {
		t.Errorf("the log is %q, want the missing cluster in it", logs)
	}
}
