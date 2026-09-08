package main

// jellyfinbackfill.go is the one-time backfill of plan 50: the subcommand
// that reads what a Jellyfin server already holds of each person's progress
// and publishes it on the bus as outside plays, the same messages the webhook
// path publishes. The operator stands it as a Job, in jellyfinbackfilljob.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The argument that selects the backfill, the way jellyfinMode selects the
// role, and the worker name its Job is labeled with.
const (
	jellyfinBackfillMode   = "jellyfin-backfill"
	workerJellyfinBackfill = "jellyfin-backfill"
)

// The two reads one user takes, as the query each adds to the item listing:
// the items with a resume point, and the items the user finished. The two
// filters narrow together, so they are separate calls.
const (
	jellyfinResumableQuery = "&filters=IsResumable"
	jellyfinPlayedQuery    = "&isPlayed=true"
)

// the recorded time an item with no LastPlayedDate carries. One second past
// the epoch is older than any real play, so the store's tie rule lets a real
// play overwrite it and never the other way.
const jellyfinBackfillFloor = 1

// the three bounds of the run. The connect wait is how long the run waits for
// the broker before it fails, so a Job with no broker ends and Kubernetes
// retries it. The pace is the gap between two publishes: the bus drops a
// publish that overflows its queue, so the run hands the writer one message
// at a time. The flush is what the run waits after its last publish, so the
// writer reaches the socket before the connection ends. All three are
// variables so a test drives them in microseconds.
var (
	jellyfinBackfillConnect = 30 * time.Second
	jellyfinBackfillPace    = time.Millisecond
	jellyfinBackfillFlush   = 2 * time.Second
)

// what one run counted: the users it read, the plays it published, the items
// it skipped for naming no provider ids, and the users whose items it could
// not read.
type jellyfinBackfillCounts struct {
	users     int
	published int
	skipped   int
	failed    int
}

// one backfill: the namespace and topic tree it publishes on, the server it
// reads, the index it reads a series' ids through, and the bus it publishes
// over. It holds no Kubernetes credential, the way the role holds none.
type jellyfinBackfill struct {
	namespace string
	topicBase string
	api       *jellyfinAPI
	index     *jellyfinIndex
	publish   func(topic string, payload []byte, retained bool)
	pace      time.Duration
	bus       *Bus
	ready     chan struct{}
	log       io.Writer
}

// the Job's whole program: read the environment, publish what Jellyfin
// already holds, and end the process with what the run left. A failure is a
// non-zero exit, so the Job fails and the next pass runs it again.
func runJellyfinBackfill() {
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	backfill := newJellyfinBackfill(os.Stdout)
	counts, err := backfill.runOnBus(stopped)
	backfill.logf("backfilled %d users: %d plays published, %d items skipped",
		counts.users, counts.published, counts.skipped)
	if err != nil {
		backfill.logf("the jellyfin backfill failed: %v", err)
		stop()
		os.Exit(1)
	}
}

// newJellyfinBackfill reads the namespace, the topic tree, the broker, and
// the Jellyfin server out of the container's environment, the same variables
// the jellyfin role reads, without the listen address and without the media
// tree, because the backfill answers nothing and reads no Play.
func newJellyfinBackfill(log io.Writer) *jellyfinBackfill {
	namespace := os.Getenv(libraryNamespaceVariable)
	base := os.Getenv(topicBaseVariable)
	if base == "" {
		base = defaultTopicBase
	}
	address := os.Getenv(jellyfinURLVariable)

	backfill := newJellyfinBackfillOn(namespace, base,
		newJellyfinAPI(address, os.Getenv(jellyfinAPIKeyVariable), &http.Client{}), nil, log)
	backfill.pace = jellyfinBackfillPace
	fmt.Fprintf(log, "library.liken.sh: backfilling the progress of %s from %s\n", namespace, address)

	// the connect callback signals that the broker answered the handshake.
	// A publish made while the client is disconnected is dropped, so
	// nothing goes out until the handshake is done.
	ready, once := make(chan struct{}), sync.Once{}
	backfill.ready = ready
	backfill.bus = newBus(os.Getenv(busAddressVariable), "jellyfin-backfill-"+namespace, nil,
		func(*Bus) { once.Do(func() { close(ready) }) }, nil)
	backfill.publish = backfill.bus.Publish
	return backfill
}

// newJellyfinBackfillOn builds the backfill around a server and a publish
// function a caller already has, so a test drives the whole run with no
// environment, no broker, and no pod.
func newJellyfinBackfillOn(namespace, topicBase string, api *jellyfinAPI,
	publish func(string, []byte, bool), log io.Writer) *jellyfinBackfill {
	return &jellyfinBackfill{
		namespace: namespace,
		topicBase: topicBase,
		api:       api,
		index:     newJellyfinIndex(api, time.Now, log),
		publish:   publish,
		log:       log,
	}
}

// runOnBus holds the connection open around one run: it starts the client,
// waits for the handshake, runs the backfill, and waits the flush before the
// connection ends.
func (b *jellyfinBackfill) runOnBus(ctx context.Context) (jellyfinBackfillCounts, error) {
	running, stopBus := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.bus.Run(running)
	}()
	defer func() {
		stopBus()
		<-done
	}()

	if err := b.await(ctx); err != nil {
		return jellyfinBackfillCounts{}, err
	}
	counts, err := b.run(ctx)
	time.Sleep(jellyfinBackfillFlush)
	return counts, err
}

// await waits for the broker to answer the handshake, bounded by the connect
// wait and by the context, so a Job that reaches no broker fails instead of
// publishing into nothing.
func (b *jellyfinBackfill) await(ctx context.Context) error {
	timer := time.NewTimer(jellyfinBackfillConnect)
	defer timer.Stop()
	select {
	case <-b.ready:
		return nil
	case <-timer.C:
		return fmt.Errorf("the broker did not answer within %s", jellyfinBackfillConnect)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// one run: every user's resumable and played items, each as one outside play
// on the bus. A user whose items cannot be read is logged and counted, and
// the run carries on with the next user, so one user's failure does not cost
// the others their progress. The joined error fails the Job, so the next pass
// runs it again.
func (b *jellyfinBackfill) run(ctx context.Context) (jellyfinBackfillCounts, error) {
	counts := jellyfinBackfillCounts{}
	users, err := b.api.users(ctx)
	if err != nil {
		return counts, fmt.Errorf("reading the users of jellyfin: %w", err)
	}

	failures := []error{}
	for _, user := range users {
		counts.users++
		items, err := b.itemsOf(ctx, user.ID)
		if err != nil {
			counts.failed++
			b.logf("could not read the items of the user %s: %v", user.Name, err)
			failures = append(failures, err)
			continue
		}
		for _, item := range items {
			b.carry(ctx, user, item, &counts)
		}
	}
	return counts, errors.Join(failures...)
}

// one item as one message on the play's outside topic, or one skip. The
// message is not retained, the way the webhook path publishes, and the pace
// holds the writer's queue short.
func (b *jellyfinBackfill) carry(ctx context.Context, user jellyfinUser,
	item jellyfinItem, counts *jellyfinBackfillCounts) {
	name, play, ok := b.outsideOf(ctx, user, item)
	if !ok {
		counts.skipped++
		b.logf("the jellyfin item %s names no provider ids", item.ID)
		return
	}
	payload, _ := json.Marshal(play)
	b.publish(playOutsideTopic(b.topicBase, b.namespace, name), payload, false)
	counts.published++
	if b.pace > 0 {
		time.Sleep(b.pace)
	}
}

// the two reads one user takes, as one list with no item twice. An item that
// is both resumable and played comes back in both answers, and the played
// answer wins, because it is the one that says the person finished.
func (b *jellyfinBackfill) itemsOf(ctx context.Context, user string) ([]jellyfinItem, error) {
	resumable, err := b.api.userItems(ctx, user, jellyfinResumableQuery)
	if err != nil {
		return nil, err
	}
	played, err := b.api.userItems(ctx, user, jellyfinPlayedQuery)
	if err != nil {
		return nil, err
	}

	at := map[string]int{}
	held := make([]jellyfinItem, 0, len(resumable)+len(played))
	for _, item := range append(resumable, played...) {
		if index, seen := at[item.ID]; seen {
			held[index] = item
			continue
		}
		at[item.ID] = len(held)
		held = append(held, item)
	}
	return held, nil
}

// the Play name and the payload one item becomes, or ok false for an item
// that names no work. The name is the user and the item, the same name the
// webhook path gives them, so a backfilled row and a later play are one row
// that moves.
func (b *jellyfinBackfill) outsideOf(ctx context.Context, user jellyfinUser,
	item jellyfinItem) (string, outsidePlay, bool) {
	aliases, season, episode := b.identityOf(ctx, item)
	if len(aliases) == 0 {
		return "", outsidePlay{}, false
	}
	return jellyfinPlayerName + "-" + user.ID + "-" + item.ID, outsidePlay{
		Player:   jellyfinPlayerName,
		People:   []string{user.Name},
		Aliases:  aliases,
		Season:   season,
		Episode:  episode,
		Position: jellyfinSeconds(item.UserData.PlaybackPositionTicks),
		Duration: jellyfinSeconds(item.RunTimeTicks),
		Ended:    item.UserData.Played,
		At:       jellyfinBackfillAt(item.UserData.LastPlayedDate),
	}, true
}

// the identity of the work one item names. An episode takes the series'
// provider ids with the season and episode numbers beside them, the way the
// webhook path reads them, because that is the identity the catalog gives an
// episode play.
func (b *jellyfinBackfill) identityOf(ctx context.Context, item jellyfinItem) (map[string]string, int, int) {
	if strings.EqualFold(item.Type, jellyfinEpisodeType) {
		return b.index.seriesAliases(ctx, item.SeriesID), item.ParentIndexNumber, item.IndexNumber
	}
	return jellyfinProviders(item.ProviderIds), 0, 0
}

// the recorded time one item carries. Jellyfin writes the date with seven
// fractional digits, and an item a person marked played by hand carries no
// date at all, which reads as the floor.
func jellyfinBackfillAt(date string) int64 {
	at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(date))
	if err != nil {
		return jellyfinBackfillFloor
	}
	return at.Unix()
}

// logf writes one line under the shared prefix. Every backfill is built with
// a log, the pod's own standard output, so there is nothing to guard.
func (b *jellyfinBackfill) logf(format string, args ...any) {
	fmt.Fprintf(b.log, "library.liken.sh: "+format+"\n", args...)
}
