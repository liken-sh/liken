package main

// settlefacts.go decides which of a folder's attempts still describe the
// volume. The walk is the one reader that sees a folder's ledgers and its
// files in the same read, so it decides, and a fact's re-read of its folder
// after a write decides the same way through the same reader. An attempt
// that no longer describes the volume is not lifted. The sweep then removes
// its row, and the fact's gap opens. The walk still writes nothing to the
// volume, and every ledger keeps its one writer.
//
// Two cases open a gap here. A file replaced at its path takes every fact
// of the earlier file with it, and fileidentity.go says how the walk tells the
// two files apart. And a found attempt says its output is on the volume, so
// an output that is gone opens the fact that wrote it.

import (
	"slices"
	"strings"
)

// The facts whose attempt keys on a media file's path and describes that one
// file. arrival keys on the path as well, but it records when the path first
// held a video, and a new file at the path is not a new title, so a replaced
// file keeps its arrival.
var fileFacts = map[string]bool{
	factProbe: true, factTrickplay: true, factEpisodeThumb: true, factMarks: true,
	factAppearances: true,
}

// What one folder read reopened: the files replaced at their paths, and the
// found attempts whose output the read did not find.
type reopenedFacts struct {
	replaced []replacedFile
	missing  int
}

// The sum of two reads, for a walk that reads many folders.
func (r reopenedFacts) add(other reopenedFacts) reopenedFacts {
	r.replaced = append(r.replaced, other.replaced...)
	r.missing += other.missing
	return r
}

// One file whose size is not the size its probe record holds.
type replacedFile struct {
	path     string
	recorded int64
	found    int64
}

// settleFacts runs once the folder's rows and ledgers are read. The outputs
// of an earlier file leave the rows first, so the found attempts that named
// them then read as attempts with no output.
func (r *walkResult) settleFacts() {
	identities := r.identities
	r.identities = nil
	r.dropEarlierOutputs(identities)
	outputs := r.outputsOnVolume()
	earlierMarks := map[string]bool{}
	kept := r.attempts[:0]
	for _, attempt := range r.attempts {
		if fileFacts[attempt.Fact] && attempt.At < identities[attempt.Item].cutoff(attempt.Fact) {
			if attempt.Fact == factMarks {
				earlierMarks[attempt.Item] = true
			}
			continue
		}
		if attempt.Result == attemptFound && !outputs.hold(attempt) {
			r.reopened.missing++
			continue
		}
		kept = append(kept, attempt)
	}
	r.attempts = kept
	r.marks = slices.DeleteFunc(r.marks, func(mark markRow) bool {
		return earlierMarks[mark.Path] || identities[mark.Path].sizeChanged
	})
	for _, path := range sortedPaths(identities) {
		if identity := identities[path]; identity.sizeChanged {
			r.reopened.replaced = append(r.reopened.replaced,
				replacedFile{path: path, recorded: identity.recorded, found: identity.found})
		}
	}
}

func sortedPaths(identities fileIdentities) []string {
	paths := make([]string, 0, len(identities))
	for path := range identities {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}

// dropEarlierOutputs takes out of the rows the tile directories and the
// thumbnails made before a file replaced the video they belong to. The files
// stay on the volume, and the rows still list them, but the video no longer
// names those tiles and the episode no longer links to that still. The
// catalog then says what is true of the file at the path: it has no tiles and
// no still of its own. The trickplay worker and the art phase read the same
// second before they replace either one.
func (r *walkResult) dropEarlierOutputs(identities fileIdentities) {
	made := map[string]int64{}
	for _, row := range r.files {
		made[row.Path] = row.Modified
	}
	for i := range r.files {
		row := &r.files[i]
		earlier := identities[row.Path].earlier
		if row.Trickplay == "" || earlier == 0 {
			continue
		}
		if at, held := made[row.Trickplay]; held && at < earlier {
			row.Trickplay = ""
		}
	}

	earlierOf := map[string]int64{}
	for _, episode := range r.episodes {
		if earlier := identities[episode.Path].earlier; earlier > 0 {
			earlierOf[episode.Id] = earlier
		}
	}
	if len(earlierOf) == 0 {
		return
	}
	dropped := map[string]map[string]bool{}
	for i := range r.files {
		row := &r.files[i]
		if !isStill(*row) {
			continue
		}
		row.Items = slices.DeleteFunc(row.Items, func(item string) bool {
			if earlier := earlierOf[item]; earlier > 0 && row.Modified < earlier {
				if dropped[item] == nil {
					dropped[item] = map[string]bool{}
				}
				dropped[item][row.Path] = true
				return true
			}
			return false
		})
		if len(row.Items) == 0 {
			row.Items = nil
		}
	}
	for i := range r.episodes {
		episode := &r.episodes[i]
		gone := dropped[episode.Id]
		if gone[episode.Art] {
			episode.Art = ""
		}
		episode.Arts = slices.DeleteFunc(episode.Arts, func(art string) bool { return gone[art] })
		if len(episode.Arts) == 0 {
			episode.Arts = nil
		}
	}
}

// An image that stands for one episode, which is what the episode-thumb gap
// reads.
func isStill(row fileRow) bool {
	return row.Type == fileTypeImage && (row.Role == fileRoleThumb || row.Role == fileRoleStill)
}

// The outputs one folder read found, in the form each fact's found attempt
// is checked against.
type folderOutputs struct {
	files        map[string]bool
	tiles        map[string]string
	episodesOf   map[string][]string
	stills       map[string]bool
	titlePaths   map[string]string
	trailers     []string
	contributors map[string]contributorRow
}

func (r *walkResult) outputsOnVolume() folderOutputs {
	outputs := folderOutputs{
		files: map[string]bool{}, tiles: map[string]string{}, episodesOf: map[string][]string{},
		stills: map[string]bool{}, titlePaths: map[string]string{}, contributors: map[string]contributorRow{},
	}
	for _, row := range r.files {
		outputs.files[row.Path] = true
		if row.Type == fileTypeVideo {
			outputs.tiles[row.Path] = row.Trickplay
		}
		if isStill(row) {
			for _, item := range row.Items {
				outputs.stills[item] = true
			}
		}
		if row.Role == fileRoleTrailer {
			outputs.trailers = append(outputs.trailers, row.Path)
		}
	}
	for _, episode := range r.episodes {
		outputs.episodesOf[episode.Path] = append(outputs.episodesOf[episode.Path], episode.Id)
	}
	for _, movie := range r.movies {
		outputs.titlePaths[movie.Id] = movie.Path
	}
	for _, series := range r.series {
		outputs.titlePaths[series.Id] = series.Path
	}
	for _, person := range r.contributors {
		outputs.contributors[person.Path] = person
	}
	return outputs
}

// Whether the output a found attempt names is on the volume. An attempt
// whose item this read does not hold answers yes, because the read cannot
// say, and a fact whose answer is a ledger and not a file has no output to
// lose.
func (o folderOutputs) hold(attempt attemptRow) bool {
	item := attempt.Item
	switch attempt.Fact {
	case factTrickplay:
		tiles, held := o.tiles[item]
		return !held || tiles != ""
	case factEpisodeThumb:
		episodes, held := o.episodesOf[item]
		return !held || slices.ContainsFunc(episodes, func(id string) bool { return o.stills[id] })
	case factTrailerFile:
		title, held := o.titlePaths[item]
		return !held || slices.ContainsFunc(o.trailers, func(file string) bool {
			return strings.HasPrefix(file, title+"/")
		})
	case factContributorHeadshot:
		person, held := o.contributors[item]
		return !held || person.Headshot
	case factContributorBiography:
		person, held := o.contributors[item]
		return !held || person.Biography
	}
	if _, art := artTypes[attempt.Fact]; art {
		return o.files[item]
	}
	return true
}
