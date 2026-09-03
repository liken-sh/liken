package main

// The calls the nfo facts make against TMDb, and the answerer that turns them
// into answers. Each fact makes its own call, so a fact that fails leaves the
// others their answers. A title with no TMDb id is no answer and not an
// error.

import (
	"context"
	"slices"
	"strconv"
	"strings"
)

// The country whose certification the mpaa element carries. TMDb states one
// certification per country.
const tmdbCertificationCountry = "US"

// Where a TMDb image lives, and the size the profile picture of a credited
// person takes.
const tmdbProfileSize = "original"

// The host every image path hangs off, which only a test replaces.
var tmdbImageBase = "https://image.tmdb.org/t/p/"

// Why the cast is cut: a title carries a hundred credited people at TMDb, and
// the sidecar is read on every walk, so the fact writes the billed cast and
// no further.
const tmdbCastLimit = 25

// What the external ids call answers: the ids of the same title in the other
// databases, which is what makes a provider that keys on an IMDb id or a
// TheTVDB id reachable.
type tmdbExternalIDs struct {
	IMDbID string `json:"imdb_id"`
	TVDbID int    `json:"tvdb_id"`
}

// The ids as a map of the same shape the sidecar and the ledger carry, with
// an id the provider left empty dropped.
func (ids tmdbExternalIDs) providerIDs() providerIDs {
	held := providerIDs{}
	if imdb := strings.TrimSpace(ids.IMDbID); imdb != "" {
		held["imdb"] = imdb
	}
	if ids.TVDbID > 0 {
		held["tvdb"] = strconv.Itoa(ids.TVDbID)
	}
	return held
}

func (c *tmdbClient) externalIDs(ctx context.Context, kind string, id int) (tmdbExternalIDs, error) {
	var answer tmdbExternalIDs
	err := c.get(ctx, tmdbTitlePath(kind, id)+"/external_ids", nil, &answer)
	return answer, err
}

// One call answers the whole overview and the score, because TMDb states them
// together on the title itself.
type tmdbDetails struct {
	Overview     string     `json:"overview"`
	Tagline      string     `json:"tagline"`
	Genres       []tmdbName `json:"genres"`
	Companies    []tmdbName `json:"production_companies"`
	Networks     []tmdbName `json:"networks"`
	ReleaseDate  string     `json:"release_date"`
	FirstAirDate string     `json:"first_air_date"`
	Runtime      int        `json:"runtime"`
	EpisodeRun   []int      `json:"episode_run_time"`
	VoteAverage  float64    `json:"vote_average"`
	VoteCount    int        `json:"vote_count"`
}

type tmdbName struct {
	Name string `json:"name"`
}

func namesOf(held []tmdbName) []string {
	var names []string
	for _, one := range held {
		if name := strings.TrimSpace(one.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// Which field each kind states the same value in: a movie states a release
// date and one runtime, and a series states a first air date and the runtime
// of an episode.
func (d tmdbDetails) premiered() string {
	if d.ReleaseDate != "" {
		return d.ReleaseDate
	}
	return d.FirstAirDate
}

func (d tmdbDetails) runtimeMinutes() int {
	if d.Runtime > 0 {
		return d.Runtime
	}
	if len(d.EpisodeRun) > 0 {
		return d.EpisodeRun[0]
	}
	return 0
}

func (d tmdbDetails) studios() []string {
	if len(d.Companies) > 0 {
		return namesOf(d.Companies)
	}
	return namesOf(d.Networks)
}

func (c *tmdbClient) details(ctx context.Context, kind string, id int) (tmdbDetails, error) {
	var answer tmdbDetails
	err := c.get(ctx, tmdbTitlePath(kind, id), nil, &answer)
	return answer, err
}

// The two kinds answer the certification under two names: a movie states it
// beside each release date, and a series states it as the rating of a
// country.
type tmdbCertifications struct {
	Results []struct {
		Country      string `json:"iso_3166_1"`
		Rating       string `json:"rating"`
		ReleaseDates []struct {
			Certification string `json:"certification"`
		} `json:"release_dates"`
	} `json:"results"`
}

func (a tmdbCertifications) certificationOf(country string) string {
	for _, result := range a.Results {
		if result.Country != country {
			continue
		}
		if rating := strings.TrimSpace(result.Rating); rating != "" {
			return rating
		}
		for _, release := range result.ReleaseDates {
			if certification := strings.TrimSpace(release.Certification); certification != "" {
				return certification
			}
		}
	}
	return ""
}

func (c *tmdbClient) certification(ctx context.Context, kind string, id int) (string, error) {
	path := tmdbTitlePath(kind, id) + "/release_dates"
	if kind == libraryKindSeries {
		path = tmdbTitlePath(kind, id) + "/content_ratings"
	}
	var answer tmdbCertifications
	if err := c.get(ctx, path, nil, &answer); err != nil {
		return "", err
	}
	return answer.certificationOf(tmdbCertificationCountry), nil
}

// A movie states one character per credited person, and a series states the
// characters of every season together, which is why the series answer carries
// a list of roles.
type tmdbCredits struct {
	Cast []struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		Character   string `json:"character"`
		Order       int    `json:"order"`
		ProfilePath string `json:"profile_path"`
		Roles       []struct {
			Character string `json:"character"`
		} `json:"roles"`
	} `json:"cast"`
	Crew []tmdbCrewMember `json:"crew"`
}

// One crew credit. A movie states one job per credit, and a series states
// every job the person held over its seasons, which is why the jobs of a
// credit are a list.
type tmdbCrewMember struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Job        string `json:"job"`
	Department string `json:"department"`
	Jobs       []struct {
		Job string `json:"job"`
	} `json:"jobs"`
}

func (m tmdbCrewMember) jobs() []string {
	if len(m.Jobs) == 0 {
		return []string{m.Job}
	}
	held := make([]string, 0, len(m.Jobs))
	for _, one := range m.Jobs {
		held = append(held, one.Job)
	}
	return held
}

// Which crew credits the two parts take. A director is the one job of that
// name, because the directing department also holds the assistants a player
// does not name. A writer is anyone in the writing department, because a
// person credited with the screenplay, the story, or the novel wrote the
// title.
const (
	tmdbDirectorJob       = "Director"
	tmdbWritingDepartment = "Writing"
)

func (m tmdbCrewMember) directs() bool { return slices.Contains(m.jobs(), tmdbDirectorJob) }

func (m tmdbCrewMember) writes() bool { return m.Department == tmdbWritingDepartment }

// The crew of one title that one part takes, in the order TMDb states them,
// with one entry per person, because a person credited twice is one name a
// player reads once. The id tells two people of one name apart, and a person
// TMDb states no id for is told apart by the name alone.
func tmdbCrew(members []tmdbCrewMember, takes func(tmdbCrewMember) bool) []creditedPerson {
	var people []creditedPerson
	held := map[int]bool{}
	for _, member := range members {
		name := strings.TrimSpace(member.Name)
		if name == "" || !takes(member) {
			continue
		}
		if member.ID > 0 && held[member.ID] {
			continue
		}
		if member.ID <= 0 && personIndex(people, name) >= 0 {
			continue
		}
		held[member.ID] = true
		people = append(people, creditedPerson{Name: name, IDs: creditedIDs(member.ID)})
	}
	return people
}

// The people one title's credits name: the billed cast, cut to the limit, and
// the crew, which has no cut because a title names few of them.
type titleCredits struct {
	Cast      []creditedActor
	Directors []creditedPerson
	Writers   []creditedPerson
}

func (c *tmdbClient) credits(ctx context.Context, kind string, id int) (titleCredits, error) {
	path := tmdbTitlePath(kind, id) + "/credits"
	if kind == libraryKindSeries {
		path = tmdbTitlePath(kind, id) + "/aggregate_credits"
	}
	var answer tmdbCredits
	if err := c.get(ctx, path, nil, &answer); err != nil {
		return titleCredits{}, err
	}
	cast := make([]creditedActor, 0, len(answer.Cast))
	for _, member := range answer.Cast {
		role := member.Character
		if role == "" && len(member.Roles) > 0 {
			role = member.Roles[0].Character
		}
		cast = append(cast, creditedActor{
			Name:  strings.TrimSpace(member.Name),
			Role:  strings.TrimSpace(role),
			Order: member.Order,
			Thumb: tmdbImageURL(tmdbProfileSize, member.ProfilePath),
			IDs:   creditedIDs(member.ID),
		})
	}
	slices.SortStableFunc(cast, func(one, two creditedActor) int { return one.Order - two.Order })
	if len(cast) > tmdbCastLimit {
		cast = cast[:tmdbCastLimit]
	}
	return titleCredits{
		Cast:      cast,
		Directors: tmdbCrew(answer.Crew, tmdbCrewMember.directs),
		Writers:   tmdbCrew(answer.Crew, tmdbCrewMember.writes),
	}, nil
}

// The ids one credit carries. A person TMDb states no id for carries none, and
// the credits fact then keys that person on the name alone.
func creditedIDs(id int) providerIDs {
	if id <= 0 {
		return nil
	}
	return providerIDs{contributorTMDbScheme: strconv.Itoa(id)}
}

// A path the provider left empty is no picture at all.
func tmdbImageURL(size, path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return tmdbImageBase + size + path
}

func tmdbTitlePath(kind string, id int) string {
	if kind == libraryKindSeries {
		return "/3/tv/" + strconv.Itoa(id)
	}
	return "/3/movie/" + strconv.Itoa(id)
}

// The TMDb answerer: one account, asked for one fact of one title. It answers
// nothing for a title with no TMDb id, because every call it makes keys on
// that id.
type tmdbAnswerer struct {
	client *tmdbClient
}

func (a tmdbAnswerer) providerBlock() string { return providerBlockTMDb }

func (a tmdbAnswerer) serves(fact string) bool {
	return slices.Contains(providerFacts[providerBlockTMDb], fact)
}

func (a tmdbAnswerer) answer(ctx context.Context, fact string, title titleRef) (factAnswer, bool, error) {
	id, err := strconv.Atoi(title.ids["tmdb"])
	if err != nil || id <= 0 {
		return factAnswer{}, false, nil
	}
	switch fact {
	case factOverview:
		return a.overview(ctx, title.kind, id)
	case factCertification:
		return a.certification(ctx, title.kind, id)
	case factRatingTMDb:
		return a.rating(ctx, title.kind, id)
	case factCredits:
		return a.credits(ctx, title.kind, id)
	}
	return factAnswer{}, false, nil
}

func (a tmdbAnswerer) overview(ctx context.Context, kind string, id int) (factAnswer, bool, error) {
	details, err := a.client.details(ctx, kind, id)
	if err != nil {
		return factAnswer{}, false, err
	}
	answer := factAnswer{
		Plot:           strings.TrimSpace(details.Overview),
		Tagline:        strings.TrimSpace(details.Tagline),
		Genres:         namesOf(details.Genres),
		Studios:        details.studios(),
		Premiered:      strings.TrimSpace(details.premiered()),
		RuntimeMinutes: details.runtimeMinutes(),
	}
	return answer, answersFact(factOverview, answer), nil
}

func (a tmdbAnswerer) certification(ctx context.Context, kind string, id int) (factAnswer, bool, error) {
	certification, err := a.client.certification(ctx, kind, id)
	if err != nil {
		return factAnswer{}, false, err
	}
	return factAnswer{Certification: certification}, certification != "", nil
}

// A title nobody has voted on has no rating to write, so a score of zero is
// no answer.
func (a tmdbAnswerer) rating(ctx context.Context, kind string, id int) (factAnswer, bool, error) {
	details, err := a.client.details(ctx, kind, id)
	if err != nil {
		return factAnswer{}, false, err
	}
	if details.VoteAverage <= 0 {
		return factAnswer{}, false, nil
	}
	return factAnswer{Rating: &titleRating{Value: details.VoteAverage, Votes: details.VoteCount}}, true, nil
}

func (a tmdbAnswerer) credits(ctx context.Context, kind string, id int) (factAnswer, bool, error) {
	credits, err := a.client.credits(ctx, kind, id)
	if err != nil {
		return factAnswer{}, false, err
	}
	answer := factAnswer{Cast: credits.Cast, Directors: credits.Directors, Writers: credits.Writers}
	held := len(credits.Cast) > 0 || len(credits.Directors) > 0 || len(credits.Writers) > 0
	return answer, held, nil
}
