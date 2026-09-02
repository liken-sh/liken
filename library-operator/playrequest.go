package main

// The playback half of the operator. A screen pod holds no API
// credential, so a person's choice on the wall reaches the control
// plane over the bus: the browser resolves the list from the catalog
// beside it and publishes the paths, and this file joins each path to
// the Library's claim and creates the Play. The browser resolves and
// the operator does not, because Corrosion's API binds to loopback in
// every pod, so the operator can read no namespace's catalog.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
)

// playRequest is one request as the browser publishes it. The
// namespace and the Player come from the topic and never from the
// payload, so a request cannot name a Player other than the one whose
// topic carried it.
type playRequest struct {
	Namespace string `json:"-"`
	Player    string `json:"-"`
	Library   string `json:"library"`
	// The catalog's slug for the item the person chose, the movie's or
	// the chosen episode's. It names the Play and nothing else, so a
	// request that carries none still plays.
	Slug  string            `json:"slug"`
	Items []playRequestItem `json:"items"`
}

// playRequestItem is one item of the list. Every path is relative to
// the library root, exactly as the catalog stores it, and the operator
// joins the claim and the root onto it.
type playRequestItem struct {
	Path         string            `json:"path"`
	Presentation *PlayPresentation `json:"presentation,omitempty"`
}

// playRequests is the queue the bus handler fills and the pass drains.
// The handler runs on the bus reader's goroutine and the pass on the
// loop's, so one mutex covers the slice.
type playRequests struct {
	mutex   sync.Mutex
	pending []playRequest
	wake    chan<- struct{}
}

func newPlayRequests(wake chan<- struct{}) *playRequests {
	return &playRequests{wake: wake}
}

// hold keeps one request for the next pass and wakes the loop. A
// person waits at the screen for the film to start, so a request never
// waits for the backstop tick.
func (p *playRequests) hold(request playRequest) {
	p.mutex.Lock()
	p.pending = append(p.pending, request)
	p.mutex.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// take returns everything held, in the order it arrived, and empties
// the queue. A request is one moment: a pass that could not serve it
// must not serve it again on the next tick.
func (p *playRequests) take() []playRequest {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	taken := p.pending
	p.pending = nil
	return taken
}

// readPlayRequest decodes one message off a play topic. An empty
// payload and one that does not decode are both dropped. Only the
// second is reported, because nothing retained stands on a play topic
// for a clear to remove.
func (o *operator) readPlayRequest(namespace, player, topic string, payload []byte) {
	if len(payload) == 0 {
		return
	}
	var request playRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		fmt.Fprintf(os.Stderr, "reading the play request on %s: %v\n", topic, err)
		return
	}
	request.Namespace, request.Player = namespace, player
	o.plays.hold(request)
}

// createPlays turns every held request into a Play. The pass holds
// the Players and the Libraries already, so every check is a read of
// what it has: the Player must be one this operator serves, and the
// Library must be one the Player's namespace holds. A request that
// fails a check is reported and dropped, because the screen has no way
// to answer and the pod log is where a person looks.
func (o *operator) createPlays(ctx context.Context, players []Player, libraries []Library) {
	for _, request := range o.plays.take() {
		play, err := request.play(players, libraries)
		if err != nil {
			fmt.Fprintf(os.Stderr, "playing on %s/%s: %v\n",
				request.Namespace, request.Player, err)
			continue
		}
		if _, err := CreatePlay(ctx, o.client, play); err != nil {
			fmt.Fprintf(os.Stderr, "playing on %s/%s: %v\n",
				request.Namespace, request.Player, err)
		}
	}
}

// play is the Play one request becomes, or the reason it becomes none.
// Every refusal here is a request that named something the screen may
// not reach.
func (r playRequest) play(players []Player, libraries []Library) (*Play, error) {
	player := r.player(players)
	if player == nil {
		return nil, fmt.Errorf("no player of this operator's answers to that name")
	}
	library := r.library(libraries)
	if library == nil {
		return nil, fmt.Errorf("namespace %s holds no library %s", r.Namespace, r.Library)
	}
	if library.Spec.Storage.Claim == "" {
		return nil, fmt.Errorf("library %s names no claim", r.Library)
	}

	items := make([]PlayItem, 0, len(r.Items))
	for _, item := range r.Items {
		stamped, err := item.stamped(library)
		if err != nil {
			return nil, err
		}
		items = append(items, stamped)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("the request named nothing to play")
	}

	return &Play{
		APIVersion: playerAPIVersion,
		Kind:       "Play",
		Metadata: ObjectMeta{
			GenerateName: playGenerateName(r.Player, r.Slug),
			Namespace:    r.Namespace,
		},
		Spec: PlaySpec{Players: []string{r.Player}, Items: items},
	}, nil
}

// The longest prefix the operator asks the API server to mint a name
// from, counting the Player, the slug, and the two hyphens that join
// them. The API server appends its own suffix, so this budget leaves
// room for it inside a DNS-1123 label.
const playNameBudget = 50

// playGenerateName is the prefix the API server mints a Play name from.
// The slug is in it so that kubectl get plays reads as titles instead
// of one line per unit. A request that carries no slug, or one whose
// slug folds to nothing, falls back to the Player alone, because a Play
// that starts matters more than its name.
func playGenerateName(player, slug string) string {
	fragment := capped(labelFragment(slug), playNameBudget-len(player)-2)
	if fragment == "" {
		return player + "-"
	}
	return player + "-" + fragment + "-"
}

// labelFragment folds a catalog slug to a DNS-1123 label fragment.
// Lowercase letters and digits pass through, a run of anything else
// becomes one hyphen, and the fragment carries no leading or trailing
// hyphen. The operator folds a slug the catalog already built because
// the request comes over the bus from a pod, so nothing but this
// function guarantees the shape a name needs.
func labelFragment(text string) string {
	var folded strings.Builder
	pendingHyphen := false
	for _, letter := range strings.ToLower(text) {
		switch {
		case letter >= 'a' && letter <= 'z', letter >= '0' && letter <= '9':
			if pendingHyphen && folded.Len() > 0 {
				folded.WriteByte('-')
			}
			pendingHyphen = false
			folded.WriteRune(letter)
		default:
			pendingHyphen = true
		}
	}
	return folded.String()
}

// capped is the cap on the fragment, in bytes. A fragment longer than
// the budget is cut back to the last hyphen inside it, so a name ends
// on a whole word where one is in reach and on the hard cut where none
// is. A budget of nothing leaves no fragment.
func capped(fragment string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if len(fragment) <= budget {
		return fragment
	}
	cut := fragment[:budget]
	if at := strings.LastIndexByte(cut, '-'); at >= 0 {
		cut = cut[:at]
	}
	return cut
}

// player is the Player this request names, and only when this
// operator stands its idle screen. A request for any other Player came
// from a screen this operator does not draw.
func (r playRequest) player(players []Player) *Player {
	for index := range players {
		player := &players[index]
		if player.Metadata.Namespace != r.Namespace || player.Metadata.Name != r.Player {
			continue
		}
		if !player.delegated() {
			return nil
		}
		return player
	}
	return nil
}

// library is the Library this request names, and only in the Player's
// own namespace. The namespace is the boundary: a screen plays the
// libraries beside it and no others.
func (r playRequest) library(libraries []Library) *Library {
	namespace, name, found := strings.Cut(r.Library, "/")
	if !found || namespace != r.Namespace {
		return nil
	}
	for index := range libraries {
		library := &libraries[index]
		if library.Metadata.Namespace == namespace && library.Metadata.Name == name {
			return library
		}
	}
	return nil
}

// stamped is one item with its paths joined to the library's claim.
// The main file must be there. The art and the trickplay are joined
// only where the catalog holds them, and an item with neither carries
// neither.
func (i playRequestItem) stamped(library *Library) (PlayItem, error) {
	uri, err := reference(library, i.Path)
	if err != nil {
		return PlayItem{}, err
	}
	item := PlayItem{URI: uri}
	if i.Presentation == nil {
		return item, nil
	}

	presentation := *i.Presentation
	for _, beside := range []*string{&presentation.Art, &presentation.Trickplay} {
		if *beside == "" {
			continue
		}
		if *beside, err = reference(library, *beside); err != nil {
			return PlayItem{}, err
		}
	}
	item.Presentation = &presentation
	return item, nil
}

// reference is the media reference one relative path becomes. The
// claim scheme mounts the Library's own claim read-only on the
// playback pod, so a file plays from the volume the scanner walked and
// no second claim is created.
func reference(library *Library, relative string) (string, error) {
	if !inside(relative) {
		return "", fmt.Errorf("the path %q is not inside the library", relative)
	}
	return "claim://" + library.Spec.Storage.Claim + "/" +
		path.Join(library.Spec.Storage.Root, relative), nil
}

// inside reports whether a path names a file under the library root.
// An empty path names nothing, an absolute path leaves the mount, and
// a path that climbs above the root reaches another library's files
// or the rest of the volume.
func inside(relative string) bool {
	if relative == "" || path.IsAbs(relative) {
		return false
	}
	cleaned := path.Clean(relative)
	return cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}
