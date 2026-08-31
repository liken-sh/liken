package main

// files.go classifies every file a title folder carries: the sidecars, the
// art, the subtitles, the trickplay tiles, and the extras beside the video.
// The classification reads a file's name and the place that holds it, and
// opens no file, so a re-walk classifies a file the same way every time and a
// large library costs one stat per file.

import (
	"os"
	"path/filepath"
	"strings"
)

// The categories a file falls into. The set is closed, so the media browser
// switches on it, and a file that fits none of them is other rather than a new
// word.
const (
	fileTypeVideo     = "video"
	fileTypeAudio     = "audio"
	fileTypeSubtitle  = "subtitle"
	fileTypeImage     = "image"
	fileTypeMetadata  = "metadata"
	fileTypeTrickplay = "trickplay"
	fileTypeOther     = "other"
)

// The roles, which say which one of its kind a file is. The words are
// Jellyfin's and Kodi's, because those are the tools that wrote the files.
const (
	fileRolePrimary    = "primary"
	fileRoleTrailer    = "trailer"
	fileRoleExtra      = "extra"
	fileRoleTheme      = "theme"
	fileRoleSample     = "sample"
	fileRoleTrack      = "track"
	fileRoleFull       = "full"
	fileRoleForced     = "forced"
	fileRoleSDH        = "sdh"
	fileRolePoster     = "poster"
	fileRoleBackdrop   = "backdrop"
	fileRoleLogo       = "logo"
	fileRoleBanner     = "banner"
	fileRoleThumb      = "thumb"
	fileRoleDisc       = "disc"
	fileRoleClearart   = "clearart"
	fileRoleStill      = "still"
	fileRoleMovie      = "movie"
	fileRoleTVShow     = "tvshow"
	fileRoleEpisode    = "episode"
	fileRoleSeason     = "season"
	fileRoleCollection = "collection"
	fileRoleTiles      = "tiles"
)

// The extensions that decide a category, beside the video extensions in
// names.go. Each set is closed, so a re-walk reads the same category off the
// same name.
var (
	audioExtensions = map[string]bool{
		".mp3": true, ".flac": true, ".m4a": true, ".m4b": true, ".aac": true,
		".ogg": true, ".oga": true, ".opus": true, ".wav": true, ".wma": true,
		".ape": true, ".aiff": true, ".alac": true,
	}
	subtitleExtensions = map[string]bool{
		".srt": true, ".ass": true, ".ssa": true, ".sub": true, ".idx": true,
		".vtt": true, ".sup": true, ".smi": true, ".sbv": true, ".ttml": true,
	}
	imageExtensions = map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".bmp": true,
		".gif": true, ".tbn": true, ".avif": true,
	}
)

// metadataExtension is the sidecar Jellyfin, Kodi, and the *arr tools write.
const metadataExtension = ".nfo"

// trickplayExtension names the directory of thumbnail tiles Jellyfin writes
// beside a video file.
const trickplayExtension = ".trickplay"

// The files a desktop or a storage appliance leaves behind. They belong to no
// title, and the walk leaves them out with the dotfiles.
var junkNames = map[string]bool{"thumbs.db": true, "desktop.ini": true}

// skipName reports whether the walk leaves an entry out: a name that starts
// with a dot, and the junk names above.
func skipName(name string) bool {
	return strings.HasPrefix(name, ".") || junkNames[strings.ToLower(name)]
}

// The extras folders Jellyfin writes beside a feature, and the one of them
// whose videos are trailers rather than ordinary extras. The set is fixed,
// because a folder the walk does not name here is a folder it does not read.
const extrasTrailers = "trailers"

var extrasFolderNames = map[string]bool{
	"extras": true, "featurettes": true, extrasTrailers: true,
	"behind the scenes": true, "deleted scenes": true, "interviews": true,
	"scenes": true, "shorts": true, "clips": true, "other": true,
}

// extrasFolderName reads the extras-folder name a directory carries, lowercased,
// or the empty string where the directory is not one.
func extrasFolderName(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	if extrasFolderNames[lower] {
		return lower
	}
	return ""
}

// filePlace is what a role depends on beyond the file's own name: the
// library's kind, whether the directory is a season folder, and the extras
// folder that holds the file.
type filePlace struct {
	kind   string
	season bool
	extras string
}

// fileClass is what one file is: its category, which one of its kind it is, and
// the language its name carries.
type fileClass struct {
	Type     string
	Role     string
	Language string
}

// classifyFile reads a file's category off its extension, its role off its name
// and its place, and its language off its name. It opens nothing.
func classifyFile(name string, place filePlace) fileClass {
	category := fileTypeOf(name)
	class := fileClass{Type: category, Role: fileRoleOf(category, name, place)}
	if category == fileTypeSubtitle || category == fileTypeAudio {
		class.Language = fileLanguage(name)
	}
	return class
}

// fileTypeOf reads a file's category off its extension.
func fileTypeOf(name string) string {
	extension := strings.ToLower(filepath.Ext(name))
	switch {
	case videoExtensions[extension]:
		return fileTypeVideo
	case audioExtensions[extension]:
		return fileTypeAudio
	case subtitleExtensions[extension]:
		return fileTypeSubtitle
	case imageExtensions[extension]:
		return fileTypeImage
	case extension == metadataExtension:
		return fileTypeMetadata
	default:
		return fileTypeOther
	}
}

// fileRoleOf reads which one of its kind a file is. A file in no category has
// no role.
func fileRoleOf(category, name string, place filePlace) string {
	base := strings.ToLower(stripAnyExtension(name))
	switch category {
	case fileTypeVideo:
		return videoRole(base, place)
	case fileTypeAudio:
		return audioRole(base)
	case fileTypeSubtitle:
		return subtitleRole(base)
	case fileTypeImage:
		return imageRole(base, place)
	case fileTypeMetadata:
		return metadataRole(base, place)
	case fileTypeTrickplay:
		return fileRoleTiles
	}
	return ""
}

// The marks Jellyfin appends to an extra's file name. Each one is the last
// token of the base name, as in The Matrix (1999)-featurette.mkv, which is how
// an extra beside the feature says what it is.
var extraMarks = map[string]bool{
	"behindthescenes": true, "deleted": true, "deletedscene": true,
	"deletedscenes": true, "featurette": true, "featurettes": true,
	"interview": true, "scene": true, "short": true, "clip": true,
	"extra": true, "other": true,
}

// videoRole reads which video a file is. A mark in the name wins over the
// folder, so a trailer under Extras still reads as a trailer, and a video in an
// extras folder with no mark takes the folder's own word.
func videoRole(base string, place filePlace) string {
	tokens := nameTokens(base)
	for _, token := range tokens {
		if token == fileRoleSample {
			return fileRoleSample
		}
	}
	last := lastToken(tokens)
	switch {
	case last == fileRoleTrailer:
		return fileRoleTrailer
	case last == fileRoleTheme:
		return fileRoleTheme
	case extraMarks[last]:
		return fileRoleExtra
	}
	if place.extras == extrasTrailers {
		return fileRoleTrailer
	}
	if place.extras != "" {
		return fileRoleExtra
	}
	return fileRolePrimary
}

// audioRole tells a theme song from an ordinary track. Jellyfin writes a theme
// as theme.mp3, and Kodi as <title>-theme.mp3.
func audioRole(base string) string {
	if lastToken(nameTokens(base)) == fileRoleTheme {
		return fileRoleTheme
	}
	return fileRoleTrack
}

// subtitleFlagWindow bounds how far back from the end of a name the scanner
// reads a subtitle flag, so a title word does not read as one.
const subtitleFlagWindow = 2

// subtitleRole reads the flag the tools write after the language tag. A
// subtitle with no flag is the full track.
func subtitleRole(base string) string {
	tokens := nameTokens(base)
	for i := max(len(tokens)-subtitleFlagWindow, 0); i < len(tokens); i++ {
		switch tokens[i] {
		case fileRoleForced:
			return fileRoleForced
		case fileRoleSDH, "cc":
			return fileRoleSDH
		}
	}
	if hearingImpairedFlag(base) {
		return fileRoleSDH
	}
	return fileRoleFull
}

// hearingImpairedTag is what the tools write for a hearing-impaired track,
// and it is also the language tag for Hindi. The two are told apart by what
// comes before: a language tag precedes the flag, so The Matrix.en.hi.srt is
// English for the hearing impaired, and The Matrix.hi.srt is Hindi.
const hearingImpairedTag = "hi"

// hearingImpairedFlag reports whether a name carries hi as the flag rather
// than as the language. It reads the dotted tokens, which is where the tools
// write both, and it needs a token before the language tag as well, so the
// title itself is never read as one.
func hearingImpairedFlag(base string) bool {
	tokens := strings.Split(strings.ToLower(base), ".")
	for i := len(tokens) - 1; i >= 2; i-- {
		if tokens[i] == hearingImpairedTag {
			return isLanguageTag(tokens[i-1])
		}
	}
	return false
}

// The words an image's name carries, and the art each one names. This is a
// slice and not a map, so the words are read in this order every time, and a
// compound word comes before the word inside it.
var imageMarks = []struct {
	mark string
	role string
}{
	{"clearlogo", fileRoleLogo},
	{"clearart", fileRoleClearart},
	{"discart", fileRoleDisc},
	{"cdart", fileRoleDisc},
	{"backdrop", fileRoleBackdrop},
	{"fanart", fileRoleBackdrop},
	{"banner", fileRoleBanner},
	{"landscape", fileRoleThumb},
	{"thumb", fileRoleThumb},
	{"poster", fileRolePoster},
	{"folder", fileRolePoster},
	{"cover", fileRolePoster},
	{"logo", fileRoleLogo},
	{"disc", fileRoleDisc},
	{"still", fileRoleStill},
}

// imageRole reads which art an image is. An image in a season folder that
// carries none of the words is the still beside an episode, which is the one
// image a season folder holds under a video's own name.
func imageRole(base string, place filePlace) string {
	for _, entry := range imageMarks {
		if strings.Contains(base, entry.mark) {
			return entry.role
		}
	}
	if place.season {
		return fileRoleStill
	}
	return ""
}

// metadataRole reads which sidecar an .nfo is. The fixed names win, and a
// sidecar named after its own file takes the kind of the library that holds it.
func metadataRole(base string, place filePlace) string {
	switch {
	case base == fileRoleMovie:
		return fileRoleMovie
	case base == fileRoleTVShow:
		return fileRoleTVShow
	case base == fileRoleCollection:
		return fileRoleCollection
	case strings.HasPrefix(base, fileRoleSeason):
		return fileRoleSeason
	}
	if place.kind == libraryKindSeries {
		return fileRoleEpisode
	}
	return fileRoleMovie
}

// The flags a subtitle name carries after its language tag. The language read
// steps over them, so en.forced reads as the language en.
var subtitleFlags = map[string]bool{
	fileRoleForced: true, fileRoleSDH: true, "cc": true,
	"default": true, fileRoleFull: true,
}

// fileLanguage reads the language tag off a file name, in the form the tools
// write it: The Matrix (1999).en.srt, or The Matrix (1999).en.forced.srt. It is
// a two-letter or three-letter tag as the name gave it, with no translation
// between the two. A name with one dotted token carries no tag, so a film named
// Up keeps its title. It steps over the flags that follow the tag, hi among
// them where hi is the flag and not the Hindi language.
func fileLanguage(name string) string {
	base := stripAnyExtension(name)
	flagged := hearingImpairedFlag(base)
	tokens := strings.Split(base, ".")
	for i := len(tokens) - 1; i >= 1; i-- {
		token := strings.ToLower(strings.TrimSpace(tokens[i]))
		if subtitleFlags[token] {
			continue
		}
		if token == hearingImpairedTag && flagged {
			continue
		}
		if isLanguageTag(token) {
			return token
		}
		return ""
	}
	return ""
}

// isLanguageTag reports whether a token reads as a two-letter or three-letter
// language tag.
func isLanguageTag(token string) bool {
	if len(token) != 2 && len(token) != 3 {
		return false
	}
	for _, r := range token {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// nameTokens splits a base name on the separators the tools write between a
// title and the marks that follow it.
func nameTokens(base string) []string {
	return strings.FieldsFunc(base, func(r rune) bool {
		return r == '.' || r == '-' || r == '_' || r == ' '
	})
}

// lastToken reads the trailing token of a name, the place the tools write the
// mark that says what a file is.
func lastToken(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	return tokens[len(tokens)-1]
}

// stripAnyExtension drops a name's final extension, whatever it is, so
// The Matrix (1999).en.forced.srt reads as The Matrix (1999).en.forced.
func stripAnyExtension(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// folderFiles is one directory the walk reads for the files a title carries.
// The walk reads a title folder, a season folder under a series, and an extras
// folder under a title folder, and it descends no further.
type folderFiles struct {
	root    string
	dir     string
	library string
	place   filePlace
	// item names the item a file in this directory links to. A season folder
	// answers with the episode whose own name the file starts with; every
	// other place answers with one id.
	item func(name string) string
	// held is the names the item walk already wrote a row for, so this pass
	// adds no second row for a video that carries its sidecar's attributes.
	held map[string]bool
}

// read returns a row for every file this directory holds, and the names of the
// subdirectories under it, so the caller descends into the season and extras
// folders from the one read. A .trickplay directory is one row and its tiles
// are none, because a large library holds millions of tiles and the directory
// is the unit a player asks for.
func (f folderFiles) read() ([]fileRow, []string) {
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return nil, nil
	}
	var rows []fileRow
	var subdirectories []string
	for _, entry := range entries {
		name := entry.Name()
		if skipName(name) {
			continue
		}
		if entry.IsDir() {
			if strings.EqualFold(filepath.Ext(name), trickplayExtension) {
				rows = append(rows, f.row(name, fileClass{Type: fileTypeTrickplay, Role: fileRoleTiles}))
				continue
			}
			subdirectories = append(subdirectories, name)
			continue
		}
		if f.held[name] {
			continue
		}
		rows = append(rows, f.row(name, classifyFile(name, f.place)))
	}
	return rows, subdirectories
}

// row builds one classified file row. The size and the modification time come
// from one stat, and no image is opened to measure it.
func (f folderFiles) row(name string, class fileClass) fileRow {
	absolute := filepath.Join(f.dir, name)
	size, modified := statFile(absolute)
	row := fileRow{
		Path:      relativePath(f.root, absolute),
		Library:   f.library,
		Container: containerFromExtension(name),
		SizeBytes: size,
		Modified:  modified,
		Type:      class.Type,
		Role:      class.Role,
		Language:  class.Language,
		Present:   true,
	}
	if class.Type == fileTypeVideo {
		row.Width, row.Height = resolutionFromName(name)
		row.Trickplay = trickplayFor(f.root, f.dir, name)
	}
	if item := f.item(name); item != "" {
		row.Items = []string{item}
	}
	return row
}

// constantItem answers with one item id for every file in a directory, the
// resolver a movie title folder and a series folder both use.
func constantItem(item string) func(string) string {
	return func(string) string { return item }
}

// episodeItem answers with the episode whose own file name a file's name starts
// with, and with the series where it matches none, which is where a season
// poster lands. The longest match wins, so an episode does not take a file that
// belongs to another episode whose name it is a prefix of.
func episodeItem(episodes map[string]string, series string) func(string) string {
	return func(name string) string {
		base := stripAnyExtension(name)
		longest, item := "", series
		for episodeBase, id := range episodes {
			if len(episodeBase) > len(longest) && strings.HasPrefix(base, episodeBase) {
				longest, item = episodeBase, id
			}
		}
		return item
	}
}

// statFile reads a path's size in bytes and the time it was last written, in
// Unix seconds, from one stat. A path it cannot read reads as zero.
func statFile(path string) (int64, int64) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	return info.Size(), info.ModTime().Unix()
}
