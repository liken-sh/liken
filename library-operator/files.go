package main

// files.go classifies every file a title folder carries: the sidecars, the
// art, the subtitles, the trickplay tiles, and the extras beside the video.
// The classification reads a file's name and the place that holds it, and
// opens no media file, so a re-walk classifies a file the same way every time
// and a large library costs one stat per file.

import (
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

// The service and trash directories a filesystem or a storage appliance
// keeps beside the media. They hold no title. A Synology share root carries
// #recycle, which is root-owned and mode 000, so a scanner that reads it marks
// every pass incomplete and the prune never runs. The list is closed: no
// patterns and no configuration, because the operator's own ignore list is
// where a volume's other folders belong.
var serviceNames = map[string]bool{
	"#recycle": true, "@eadir": true, "$recycle.bin": true,
	"lost+found": true, "system volume information": true,
}

// skipName reports whether the walk leaves an entry out: a name that
// starts with a dot, the junk names above, and the service directories above.
// The two name lists are matched without regard to case, the way the
// appliances that write them vary it.
func skipName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(name, ".") || junkNames[lower] || serviceNames[lower]
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
//
// A trickplay directory never reaches here: its category comes from the
// directory's name rather than from fileTypeOf, and the walk builds its
// row with the tiles role already set.
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
//
// Every mark, sample among them, is read off the last token alone, so a
// title that opens with one of the words is not a sample.
func videoRole(base string, place filePlace) string {
	tokens := nameTokens(base)
	last := lastToken(tokens)
	switch {
	case last == fileRoleSample:
		return fileRoleSample
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
//
// The word is read off the end of the name's last token, where the tools
// write it, the way videoRole reads its own marks. Trailing digits are
// stripped before the match, so extrafanart1.jpg is a backdrop. A title
// that holds one of the words anywhere else, the way Discovery holds
// disc, is not art.
func imageRole(base string, place filePlace) string {
	if role, _, _ := imageArt(base); role != "" {
		return role
	}
	if place.season {
		return fileRoleStill
	}
	return ""
}

// imageArt reads which art an image is, from its name alone. bare says
// the name is the mark word itself rather than a word after a title, and
// rank is the mark's place in imageMarks, where the explicit name comes
// before the generic one; discoverArt picks the lower rank among the
// images of one role. A name that names no art reads as no role, at a
// rank past every mark.
func imageArt(base string) (role string, rank int, bare bool) {
	tokens := nameTokens(base)
	last := strings.TrimRight(lastToken(tokens), "0123456789")
	for index, entry := range imageMarks {
		if strings.HasSuffix(last, entry.mark) {
			return entry.role, index, len(tokens) == 1 && last == entry.mark
		}
	}
	return "", len(imageMarks), false
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
