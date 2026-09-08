package main

// jellyfin.go is the role that keeps a person's playback progress the same in
// the progress store and in a Jellyfin server, in both directions. Plan 47
// builds it.
// The role holds no Kubernetes credential. It reads its server, its key, and
// its two topic trees out of the environment, it answers the webhook Jellyfin
// posts to, and everything it learns of this cluster's Plays arrives on the
// bus.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// The argument that selects this role, the way progressMode selects the
// progress role.
const jellyfinMode = "jellyfin"

// The variables the role reads its Jellyfin server from: the address, the API
// key of one administrator, and the address the webhook posts to. The pod
// holds no Kubernetes credential, so the environment is where it reads them.
const (
	jellyfinURLVariable    = "LIBRARY_JELLYFIN_URL"
	jellyfinAPIKeyVariable = "LIBRARY_JELLYFIN_API_KEY"
	jellyfinListenVariable = "LIBRARY_JELLYFIN_LISTEN"
	defaultJellyfinListen  = ":8080"
)

// One jellyfin role: the namespace it serves, the two topic trees it reads,
// the address it answers on, the server it writes, and the two halves that
// carry a play each way.
type jellyfin struct {
	namespace string
	topicBase string
	mediaBase string
	listen    string
	api       *jellyfinAPI
	index     *jellyfinIndex
	out       *jellyfinOutbound
	now       func() time.Time
	publish   func(topic string, payload []byte, retained bool)
	bus       *Bus
	log       io.Writer
}

// runJellyfin is the role's whole program: read the environment, carry
// progress both ways until the kubelet stops the container, and stop.
func runJellyfin() {
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	role := newJellyfin(os.Stdout)
	role.subscribe()
	role.serve(stopped)
}

// newJellyfin reads the namespace, the two topic trees, the broker, and the
// Jellyfin server out of the container's environment, the only place a pod
// with no API credential learns them.
func newJellyfin(log io.Writer) *jellyfin {
	namespace := os.Getenv(libraryNamespaceVariable)
	base := os.Getenv(topicBaseVariable)
	if base == "" {
		base = defaultTopicBase
	}
	listen := os.Getenv(jellyfinListenVariable)
	if listen == "" {
		listen = defaultJellyfinListen
	}
	address := os.Getenv(jellyfinURLVariable)

	// The client takes no timeout of its own, because every request
	// bounds itself with a context instead.
	role := newJellyfinOn(namespace, base, mediaTopicBaseOf(os.Getenv(mediaTopicBaseVariable)), listen,
		newJellyfinAPI(address, os.Getenv(jellyfinAPIKeyVariable), &http.Client{}),
		time.Now, nil, log)
	fmt.Fprintf(log, "library.liken.sh: carrying the progress of %s to %s\n", namespace, address)

	role.bus = newBus(os.Getenv(busAddressVariable), "jellyfin-"+namespace, nil, nil, role.onMessage)
	role.publish = role.bus.Publish
	return role
}

// newJellyfinOn builds the role around a server and a clock a caller already
// has, so a test drives both halves with no environment, no broker, and no
// pod.
func newJellyfinOn(namespace, base, mediaBase, listen string, api *jellyfinAPI,
	now func() time.Time, publish func(string, []byte, bool), log io.Writer) *jellyfin {
	index := newJellyfinIndex(api, now, log)
	return &jellyfin{
		namespace: namespace,
		topicBase: base,
		mediaBase: mediaBase,
		listen:    listen,
		api:       api,
		index:     index,
		out:       newJellyfinOutbound(api, index, now, log),
		now:       now,
		publish:   publish,
		log:       log,
	}
}

// Subscribe names the three topics the outbound half joins: the sidecar's
// position on the media tree, and the operator's audience and final on this
// operator's tree. The role publishes on a topic of its own and reads none of
// its own back.
func (j *jellyfin) subscribe() {
	j.bus.Subscribe(mediaPlayStatusFilter(j.mediaBase, j.namespace))
	j.bus.Subscribe(playAudienceFilter(j.topicBase, j.namespace))
	j.bus.Subscribe(playFinalFilter(j.topicBase, j.namespace))
}

// onMessage folds one message into the join. It runs on the bus reader's
// goroutine, and a final writes to Jellyfin on that goroutine, so the write
// bounds itself with a context.
func (j *jellyfin) onMessage(topic string, payload []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), jellyfinRequestTimeout)
	defer cancel()

	if namespace, play, kind, ok := parsePlayTopic(j.mediaBase, topic); ok {
		if kind == mediaStatusKind && namespace == j.namespace {
			j.out.status(play, payload)
		}
		return
	}
	if namespace, play, kind, ok := parsePlayTopic(j.topicBase, topic); ok && namespace == j.namespace {
		switch kind {
		case playAudienceKind:
			j.out.audience(play, payload)
		case playFinalKind:
			j.out.final(ctx, play, payload)
		}
	}
}

// Serve runs the webhook server, the bus, and the write loop until the
// context ends. A server that cannot listen ends the role, because a Jellyfin
// that posts to an address nothing answers loses every event it posts.
func (j *jellyfin) serve(stopped context.Context) {
	running, stopBus := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		j.bus.Run(running)
	}()

	server := &http.Server{Addr: j.listen, Handler: j.handler(), ReadHeaderTimeout: jellyfinHeaderTimeout}
	failed := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			failed <- err
		}
	}()

	j.index.prime(running)
	j.writeLoop(stopped, failed)

	ending, ended := context.WithTimeout(context.Background(), jellyfinRequestTimeout)
	defer ended()
	if err := server.Shutdown(ending); err != nil {
		j.logf("shutting the webhook server down: %v", err)
	}
	stopBus()
	<-done
}

// The write loop is the ten second pass. It ends on the signal the kubelet
// sends, or on a webhook server that stopped on its own.
func (j *jellyfin) writeLoop(stopped context.Context, failed <-chan error) {
	ticker := time.NewTicker(jellyfinWriteInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stopped.Done():
			return
		case err := <-failed:
			j.logf("the webhook server stopped: %v", err)
			return
		case <-ticker.C:
			j.out.tick(stopped)
		}
	}
}

// logf writes one line under the shared prefix, or nothing when the role was
// built without a log.
func (j *jellyfin) logf(format string, args ...any) {
	if j.log == nil {
		return
	}
	fmt.Fprintf(j.log, "library.liken.sh: "+format+"\n", args...)
}
