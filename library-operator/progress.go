package main

// The progress role is the container beside the standing progress
// agent. It is the one process in the namespace that writes the
// progress store: it subscribes to each Play's position and audience,
// writes the rows, and publishes what it recorded.
//
// It holds no Kubernetes credential and it answers on no port. The
// operator is the only API client, so everything the role learns about
// a Play arrives on the bus, on the topics progressbus.go names, and
// everything the operator learns back goes out the same way.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// The argument that selects this role, the way reportMode selects the
// reporter. The operator writes it over the image's entrypoint.
const progressMode = "progress"

// progressWriteTimeout bounds one message's writes and reads, so an
// agent that stops answering cannot hold the bus reader forever.
var progressWriteTimeout = 30 * time.Second

// How long the role holds the bus open after it publishes the closing
// offline, so the writer goroutine sends it before the process exits. A
// variable, so a test drives a shutdown in milliseconds.
var progressFlushGrace = 500 * time.Millisecond

// One progress role: the namespace it records, the two topic trees it
// reads, the store it writes, and the bus it publishes on.
type progress struct {
	namespace         string
	topicBase         string
	mediaBase         string
	availabilityTopic string
	store             *progressStore
	bus               *Bus
	log               io.Writer
}

// runProgress is the role's whole program: read the environment,
// record until the kubelet stops the container, and mark the role
// offline on the way out.
func runProgress() {
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	role := newProgress(os.Stdout)
	role.subscribe()
	role.serve(stopped)
}

// newProgress reads the namespace, the two topic trees, the broker, and
// the agent's address out of the container's environment, the only
// place a pod with no API credential learns them.
func newProgress(log io.Writer) *progress {
	namespace := os.Getenv(libraryNamespaceVariable)
	base := os.Getenv(topicBaseVariable)
	if base == "" {
		base = defaultTopicBase
	}
	api := os.Getenv(progressAPIVariable)
	if api == "" {
		api = defaultProgressAPI
	}

	role := newProgressOn(namespace, base, mediaTopicBaseOf(os.Getenv(mediaTopicBaseVariable)),
		// No client timeout, because every request bounds itself with a
		// context instead.
		newProgressStore(api, defaultProgressClient()), log)
	fmt.Fprintf(log, "library.liken.sh: recording the progress of %s to %s\n", namespace, api)

	role.bus = newBus(os.Getenv(busAddressVariable), "progress-"+namespace,
		&busWill{Topic: role.availabilityTopic, Payload: []byte(availabilityOffline), Retained: true},
		role.onConnect, role.onMessage)
	return role
}

// newProgressOn builds the role around a store a caller already has, so
// a test drives the whole handler with no environment and no pod.
func newProgressOn(namespace, base, mediaBase string, store *progressStore, log io.Writer) *progress {
	return &progress{
		namespace:         namespace,
		topicBase:         base,
		mediaBase:         mediaBase,
		availabilityTopic: progressAvailabilityTopic(base, namespace),
		store:             store,
		log:               log,
	}
}

// Subscribe names every topic the role records from. The Bus remembers
// each filter and sends it again on every reconnect, so a broker that
// restarts delivers the retained messages back.
func (p *progress) subscribe() {
	p.bus.Subscribe(mediaPlayStatusFilter(p.mediaBase, p.namespace))
	p.bus.Subscribe(mediaPlayAvailabilityFilter(p.mediaBase, p.namespace))
	p.bus.Subscribe(playAudienceFilter(p.topicBase, p.namespace))
	p.bus.Subscribe(playFinalFilter(p.topicBase, p.namespace))
	// The outside plays are the jellyfin role's, on the same tree, and
	// one namespace's role records only its own.
	p.bus.Subscribe(playOutsideFilter(p.topicBase, p.namespace))
	// A Person is cluster-scoped, so every namespace's role answers
	// every forget request.
	p.bus.Subscribe(personForgetFilter(p.topicBase))
}

// Serve holds the bus open until the context ends, then marks this role
// offline and returns. The bus runs on a context of its own, so the
// closing publish has a live connection to go out on.
func (p *progress) serve(stopped context.Context) {
	running, stopBus := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.bus.Run(running)
	}()

	<-stopped.Done()

	p.bus.Publish(p.availabilityTopic, []byte(availabilityOffline), true)
	time.Sleep(progressFlushGrace)
	stopBus()
	<-done
}

// onConnect marks the role online the moment a session connects,
// because a broker that restarts drops its retained messages. It
// republishes nothing else: every message the role publishes is derived
// from a message the broker holds retained, and the resubscribe
// delivers those back.
func (p *progress) onConnect(bus *Bus) {
	bus.Publish(p.availabilityTopic, []byte(availabilityOnline), true)
}

// onMessage records one message. It runs on the bus reader's goroutine,
// so each write bounds itself with a context: a store that stops
// answering costs the role its reads for that long and never its
// connection.
func (p *progress) onMessage(topic string, payload []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), progressWriteTimeout)
	defer cancel()

	if namespace, play, kind, ok := parsePlayTopic(p.mediaBase, topic); ok {
		// The availability of a playback pod ends no Play. The operator
		// reads the Play's last status off the API and publishes the
		// final, which is what closes the row.
		if kind == mediaStatusKind && namespace == p.namespace {
			p.recordStatus(ctx, play, payload)
		}
		return
	}
	if namespace, play, kind, ok := parsePlayTopic(p.topicBase, topic); ok && namespace == p.namespace {
		switch kind {
		case playAudienceKind:
			p.recordAudience(ctx, play, payload)
		case playOutsideKind:
			p.recordOutside(ctx, play, payload)
		case playFinalKind:
			p.recordFinal(ctx, play, payload)
		}
		return
	}
	if person, kind, _, ok := parsePersonTopic(p.topicBase, topic); ok && kind == personForgetKind {
		p.forget(ctx, person, payload)
	}
}

// mediaPlayStatus is the report media-operator's playback sidecar
// publishes for one Play. The role reads the item and the two positions
// and ignores every other field, so a field media-operator adds costs
// nothing here.
type mediaPlayStatus struct {
	Item     int    `json:"item"`
	Position string `json:"position"`
	Duration string `json:"duration"`
}

// recordStatus writes where one Play reached and publishes what it
// wrote. An empty payload is a cleared topic, and it leaves the rows
// alone, because the rows are the history.
//
// A status for a Play the store already holds as ended writes nothing
// and publishes nothing. The playback pod reports for about a second
// past the final, and a recorded message that said ended false again
// would hold the Play's finalizer for good.
func (p *progress) recordStatus(ctx context.Context, play string, payload []byte) {
	if len(payload) == 0 {
		return
	}
	status := mediaPlayStatus{}
	if err := json.Unmarshal(payload, &status); err != nil {
		p.logf("the status of %s reads as no report: %v", play, err)
		return
	}
	position := p.seconds(play, status.Position)
	duration := p.seconds(play, status.Duration)

	wrote, err := p.store.recordPosition(ctx, play, status.Item, position, duration, time.Now().UTC())
	if err != nil {
		p.logf("could not record the position of %s: %v", play, err)
		return
	}
	if !wrote {
		return
	}
	p.publishRecorded(play, status.Item, position, false)
}

// recordAudience writes what the operator knows about a Play. An empty
// payload is the clear the operator publishes once it has released the
// Play, and the rows stay as the history of it.
func (p *progress) recordAudience(ctx context.Context, play string, payload []byte) {
	if len(payload) == 0 {
		return
	}
	audience := playAudience{}
	if err := json.Unmarshal(payload, &audience); err != nil {
		p.logf("the audience of %s reads as no audience: %v", play, err)
		return
	}

	if err := p.store.recordAudience(ctx, play, audience, time.Now().UTC()); err != nil {
		p.logf("could not record the audience of %s: %v", play, err)
		return
	}
}

// recordOutside writes one play that ran outside this cluster, whole, from
// the one message that carries it.
// It publishes nothing back, because no Play resource holds this row.
func (p *progress) recordOutside(ctx context.Context, play string, payload []byte) {
	if len(payload) == 0 {
		return
	}
	outside := outsidePlay{}
	if err := json.Unmarshal(payload, &outside); err != nil {
		p.logf("the outside play %s reads as no play: %v", play, err)
		return
	}

	if err := p.store.recordOutside(ctx, play, outside); err != nil {
		p.logf("could not record the outside play %s: %v", play, err)
	}
}

// recordFinal writes the last status of a Play and publishes the ended
// mark. The operator holds the Play's finalizer until it reads that
// mark, so this write is what lets the Play be deleted.
func (p *progress) recordFinal(ctx context.Context, play string, payload []byte) {
	if len(payload) == 0 {
		return
	}
	final := playFinal{}
	if err := json.Unmarshal(payload, &final); err != nil {
		p.logf("the final status of %s reads as no status: %v", play, err)
		return
	}
	position := p.seconds(play, final.Position)
	duration := p.seconds(play, final.Duration)

	if err := p.store.recordFinal(ctx, play, final, position, duration, time.Now().UTC()); err != nil {
		p.logf("could not record the final status of %s: %v", play, err)
		return
	}
	p.publishRecorded(play, final.Item, position, true)
}

// forget takes one person out of this namespace's store and answers
// that they are gone. An empty request is the operator's clear, and the
// role clears its own answer with it.
func (p *progress) forget(ctx context.Context, person string, payload []byte) {
	topic := personForgottenTopic(p.topicBase, person, p.namespace)
	if len(payload) == 0 {
		p.bus.Publish(topic, nil, true)
		return
	}
	if err := p.store.forgetPerson(ctx, person); err != nil {
		p.logf("could not forget %s: %v", person, err)
		return
	}
	answer, _ := json.Marshal(map[string]string{"at": time.Now().UTC().Format(time.RFC3339)})
	p.bus.Publish(topic, answer, true)
}

// publishRecorded says what the role wrote last for one Play, retained,
// so the operator reads it back after a restart of either side. The
// position is written back from the seconds the store holds, so the
// message and the row never say different things.
func (p *progress) publishRecorded(play string, item, position int, ended bool) {
	payload, _ := json.Marshal(playRecorded{
		Item:     item,
		Position: formatPosition(position),
		Ended:    ended,
		At:       time.Now().UTC().Format(time.RFC3339),
	})
	p.bus.Publish(playRecordedTopic(p.topicBase, p.namespace, play), payload, true)
}

// seconds reads one H:MM:SS value as seconds. A value it cannot read
// records zero and leaves a line in the pod log, because a report of a
// shape nobody expected must not stop the rest of the message.
func (p *progress) seconds(play, value string) int {
	seconds, ok := parsePosition(value)
	if !ok {
		p.logf("the position %q of %s reads as no time", value, play)
	}
	return seconds
}

// logf writes one line under the shared prefix, or nothing when the
// role was built without a log.
func (p *progress) logf(format string, args ...any) {
	if p.log == nil {
		return
	}
	fmt.Fprintf(p.log, "library.liken.sh: "+format+"\n", args...)
}
