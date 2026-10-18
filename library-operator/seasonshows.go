package main

// seasonshows.go reads which show one season of a series is on each provider.
// Providers do not agree on what a series is. TheTVDB and IMDb keep an
// anthology such as Monster as one series of four seasons, and Sonarr names
// its files that way, while TMDb and TVmaze give each installment a show of
// its own. A series folder holds one id per provider, so a fact that asks a
// provider about one season or one episode would ask the wrong show. A person
// writes a <showid> into the season folder's season.nfo to name the show and
// its season on that provider:
//
//	<season>
//	  <showid type="tmdb" season="1">225634</showid>
//	</season>
//
// Jellyfin reads season.nfo too, and skips an element it does not know. It
// reads a season's <uniqueid> as the provider's own record of that season,
// so the mapping takes an element of its own.
//
// The facts that ask about a season or an episode read the mapping before
// they ask, and each attempt records the mapping it asked under. The walk
// lifts an attempt only while the season's mapping is still the one the
// attempt recorded, so a changed mapping opens the season's facts again.

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The file Kodi and Jellyfin read in a season folder.
const seasonNFOName = "season.nfo"

// The providers a <showid> may name: the ones whose id a fact sends with a
// season and an episode number. The names are the ones a <uniqueid> takes.
var seasonShowProviders = []string{"imdb", "tmdb", "tvdb", "tvmaze"}

// One show on one provider, and the season of that show that the folder's
// season is.
type showRef struct {
	id     string
	season int
}

// A season's mapping, keyed by provider. A provider the season does not map
// is asked as the series.
type seasonShows map[string]showRef

// The id and the season to ask one provider with. A mapped provider takes the
// show's id and the show's season, because the two go together: season 1 of
// TMDb's show is not season 1 of the series.
func (s seasonShows) on(provider, id string, season int) (string, int) {
	if show, mapped := s[provider]; mapped {
		return show.id, show.season
	}
	return id, season
}

// The mapping as one line an attempt records, in provider order, so the same
// season.nfo always gives the same line and an unmapped season gives none.
func (s seasonShows) signature() string {
	providers := make([]string, 0, len(s))
	for provider := range s {
		providers = append(providers, provider)
	}
	slices.Sort(providers)
	parts := make([]string, 0, len(providers))
	for _, provider := range providers {
		show := s[provider]
		parts = append(parts, provider+":"+show.id+":"+strconv.Itoa(show.season))
	}
	return strings.Join(parts, ",")
}

// The elements of season.nfo the mapping reads. The root element takes any
// name, because a season.nfo another tool wrote may differ.
type seasonNFO struct {
	ShowIDs []struct {
		Type   string `xml:"type,attr"`
		Season string `xml:"season,attr"`
		ID     string `xml:",chardata"`
	} `xml:"showid"`
}

// parseSeasonShows reads the <showid> elements of one season.nfo. A <showid>
// with no id, an unknown provider, or a season that is no number is skipped,
// and each skip returns one line that says why, so a person reads why the
// mapping did nothing. A file that is no XML maps nothing and returns one
// line. The first <showid> of a provider wins.
func parseSeasonShows(data []byte, folderSeason int) (seasonShows, []string) {
	shows := seasonShows{}
	var document seasonNFO
	if err := xml.Unmarshal(data, &document); err != nil {
		return shows, []string{"could not read the showid elements: " + err.Error()}
	}
	var skipped []string
	for _, element := range document.ShowIDs {
		provider := strings.ToLower(strings.TrimSpace(element.Type))
		id := strings.TrimSpace(element.ID)
		season := folderSeason
		if text := strings.TrimSpace(element.Season); text != "" {
			number, err := strconv.Atoi(text)
			if err != nil || number < 0 {
				skipped = append(skipped, fmt.Sprintf("skipped the %s showid: season %q is no season number", provider, text))
				continue
			}
			season = number
		}
		switch {
		case !slices.Contains(seasonShowProviders, provider):
			skipped = append(skipped, fmt.Sprintf("skipped the showid of type %q: no fact asks %q by season",
				element.Type, element.Type))
		case id == "":
			skipped = append(skipped, fmt.Sprintf("skipped the %s showid: it names no id", provider))
		default:
			if _, held := shows[provider]; !held {
				shows[provider] = showRef{id: id, season: season}
			}
		}
	}
	return shows, skipped
}

// readSeasonShows reads the season.nfo of one folder. A folder with no
// season.nfo maps nothing and is no error. A season.nfo the walk cannot open
// is an error, so the pass is incomplete and the sweep removes nothing.
func readSeasonShows(dir string, folderSeason int) (seasonShows, []string, error) {
	data, err := os.ReadFile(filepath.Join(dir, seasonNFOName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	shows, skipped := parseSeasonShows(data, folderSeason)
	return shows, skipped, nil
}

// The mapping of the folder that holds an episode, read beside the episode.
// A folder that is no season folder, such as a series folder that holds an
// episode of its own, maps nothing.
func folderSeasonShows(dir string) (seasonShows, error) {
	season, isSeason := parseSeasonFolder(filepath.Base(dir))
	if !isSeason {
		return nil, nil
	}
	shows, _, err := readSeasonShows(dir, season)
	return shows, err
}

// One <showid> the walk skipped: the season.nfo that holds it, relative to
// the root, and why.
type skippedShow struct {
	path   string
	reason string
}

// One season folder of a series and its mapping.
type seasonFolderShows struct {
	name    string
	season  int
	shows   seasonShows
	skipped []string
}

// seriesSeasonShows reads the mapping of every season folder of one series:
// the folders one level down whose name is a season, less the ones the
// Library ignores. A series with no season.nfo reads one directory and opens
// no file.
func seriesSeasonShows(seriesDir string, ignore ignoreSet) ([]seasonFolderShows, error) {
	entries, err := os.ReadDir(seriesDir)
	if err != nil {
		return nil, err
	}
	var folders []seasonFolderShows
	for _, entry := range entries {
		if !entry.IsDir() || skipName(entry.Name()) || ignore.skips(entry.Name()) {
			continue
		}
		season, isSeason := parseSeasonFolder(entry.Name())
		if !isSeason {
			continue
		}
		shows, skipped, err := readSeasonShows(filepath.Join(seriesDir, entry.Name()), season)
		if err != nil {
			return folders, err
		}
		folders = append(folders, seasonFolderShows{name: entry.Name(), season: season, shows: shows, skipped: skipped})
	}
	return folders, nil
}

// The mapping of one season of a series, for the season art that lands in the
// series folder. Two folders of one season, such as Season 2 and Season 02,
// take the first that maps anything.
func seasonShowsOf(folders []seasonFolderShows, season int) seasonShows {
	for _, folder := range folders {
		if folder.season == season && len(folder.shows) > 0 {
			return folder.shows
		}
	}
	return nil
}

// The mapping each of a folder's ledgers is read against. A season folder's
// own mapping holds for the facts that key on its episodes, and the series
// folder holds the season art of every season.
type folderShows struct {
	folder  string
	seasons map[int]string
}

// The facts that ask a provider about one season or one episode, so a
// changed mapping opens them.
var seasonShowFacts = map[string]bool{
	factEpisodeThumb: true, factSeasonPoster: true, factSeasonBanner: true, factMarks: true,
}

// The mapping one ledger entry was due to be asked under: the folder's own
// for an episode, and the named season's for a season's art.
func (f folderShows) of(fact, entry string) string {
	switch fact {
	case factEpisodeThumb, factMarks:
		return f.folder
	case factSeasonPoster, factSeasonBanner:
		if season, held := seasonOfArtEntry(entry); held {
			return f.seasons[season]
		}
	}
	return ""
}

// The season one season art file names, from the name seasonArtName writes.
func seasonOfArtEntry(entry string) (int, bool) {
	if strings.HasPrefix(entry, specialsSeasonPrefix) {
		return 0, true
	}
	var season int
	var rest string
	if _, err := fmt.Sscanf(entry, "season%02d-%s", &season, &rest); err != nil {
		return 0, false
	}
	return season, true
}

// The mapping of a series' folders, as the ledgers of the series folder and
// of each season folder read it.
func seriesFolderShows(folders []seasonFolderShows) (folderShows, map[string]string) {
	series := folderShows{seasons: map[int]string{}}
	byFolder := map[string]string{}
	for _, folder := range folders {
		signature := folder.shows.signature()
		byFolder[folder.name] = signature
		if _, held := series.seasons[folder.season]; !held || signature != "" {
			series.seasons[folder.season] = signature
		}
	}
	return series, byFolder
}

// The entries of one ledger whose attempt asked under a mapping that is no
// longer the season's. The walk lifts neither their attempts nor their spans,
// so the sweep removes the rows and the fact's gap opens.
func (s likenDir) reshownEntries(fact string, ledger likenLedger) map[string]bool {
	if !seasonShowFacts[fact] {
		return nil
	}
	reshown := map[string]bool{}
	for _, attempt := range ledger.Attempts {
		if attempt.Show != s.shows.of(fact, attempt.Path) {
			reshown[attempt.Path] = true
		}
	}
	return reshown
}

// The art files of one ledger that the fact itself wrote under a mapping
// that is no longer the season's. A file another tool wrote is the
// person's, and stays in the rows.
func (s likenDir) reshownOutputs(fact string, ledger likenLedger) []string {
	art, isArt := artTypes[fact]
	if !isArt || !seasonShowFacts[fact] {
		return nil
	}
	var outputs []string
	for _, item := range ledger.Items {
		if item.Written.IsZero() || item.Provider.is(artProviderExisting) ||
			item.Show == s.shows.of(fact, item.Path) {
			continue
		}
		name := item.Path
		if art.fact == factEpisodeThumb {
			name = episodeThumbName(item.Path)
		}
		outputs = append(outputs, relativePath(s.root, filepath.Join(s.dir, name)))
	}
	return outputs
}

// dropReshownOutputs takes out of the rows the art files a fact wrote under
// another mapping. The files stay on the volume until the fact replaces them,
// but no episode links to a still of another show, and the season's gap
// opens because the catalog holds no file at the art's name.
func (r *walkResult) dropReshownOutputs() {
	if len(r.reshown) == 0 {
		return
	}
	gone := map[string]bool{}
	for _, path := range r.reshown {
		gone[path] = true
	}
	r.reshown = nil
	r.files = slices.DeleteFunc(r.files, func(row fileRow) bool {
		if gone[row.Path] {
			r.reopened.reshown++
			return true
		}
		return false
	})
	for i := range r.episodes {
		episode := &r.episodes[i]
		if gone[episode.Art] {
			episode.Art = ""
		}
		episode.Arts = slices.DeleteFunc(episode.Arts, func(art string) bool { return gone[art] })
		if len(episode.Arts) == 0 {
			episode.Arts = nil
		}
	}
}

// The mapping one art gap asks under: the season folder's own for an
// episode's still, and the named season's for a season's art. Every other
// art fact asks about the whole title and maps nothing.
func (e *enricher) gapShows(fact string, gap artGap) (seasonShows, error) {
	switch fact {
	case factEpisodeThumb:
		return folderSeasonShows(filepath.Join(e.root, filepath.Dir(gap.key)))
	case factSeasonPoster, factSeasonBanner:
		folders, err := seriesSeasonShows(filepath.Join(e.root, gap.folder()), e.ignore)
		if err != nil {
			return nil, err
		}
		return seasonShowsOf(folders, gap.season), nil
	}
	return nil, nil
}

// The second this fact wrote the gap's file under another mapping, or the
// zero time where it did not: a file another tool wrote, a file this fact
// wrote under the mapping the gap asks under, or no file at all.
func (e *enricher) reshownArt(folder, fact string, gap artGap) (time.Time, error) {
	if !seasonShowFacts[fact] {
		return time.Time{}, nil
	}
	ledger, err := readLikenLedger(folder, fact)
	if err != nil {
		return time.Time{}, err
	}
	item, held := ledger.itemAt(gap.entry())
	if !held || item.Written.IsZero() || item.Provider.is(artProviderExisting) ||
		item.Show == gap.shows.signature() {
		return time.Time{}, nil
	}
	return item.Written, nil
}

// Whether the last attempt at one entry asked under a mapping other than the
// one given. An entry with no attempt asked under none.
func (l *likenLedger) askedUnderAnother(entry, show string) bool {
	for _, attempt := range l.Attempts {
		if attempt.Path == entry {
			return attempt.Show != show
		}
	}
	return false
}
