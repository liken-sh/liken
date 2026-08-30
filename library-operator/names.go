package main

// names.go reads a title, a year, and a season and episode off a folder or file
// name in the *arr form, for a folder or file with no sidecar. The token list is
// fixed, so a re-walk of the same volume reads the same names every time. It
// also reads a file's technical attributes off its name where a sidecar carried
// none, and discovers the art and the trickplay directory a folder holds.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// videoExtensions is the fixed set the scanner treats as a video file. The set
// is closed, so a re-walk counts the same files, and a subtitle or an image
// beside the video is never mistaken for one.
var videoExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true,
	".webm": true, ".ts": true, ".m2ts": true, ".wmv": true, ".mpg": true,
	".mpeg": true, ".flv": true,
}

// releaseTokens are the words a *arr release name cuts the title at: a source, a
// codec, or an audio format. The title is everything before the first of these,
// so the set is fixed and a new word here changes every re-walk the same way.
var releaseTokens = map[string]bool{
	"bluray": true, "brrip": true, "bdrip": true, "webrip": true, "web": true,
	"webdl": true, "hdtv": true, "dvdrip": true, "dvd": true, "remux": true,
	"hdrip": true, "cam": true, "hdcam": true, "bdremux": true, "uhd": true,
	"x264": true, "x265": true, "h264": true, "h265": true, "hevc": true,
	"avc": true, "xvid": true, "divx": true, "av1": true,
	"dts": true, "ac3": true, "aac": true, "ddp": true, "truehd": true,
	"atmos": true, "flac": true, "eac3": true,
	"hdr": true, "hdr10": true, "dv": true, "10bit": true, "8bit": true,
	"proper": true, "repack": true, "extended": true, "remastered": true,
	"imax": true, "unrated": true,
}

// releaseCodecPrefixes are the codec and source words a group tag follows with a
// dash, so x264-GROUP reads as a release token and not as part of the title.
var releaseCodecPrefixes = map[string]bool{
	"x264": true, "x265": true, "h264": true, "h265": true, "hevc": true,
	"web": true, "webdl": true, "bluray": true, "bdrip": true, "hdtv": true,
}

var (
	// yearInParens reads the year off a Title (Year) folder.
	yearInParens = regexp.MustCompile(`\((\d{4})\)`)
	// resolutionToken reads a 720p or 1080i token.
	resolutionToken = regexp.MustCompile(`^\d{3,4}[pi]$`)
	// seasonFolder reads the number off a Season 02 folder.
	seasonFolder = regexp.MustCompile(`(?i)^season\s*0*(\d+)$`)
	// episodeMarker reads the season and episode off an s02e05 or 2x05 name,
	// wherever it sits.
	episodeMarker = regexp.MustCompile(`(?i)s(\d{1,3})[ ._-]?e(\d{1,3})|(\d{1,2})x(\d{1,3})`)
)

// parseReleaseName reads a title and a year off a folder or file name in the
// *arr form. A Title (Year) name takes the parenthesized year; a dotted release
// name is cut at its first release token and reads a trailing year before the
// cut. The year is 0 where the name carries none, the signal the walk counts as
// unidentified.
func parseReleaseName(name string) (string, int) {
	name = strings.TrimSpace(stripExtension(name))
	if match := yearInParens.FindStringIndex(name); match != nil {
		year, _ := strconv.Atoi(name[match[0]+1 : match[1]-1])
		return cleanTitle(name[:match[0]]), year
	}
	tokens := splitTokens(name)
	cut := len(tokens)
	for i, token := range tokens {
		if isReleaseToken(token) {
			cut = i
			break
		}
	}
	year, yearIndex := 0, -1
	for i := 0; i < cut; i++ {
		if value, ok := releaseYear(tokens[i]); ok {
			year, yearIndex = value, i
		}
	}
	end := cut
	if yearIndex >= 0 {
		end = yearIndex
	}
	return cleanTitle(strings.Join(tokens[:end], " ")), year
}

// splitTokens splits a name on the separators a release name uses, so a dotted,
// spaced, or underscored name reads the same. A dash stays inside a token,
// because a title like Wall-E keeps it.
func splitTokens(name string) []string {
	return strings.FieldsFunc(name, func(r rune) bool {
		return r == '.' || r == '_' || r == ' '
	})
}

// isReleaseToken reports whether a token starts the release part of a name: a
// resolution, a fixed source or codec word, or a codec followed by a group tag.
func isReleaseToken(token string) bool {
	lower := strings.ToLower(token)
	if releaseTokens[lower] || resolutionToken.MatchString(lower) {
		return true
	}
	if prefix, _, found := strings.Cut(lower, "-"); found {
		return releaseCodecPrefixes[prefix]
	}
	return false
}

// releaseYear reads a plausible release year, 1900 through 2099, off a token, so
// a four-digit part of a title is not mistaken for one.
func releaseYear(token string) (int, bool) {
	if len(token) != 4 {
		return 0, false
	}
	year, err := strconv.Atoi(token)
	if err != nil || year < 1900 || year > 2099 {
		return 0, false
	}
	return year, true
}

// cleanTitle collapses the runs of whitespace a split leaves and trims the ends,
// so a title reads as a person wrote it.
func cleanTitle(title string) string {
	return strings.Join(strings.Fields(title), " ")
}

// stripExtension drops a known video extension, so a file name parses as its
// title. It leaves a folder name, which has no extension, alone.
func stripExtension(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if videoExtensions[ext] {
		return name[:len(name)-len(ext)]
	}
	return name
}

// parseSeasonFolder reads a season number off a folder name. Specials is season
// zero, the number Jellyfin and Kodi both give it.
func parseSeasonFolder(name string) (int, bool) {
	if strings.EqualFold(strings.TrimSpace(name), "specials") {
		return 0, true
	}
	if match := seasonFolder.FindStringSubmatch(name); match != nil {
		season, _ := strconv.Atoi(match[1])
		return season, true
	}
	return 0, false
}

// parseEpisodeMarker reads a season and an episode off a name in the s02e05 or
// 2x05 form, wherever the marker sits.
func parseEpisodeMarker(name string) (season, episode int, ok bool) {
	match := episodeMarker.FindStringSubmatch(name)
	if match == nil {
		return 0, 0, false
	}
	if match[1] != "" {
		season, _ = strconv.Atoi(match[1])
		episode, _ = strconv.Atoi(match[2])
		return season, episode, true
	}
	season, _ = strconv.Atoi(match[3])
	episode, _ = strconv.Atoi(match[4])
	return season, episode, true
}

// resolutionFromName reads a file's resolution off a token in its name, the
// width and height of the standard 16:9 frame that token names. It is the
// resolution a sidecar's streamdetails would state, read from the name where the
// sidecar carried none.
func resolutionFromName(name string) (int, int) {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "2160p"), strings.Contains(lower, "4k"):
		return 3840, 2160
	case strings.Contains(lower, "1080p"), strings.Contains(lower, "1080i"):
		return 1920, 1080
	case strings.Contains(lower, "720p"):
		return 1280, 720
	case strings.Contains(lower, "480p"):
		return 854, 480
	}
	return 0, 0
}

// containerFromExtension reads a file's container off its extension, so a .mkv
// reads as mkv.
func containerFromExtension(name string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
}

// fileAttributes reads a file's technical attributes: the container off the
// extension, the resolution off the name, and the codecs, the resolution, and
// the duration off the sidecar's streamdetails where one was present. The
// sidecar wins over the name, because it read the file itself. There is no media
// probe: the scanner image carries none.
func fileAttributes(name string, stream *streamInfo) (container, videoCodec, audioCodec string, width, height int, durationMs int64) {
	container = containerFromExtension(name)
	width, height = resolutionFromName(name)
	if stream != nil {
		if stream.Width > 0 {
			width = stream.Width
		}
		if stream.Height > 0 {
			height = stream.Height
		}
		videoCodec = stream.VideoCodec
		audioCodec = stream.AudioCodec
		durationMs = stream.DurationMs
	}
	return container, videoCodec, audioCodec, width, height, durationMs
}

// posterNames, backdropNames, and logoNames are the art a folder holds, each in
// the order the scanner prefers. folder.jpg is the poster Jellyfin writes, and
// the first name that exists is the one recorded.
var (
	posterNames   = []string{"folder.jpg", "poster.jpg", "folder.png", "poster.png", "cover.jpg"}
	backdropNames = []string{"backdrop.jpg", "fanart.jpg", "backdrop.png", "fanart.png"}
	logoNames     = []string{"logo.png", "clearlogo.png", "logo.jpg"}
)

// discoverArt reads the art beside a title. The primary is the poster, the art
// the item row carries; the full list is the poster, the backdrop, and the logo,
// in that order, for the body. Every path is relative to the library root, so
// the display draws the art from the volume and the scanner copies nothing.
func discoverArt(root, dir string) (string, []string) {
	var primary string
	var all []string
	for group, names := range [][]string{posterNames, backdropNames, logoNames} {
		for _, name := range names {
			if !fileExists(filepath.Join(dir, name)) {
				continue
			}
			relative := relativePath(root, filepath.Join(dir, name))
			if group == 0 {
				primary = relative
			}
			all = append(all, relative)
			break
		}
	}
	return primary, all
}

// listVideoFiles reads a directory's video files in name order, so a re-walk
// reads a folder's files the same way and the first file is a fixed choice.
func listVideoFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if videoExtensions[strings.ToLower(filepath.Ext(entry.Name()))] {
			names = append(names, entry.Name())
		}
	}
	return names
}

// trickplayFor reads the path of a file's .trickplay directory, the thumbnail
// tiles Jellyfin writes beside the file under the file's own base name. The path
// is relative to the library root, and it is empty where the directory does not
// exist.
func trickplayFor(root, dir, file string) string {
	base := strings.TrimSuffix(file, filepath.Ext(file))
	candidate := filepath.Join(dir, base+".trickplay")
	if dirExists(candidate) {
		return relativePath(root, candidate)
	}
	return ""
}

// episodeThumb reads the path of an episode's thumbnail, which Jellyfin writes
// beside the file as a -thumb.jpg.
func episodeThumb(root, dir, file string) string {
	base := strings.TrimSuffix(file, filepath.Ext(file))
	for _, suffix := range []string{"-thumb.jpg", "-thumb.png", ".jpg", ".png"} {
		candidate := filepath.Join(dir, base+suffix)
		if fileExists(candidate) {
			return relativePath(root, candidate)
		}
	}
	return ""
}

// folderKey is the slug of a folder's own name, the key a title with no provider
// id rests its id on. It is stable for a folder that does not move, which is the
// weak case the scanner accepts for a sidecar-less title.
func folderKey(name string) string {
	return slug(name, 0)
}

// relativePath reports a path relative to the library root, the form every row
// stores so the catalog reads the same whatever mount the volume takes. A path
// that does not sit under the root passes through.
func relativePath(root, path string) string {
	if relative, err := filepath.Rel(root, path); err == nil {
		return relative
	}
	return path
}

// fileExists reports whether a path is a file that exists.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// dirExists reports whether a path is a directory that exists.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// pathExists reports whether a path exists at all, the check the webhook
// resolver makes as it maps a payload path onto the volume.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// fileSize reads a file's size in bytes, or 0 where the file cannot be read.
func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// addedTime reads the time a path was last written, in Unix seconds, the value
// the item's added column carries. It is stable across a re-walk, so a re-walk
// does not rewrite the column.
func addedTime(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.ModTime().Unix()
}
