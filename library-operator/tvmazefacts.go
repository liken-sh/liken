package main

// What the nfo facts make of TVmaze. TVmaze holds series alone and takes no
// account. The lookup keys on the IMDb id or the TheTVDB id the identity fact
// wrote, the show it answers carries the overview, and one more call carries
// the cast. A movie library's title is no answer.

import (
	"context"
	"html"
	"regexp"
	"slices"
	"strings"
)

// TVmaze states the summary as HTML. A paragraph and a line break end a line,
// every other tag goes, and the entities read as the characters a person
// wrote.
var (
	tvmazeLineBreaks = strings.NewReplacer(
		"</p>", "\n", "<br>", "\n", "<br/>", "\n", "<br />", "\n")
	tvmazeTag = regexp.MustCompile(`<[^>]*>`)
)

func tvmazePlot(summary string) string {
	text := html.UnescapeString(tvmazeTag.ReplaceAllString(tvmazeLineBreaks.Replace(summary), ""))
	var paragraphs []string
	for _, paragraph := range strings.Split(text, "\n") {
		if paragraph = strings.TrimSpace(paragraph); paragraph != "" {
			paragraphs = append(paragraphs, paragraph)
		}
	}
	return strings.Join(paragraphs, "\n\n")
}

// Who broadcast the show is the studio the sidecar carries. A show on a
// streaming service names a web channel where a broadcaster would be.
func tvmazeStudios(show tvmazeShow) []string {
	for _, source := range []*tvmazeNetwork{show.Network, show.WebChannel} {
		if source == nil {
			continue
		}
		if name := strings.TrimSpace(source.Name); name != "" {
			return []string{name}
		}
	}
	return nil
}

// TVmaze states the runtime of an episode twice: the length of a slot and the
// length the episodes average.
func tvmazeRuntimeMinutes(show tvmazeShow) int {
	if show.Runtime > 0 {
		return show.Runtime
	}
	return show.AverageRuntime
}

// The cast in the order TVmaze holds it, which is the billing order the
// sidecar carries, with the character each person plays and the picture
// TVmaze holds of them.
func tvmazeCast(members []tvmazeCastMember) []creditedActor {
	cast := make([]creditedActor, 0, len(members))
	for at, member := range members {
		cast = append(cast, creditedActor{
			Name:  strings.TrimSpace(member.Person.Name),
			Role:  strings.TrimSpace(member.Character.Name),
			Order: at,
			Thumb: strings.TrimSpace(member.Person.Image.Original),
		})
	}
	return cast
}

// The TVmaze answerer. It needs no key, and it answers nothing for a movie
// library, because TVmaze holds series alone. The show each id answered is
// held for the life of the container, so the facts of one title cost one
// lookup, and an id TVmaze does not hold is held as no show at all.
type tvmazeAnswerer struct {
	client *tvmazeClient
	shows  map[string]*tvmazeShow
}

func newTVmazeAnswerer(client *tvmazeClient) tvmazeAnswerer {
	return tvmazeAnswerer{client: client, shows: map[string]*tvmazeShow{}}
}

func (a tvmazeAnswerer) providerBlock() string { return providerBlockTVmaze }

// The facts this answerer asks TVmaze for. The table's row also names the
// identity and the art the show carries; the identity ladder does not ask
// TVmaze yet, and the art facts ask it through their own answerer.
var tvmazeAnsweredFacts = []string{factOverview, factCredits}

func (a tvmazeAnswerer) serves(fact string) bool {
	return slices.Contains(tvmazeAnsweredFacts, fact)
}

func (a tvmazeAnswerer) answer(ctx context.Context, fact string, title titleRef) (factAnswer, bool, error) {
	if !a.serves(fact) || title.kind != libraryKindSeries {
		return factAnswer{}, false, nil
	}
	show, err := a.show(ctx, title.ids)
	if err != nil || show == nil {
		return factAnswer{}, false, err
	}
	if fact == factCredits {
		return a.credits(ctx, show.ID)
	}
	answer := factAnswer{
		Plot:           tvmazePlot(show.Summary),
		Genres:         show.Genres,
		Studios:        tvmazeStudios(*show),
		Premiered:      strings.TrimSpace(show.Premiered),
		RuntimeMinutes: tvmazeRuntimeMinutes(*show),
	}
	return answer, answersFact(factOverview, answer), nil
}

// Which id the lookup keys on. TVmaze answers on an IMDb id or a TheTVDB id.
// The IMDb id is asked first because every provider states one. A title with
// neither id, and an id TVmaze does not hold, are both no answer.
func (a tvmazeAnswerer) show(ctx context.Context, ids providerIDs) (*tvmazeShow, error) {
	for _, key := range []struct{ sidecar, scheme string }{
		{sidecar: "imdb", scheme: tvmazeSchemeIMDb},
		{sidecar: "tvdb", scheme: tvmazeSchemeTheTVDB},
	} {
		id := strings.TrimSpace(ids[key.sidecar])
		if id == "" {
			continue
		}
		show, err := a.lookup(ctx, key.scheme, id)
		if err != nil || show != nil {
			return show, err
		}
	}
	return nil, nil
}

// The one lookup an id costs. The show the container holds is read again for
// every other fact of the same title, and an id already looked up makes no
// request at all.
func (a tvmazeAnswerer) lookup(ctx context.Context, scheme, id string) (*tvmazeShow, error) {
	if held, cached := a.shows[scheme+"="+id]; cached {
		return held, nil
	}
	show, err := a.client.lookup(ctx, scheme, id)
	if err != nil {
		return nil, err
	}
	a.shows[scheme+"="+id] = show
	return show, nil
}

func (a tvmazeAnswerer) credits(ctx context.Context, id int) (factAnswer, bool, error) {
	members, err := a.client.cast(ctx, id)
	if err != nil {
		return factAnswer{}, false, err
	}
	cast := tvmazeCast(members)
	return factAnswer{Cast: cast}, len(cast) > 0, nil
}
