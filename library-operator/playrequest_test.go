package main

// These tests read what a play request becomes: the Play a pass created,
// the claim references it carries, and the requests that create nothing.
// The refusals matter as much as the creation, because a request arrives
// over the bus from a pod that holds no credential of its own.

import (
	"context"
	"encoding/json"
	"net"
	"testing"
)

// The namespace, the Player, and the library key every request here
// names, so one request differs from another only where the test differs.
const (
	testPlayer      = "den-tv"
	testLibraryKey  = testLibraryNamespace + "/movies"
	testFilmPath    = "Some Film (1999)/Some Film (1999).mkv"
	testPosterPath  = "Some Film (1999)/poster.jpg"
	testTrickplay   = "Some Film (1999)/Some Film (1999).trickplay"
	testFilmClaimed = "claim://movies//movies/Some Film (1999)/Some Film (1999).mkv"
)

// A house with one delegated screen over the bound movies library, which
// is the state every request here is read against.
func playingHouse(t *testing.T) (*operator, *fakeCluster) {
	t.Helper()
	cluster := newFakeCluster()
	boundHouse(cluster)
	seedPlayer(cluster, testPlayer, testLibraryNamespace, screenController)
	return testOperator(t, cluster), cluster
}

// One request as the browser publishes it, with the paths the test names.
func filmRequest(items ...playRequestItem) []byte {
	request := playRequest{Library: testLibraryKey, Items: items}
	payload, err := json.Marshal(request)
	if err != nil {
		panic(err)
	}
	return payload
}

func film(path string) playRequestItem {
	return playRequestItem{
		Path: path,
		Presentation: &PlayPresentation{
			Type:  "video",
			Hint:  "movie",
			Title: "Some Film",
			Year:  1999,
		},
	}
}

// The same request with the slug the catalog holds for the chosen item.
func slugRequest(slug string, items ...playRequestItem) []byte {
	request := playRequest{Library: testLibraryKey, Slug: slug, Items: items}
	payload, err := json.Marshal(request)
	if err != nil {
		panic(err)
	}
	return payload
}

// The request reaches the operator the way the broker delivers it: on
// the Player's own play topic.
func publishPlay(operator *operator, payload []byte) {
	operator.handleBusMessage(
		playRequestTopic(defaultTopicBase, testLibraryNamespace, testPlayer), payload)
}

func TestAPlayRequestCreatesAPlayOnThePlayerThatAsked(t *testing.T) {
	operator, cluster := playingHouse(t)
	publishPlay(operator, filmRequest(film(testFilmPath)))

	operator.pass()

	plays := cluster.heldPlays()
	if len(plays) != 1 {
		t.Fatalf("plays = %+v, want one", plays)
	}
	play := plays[0]
	if play.APIVersion != playerAPIVersion || play.Kind != "Play" {
		t.Errorf("play = %s %s, want %s Play", play.APIVersion, play.Kind, playerAPIVersion)
	}
	if play.Metadata.Namespace != testLibraryNamespace {
		t.Errorf("namespace = %q, want %q", play.Metadata.Namespace, testLibraryNamespace)
	}
	if play.Metadata.Name != testPlayer+"-"+mintedSuffix {
		t.Errorf("name = %q, want a name minted from %q", play.Metadata.Name, testPlayer+"-")
	}
	if len(play.Spec.Players) != 1 || play.Spec.Players[0] != testPlayer {
		t.Errorf("players = %v, want [%s]", play.Spec.Players, testPlayer)
	}
	if len(play.Spec.Items) != 1 || play.Spec.Items[0].URI != testFilmClaimed {
		t.Errorf("items = %+v, want one at %q", play.Spec.Items, testFilmClaimed)
	}
}

func TestAPlayCarriesThePresentationTheBrowserResolved(t *testing.T) {
	operator, cluster := playingHouse(t)
	publishPlay(operator, filmRequest(film(testFilmPath)))

	operator.pass()

	presentation := cluster.heldPlays()[0].Spec.Items[0].Presentation
	want := PlayPresentation{Type: "video", Hint: "movie", Title: "Some Film", Year: 1999}
	if presentation == nil || *presentation != want {
		t.Errorf("presentation = %+v, want %+v", presentation, want)
	}
}

func TestTheArtAndTheTrickplayAreStampedOntoTheSameClaim(t *testing.T) {
	operator, cluster := playingHouse(t)
	item := film(testFilmPath)
	item.Presentation.Art = testPosterPath
	item.Presentation.Trickplay = testTrickplay
	publishPlay(operator, filmRequest(item))

	operator.pass()

	presentation := cluster.heldPlays()[0].Spec.Items[0].Presentation
	if presentation.Art != "claim://movies//movies/"+testPosterPath {
		t.Errorf("art = %q", presentation.Art)
	}
	if presentation.Trickplay != "claim://movies//movies/"+testTrickplay {
		t.Errorf("trickplay = %q", presentation.Trickplay)
	}
}

func TestAnItemWithNoArtCarriesNone(t *testing.T) {
	operator, cluster := playingHouse(t)
	publishPlay(operator, filmRequest(film(testFilmPath)))

	operator.pass()

	presentation := cluster.heldPlays()[0].Spec.Items[0].Presentation
	if presentation.Art != "" || presentation.Trickplay != "" {
		t.Errorf("art = %q and trickplay = %q, want both empty",
			presentation.Art, presentation.Trickplay)
	}
}

func TestAnEpisodeRequestKeepsTheOrderTheBrowserResolved(t *testing.T) {
	operator, cluster := playingHouse(t)
	publishPlay(operator, filmRequest(
		film("Show/S01E02.mkv"), film("Show/S01E03.mkv"), film("Show/S01E04.mkv")))

	operator.pass()

	items := cluster.heldPlays()[0].Spec.Items
	want := []string{
		"claim://movies//movies/Show/S01E02.mkv",
		"claim://movies//movies/Show/S01E03.mkv",
		"claim://movies//movies/Show/S01E04.mkv",
	}
	if len(items) != len(want) {
		t.Fatalf("items = %+v, want %d", items, len(want))
	}
	for index, uri := range want {
		if items[index].URI != uri {
			t.Errorf("item %d = %q, want %q", index, items[index].URI, uri)
		}
	}
}

// A library with no root of its own puts the file at the top of the
// claim, so the reference carries no empty level.
func TestALibraryWithNoRootStampsThePathAlone(t *testing.T) {
	operator, cluster := playingHouse(t)
	cluster.libraries["movies"].Spec.Storage.Root = ""
	publishPlay(operator, filmRequest(film(testFilmPath)))

	operator.pass()

	if uri := cluster.heldPlays()[0].Spec.Items[0].URI; uri != "claim://movies/"+testFilmPath {
		t.Errorf("uri = %q, want %q", uri, "claim://movies/"+testFilmPath)
	}
}

func TestAPassLeavesNoRequestBehindIt(t *testing.T) {
	operator, _ := playingHouse(t)
	publishPlay(operator, filmRequest(film(testFilmPath)))

	operator.pass()

	if held := operator.plays.take(); len(held) != 0 {
		t.Errorf("the queue holds %+v, want nothing", held)
	}
}

// Every request that names something the screen may not reach. Each one
// creates no Play, and the reason is the line the pod log carries.
func TestARequestTheOperatorRefusesCreatesNoPlay(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(cluster *fakeCluster)
		payload []byte
	}{
		{
			name:    "a player this operator does not serve",
			arrange: func(c *fakeCluster) { c.players[testPlayer].Status.Idle = nil },
			payload: filmRequest(film(testFilmPath)),
		},
		{
			name: "a player another controller draws",
			arrange: func(c *fakeCluster) {
				c.players[testPlayer].Status.Idle.Controller = "someone.example/other"
			},
			payload: filmRequest(film(testFilmPath)),
		},
		{
			name:    "a player of another name",
			arrange: func(c *fakeCluster) { delete(c.players, testPlayer) },
			payload: filmRequest(film(testFilmPath)),
		},
		{
			name:    "a library of another namespace",
			payload: []byte(`{"library":"studio/series","items":[{"path":"a.mkv"}]}`),
		},
		{
			name:    "a library the namespace does not hold",
			payload: []byte(`{"library":"house/photos","items":[{"path":"a.mkv"}]}`),
		},
		{
			name:    "a library key with no namespace in it",
			payload: []byte(`{"library":"movies","items":[{"path":"a.mkv"}]}`),
		},
		{
			name:    "a library that names no claim",
			arrange: func(c *fakeCluster) { c.libraries["movies"].Spec.Storage.Claim = "" },
			payload: filmRequest(film(testFilmPath)),
		},
		{
			name:    "a path that climbs above the root",
			payload: filmRequest(film("../elsewhere/private.mkv")),
		},
		{
			name:    "a path that is absolute",
			payload: filmRequest(film("/etc/shadow")),
		},
		{
			name:    "a path that is empty",
			payload: filmRequest(film("")),
		},
		{
			name:    "an item list with nothing in it",
			payload: filmRequest(),
		},
		{
			name:    "a payload that does not decode",
			payload: []byte("play it"),
		},
		{
			name:    "a payload with nothing in it",
			payload: nil,
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			operator, cluster := playingHouse(t)
			boundStudio(cluster)
			if each.arrange != nil {
				each.arrange(cluster)
			}
			publishPlay(operator, each.payload)

			operator.pass()

			if plays := cluster.heldPlays(); len(plays) != 0 {
				t.Errorf("plays = %+v, want none", plays)
			}
		})
	}
}

// The art and the trickplay take the same rule as the file's own path,
// so a request cannot reach a poster outside the library either.
func TestAnArtPathOutsideTheLibraryRefusesTheWholeRequest(t *testing.T) {
	operator, cluster := playingHouse(t)
	item := film(testFilmPath)
	item.Presentation.Art = "../../etc/shadow"
	publishPlay(operator, filmRequest(film(testFilmPath), item))

	operator.pass()

	if plays := cluster.heldPlays(); len(plays) != 0 {
		t.Errorf("plays = %+v, want none", plays)
	}
}

func TestATrickplayPathOutsideTheLibraryRefusesTheWholeRequest(t *testing.T) {
	operator, cluster := playingHouse(t)
	item := film(testFilmPath)
	item.Presentation.Trickplay = "/var/run/secrets"
	publishPlay(operator, filmRequest(item))

	operator.pass()

	if plays := cluster.heldPlays(); len(plays) != 0 {
		t.Errorf("plays = %+v, want none", plays)
	}
}

// A payload cannot name a Player of its own: the topic is what says
// which Player asked, and a request on one Player's topic reaches that
// Player alone.
func TestARequestOnAnotherPlayersTopicNamesThatPlayer(t *testing.T) {
	operator, cluster := playingHouse(t)
	seedPlayer(cluster, "kitchen", testLibraryNamespace, screenController)
	operator.handleBusMessage(
		playRequestTopic(defaultTopicBase, testLibraryNamespace, "kitchen"),
		filmRequest(film(testFilmPath)))

	operator.pass()

	if players := cluster.heldPlays()[0].Spec.Players; len(players) != 1 || players[0] != "kitchen" {
		t.Errorf("players = %v, want [kitchen]", players)
	}
}

// A create the API server refuses is reported and the pass carries on,
// and the request is gone, because a person who saw nothing start
// presses again.
func TestAPlayTheAPIServerRefusesLeavesNothingBehind(t *testing.T) {
	operator, cluster := playingHouse(t)
	cluster.refuseCreate = true
	publishPlay(operator, filmRequest(film(testFilmPath)))

	operator.pass()

	if plays := cluster.heldPlays(); len(plays) != 0 {
		t.Errorf("plays = %+v, want none", plays)
	}
	if held := operator.plays.take(); len(held) != 0 {
		t.Errorf("the queue holds %+v, want nothing", held)
	}
}

// A message on a topic this operator does not read reaches neither the
// report desk nor the play queue.
func TestATopicThatIsNeitherAReportNorAPlayRequestHoldsNothing(t *testing.T) {
	operator, _ := playingHouse(t)

	operator.handleBusMessage("liken/media/players/house/den-tv/commands",
		filmRequest(film(testFilmPath)))

	if held := operator.plays.take(); len(held) != 0 {
		t.Errorf("the queue holds %+v, want nothing", held)
	}
}

// The operator's bus, on a broker the test reads. TestOperator builds
// the bus and never runs it, so the filters it remembers reach a broker
// only here.
func operatorsBroker(t *testing.T, operator *operator) *fakeBroker {
	t.Helper()
	shorterBackoff(t)
	near, far := net.Pipe()
	t.Cleanup(func() {
		near.Close()
		far.Close()
	})
	broker := newFakeBroker(far)
	conns := make(chan net.Conn, 1)
	conns <- near
	operator.bus.dial = func(ctx context.Context) (net.Conn, error) {
		select {
		case conn := <-conns:
			return conn, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	go operator.bus.Run(testRunContext(t))
	return broker
}

func TestTheOperatorSubscribesToEveryPlayersPlayTopic(t *testing.T) {
	operator, _ := playingHouse(t)
	broker := operatorsBroker(t, operator)

	filters := map[string]bool{}
	for range 3 {
		filters[waitForString(t, broker.subs)] = true
	}

	if !filters[playRequestFilter(defaultTopicBase)] {
		t.Errorf("filters = %v, want %q among them",
			filters, playRequestFilter(defaultTopicBase))
	}
}

// A request whose item carries no presentation still plays: the file is
// what a Play needs, and the words beside it are what the catalog held.
func TestAnItemWithNoPresentationCarriesNone(t *testing.T) {
	operator, cluster := playingHouse(t)
	publishPlay(operator, filmRequest(playRequestItem{Path: testFilmPath}))

	operator.pass()

	item := cluster.heldPlays()[0].Spec.Items[0]
	if item.URI != testFilmClaimed || item.Presentation != nil {
		t.Errorf("item = %+v, want %q with no presentation", item, testFilmClaimed)
	}
}

func TestAPlayCarriesTheChosenItemsSlugInItsName(t *testing.T) {
	operator, cluster := playingHouse(t)
	publishPlay(operator, slugRequest("some-film-1999", film(testFilmPath)))

	operator.pass()

	name := cluster.heldPlays()[0].Metadata.Name
	if name != testPlayer+"-some-film-1999-"+mintedSuffix {
		t.Errorf("name = %q, want the player, the slug, and the minted suffix", name)
	}
}

// The catalog builds the slug, but it arrives over the bus, so the
// operator folds it to what a name may hold.
func TestTheSlugFoldsToALabelFragment(t *testing.T) {
	cases := []struct {
		name string
		slug string
		want string
	}{
		{"a slug the catalog built passes through", "some-film-1999", "some-film-1999"},
		{"a title folds to lowercase and hyphens", "Some Film (1999)", "some-film-1999"},
		{"a run of separators becomes one hyphen", "a  --  b", "a-b"},
		{"the edges carry no hyphen", "--some film--", "some-film"},
		{"a letter this fold does not name becomes a hyphen", "séance", "s-ance"},
		{"a slug of nothing but separators folds to nothing", "--- ---", ""},
		{"an empty slug folds to nothing", "", ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := labelFragment(testCase.slug); got != testCase.want {
				t.Errorf("fold = %q, want %q", got, testCase.want)
			}
		})
	}
}

// The name has a budget, and a fragment longer than it ends on a whole
// word where one is in reach.
func TestALongSlugIsCutToTheBudget(t *testing.T) {
	cases := []struct {
		name     string
		fragment string
		budget   int
		want     string
	}{
		{"a fragment inside the budget is itself", "some-film-1999", 42, "some-film-1999"},
		{"a fragment at the budget is itself", "abcde", 5, "abcde"},
		{"the cut falls back to the last hyphen", "some-film-of-1999", 14, "some-film-of"},
		{"a fragment with no hyphen in reach is cut hard", "abcdefghij", 4, "abcd"},
		{"a budget of nothing leaves no fragment", "some-film", 0, ""},
		{"a player longer than the budget leaves no fragment", "some-film", -3, ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := capped(testCase.fragment, testCase.budget); got != testCase.want {
				t.Errorf("cap = %q, want %q", got, testCase.want)
			}
		})
	}
}

// The whole name fits inside the budget, whatever the slug holds, so
// the API server's own suffix lands inside a label.
func TestAPlayNameFitsTheBudget(t *testing.T) {
	long := "the-very-long-title-of-a-film-nobody-has-heard-of-1999"

	name := playGenerateName(testPlayer, long)

	if len(name) > playNameBudget {
		t.Errorf("name = %q, %d bytes, want at most %d", name, len(name), playNameBudget)
	}
	if name != testPlayer+"-the-very-long-title-of-a-film-nobody-has-" {
		t.Errorf("name = %q, want the cut at a hyphen", name)
	}
}

// A request that names no slug, and one whose slug folds to nothing,
// name the Player alone, because a name is worth less than a Play that
// starts.
func TestAPlayWithNoUsableSlugNamesThePlayerAlone(t *testing.T) {
	if got := playGenerateName(testPlayer, ""); got != testPlayer+"-" {
		t.Errorf("name = %q, want %q", got, testPlayer+"-")
	}
	if got := playGenerateName(testPlayer, "((( )))"); got != testPlayer+"-" {
		t.Errorf("name = %q, want %q", got, testPlayer+"-")
	}
}
