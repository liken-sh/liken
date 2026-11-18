package main

// The resolver turns the URIs of a Play into the arguments, volumes, and
// mounts of the playback pod. An https:// URI goes to the player
// unchanged. A pattern:// URI becomes the path of a file in the player
// image (patterns.go). An nfs:// URI becomes an NFS volume, and a
// claim://<namespace>/<claim>/<path> URI becomes a PersistentVolumeClaim
// volume.

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// mediaMountPrefix is the directory in the playback pod that holds the
// media mounts. NFS servers and claims share one numbering, in the order
// the playlist first uses each of them. A number is a safe path segment,
// and a server or claim name might not be.
const mediaMountPrefix = "/media/"

// resolution is one resolved playlist and the volumes and mounts the pod
// needs to read it. Each slice has one entry per item, in spec order.
// An item without a logo, trickplay sheet, cover, spans file, or
// .contributors directory has an empty string in that slice.
type resolution struct {
	Items        []string
	Logos        []string
	Trickplays   []string
	Arts         []string
	Appearances  []string
	Contributors []string
	// Next is the resolved art of the Play's next block. It is empty when
	// the Play has no next block or the block has no art.
	Next    string
	Volumes []Volume
	Mounts  []VolumeMount
}

// nfsRef is one parsed nfs:// URI. The last segment is the file or the
// album folder.
type nfsRef struct {
	server   string
	segments []string
}

// claimRef is one parsed claim:// URI. The segments are the path under
// the claim's root, and the last one is the file or the album folder.
type claimRef struct {
	namespace string
	claim     string
	segments  []string
}

// resolvedRef is one parsed URI. Exactly one field is set, except for an
// optional URI that the Play leaves empty, which sets none. passthrough
// is an https:// URL.
type resolvedRef struct {
	passthrough string
	nfs         *nfsRef
	claim       *claimRef
	pattern     *patternRef
}

// mountKey identifies one mount the pod needs: an NFS server or a claim.
type mountKey struct {
	scheme string
	name   string
}

// mount returns the mount a reference needs and its path segments under
// that mount. It returns false for a reference that needs no mount.
func (r resolvedRef) mount() (mountKey, []string, bool) {
	switch {
	case r.nfs != nil:
		return mountKey{scheme: schemeNFS, name: r.nfs.server}, r.nfs.segments, true
	case r.claim != nil:
		return mountKey{scheme: schemeClaim, name: r.claim.claim}, r.claim.segments, true
	}
	return mountKey{}, nil, false
}

// resolvePlay resolves the media and art URIs of a Play's items and next
// block together, so a film and its logo on one server or claim share
// one mount. An https:// URI stays as it is, because the player streams
// it and seeks with range requests.
//
// An nfs:// URI does not show where the export ends and the path inside
// it begins. So for each server, resolvePlay mounts the deepest directory
// that holds every file the Play uses on that server, and rewrites each
// path under that mount. The mount can expose files the Play does not
// name, which is safe because it is read-only. A claim mounts at its
// root, because the claim already limits what the pod can read.
//
// A claim:// URI must name the Play's own namespace, because a pod can
// mount only a claim in its own namespace.
//
// screen is the size of the Player's screen, which a pattern:// URI with
// no frame of its own takes. The zero frame means the size is unknown.
func resolvePlay(namespace string, items []PlayItem, next *PlayNext, screen frame) (resolution, error) {
	mediaRefs := make([]resolvedRef, len(items))
	logoRefs := make([]resolvedRef, len(items))
	trickRefs := make([]resolvedRef, len(items))
	artRefs := make([]resolvedRef, len(items))
	appearanceRefs := make([]resolvedRef, len(items))
	contributorRefs := make([]resolvedRef, len(items))

	var mountOrder []mountKey
	seen := map[mountKey]bool{}
	dirLists := map[mountKey][][]string{}
	register := func(ref resolvedRef) {
		key, segments, ok := ref.mount()
		if !ok {
			return
		}
		if !seen[key] {
			seen[key] = true
			mountOrder = append(mountOrder, key)
		}
		// A claim mounts at its root, so only an nfs reference adds a
		// directory for commonPrefix.
		if ref.nfs != nil {
			dirLists[key] = append(dirLists[key], segments[:len(segments)-1])
		}
	}

	for index, item := range items {
		// The pod expands an album directory into one playlist entry. mpv
		// would expand any other directory into entries that the operator
		// did not count, and the blocks and art of later items would then
		// match the wrong tracks.
		if strings.HasSuffix(item.URI, "/") && !albumItem(item) {
			return resolution{}, fmt.Errorf(
				"the URI %q is a directory and the item is not an album, so set presentation.type to music and presentation.hint to album, or name a file",
				item.URI)
		}
		media, err := parseRef(item.URI, namespace)
		if err != nil {
			return resolution{}, err
		}
		mediaRefs[index] = media
		register(media)

		logo, trickplay, cover, appearances, contributors := "", "", "", "", ""
		if item.Presentation != nil {
			logo = item.Presentation.Logo
			trickplay = item.Presentation.Trickplay
			cover = item.Presentation.Art
			appearances = item.Presentation.Appearances
			contributors = item.Presentation.Contributors
		}
		if logo != "" {
			art, err := parseArt(logo, namespace)
			if err != nil {
				return resolution{}, err
			}
			logoRefs[index] = art
			register(art)
		}
		if trickplay != "" {
			trick, err := parseArt(trickplay, namespace)
			if err != nil {
				return resolution{}, err
			}
			trickRefs[index] = trick
			register(trick)
		}
		if cover != "" {
			art, err := parseArt(cover, namespace)
			if err != nil {
				return resolution{}, err
			}
			artRefs[index] = art
			register(art)
		}
		if appearances != "" {
			spans, err := parseArt(appearances, namespace)
			if err != nil {
				return resolution{}, err
			}
			appearanceRefs[index] = spans
			register(spans)
		}
		if contributors != "" {
			entries, err := parseArt(contributors, namespace)
			if err != nil {
				return resolution{}, err
			}
			contributorRefs[index] = entries
			register(entries)
		}
	}

	var nextRef resolvedRef
	if next != nil && next.Art != "" {
		art, err := parseArt(next.Art, namespace)
		if err != nil {
			return resolution{}, err
		}
		nextRef = art
		register(art)
	}

	ancestor := map[mountKey][]string{}
	ordinal := map[mountKey]int{}
	var resolved resolution
	for index, key := range mountOrder {
		anc := commonPrefix(dirLists[key])
		ord := index + 1
		ancestor[key] = anc
		ordinal[key] = ord
		volume := Volume{Name: "media-" + strconv.Itoa(ord)}
		switch key.scheme {
		case schemeNFS:
			volume.NFS = &NFSVolumeSource{Server: key.name, Path: "/" + strings.Join(anc, "/"), ReadOnly: true}
		case schemeClaim:
			volume.PersistentVolumeClaim = &PersistentVolumeClaimVolumeSource{ClaimName: key.name, ReadOnly: true}
		}
		resolved.Volumes = append(resolved.Volumes, volume)
		// The player never writes to a library, so the volume and the
		// mount are both read-only.
		resolved.Mounts = append(resolved.Mounts, VolumeMount{
			Name:      volume.Name,
			MountPath: mountPathFor(ord),
			ReadOnly:  true,
		})
	}

	rewrite := func(ref resolvedRef) string {
		if ref.pattern != nil {
			return ref.pattern.path(screen)
		}
		key, segments, ok := ref.mount()
		if !ok {
			return ref.passthrough
		}
		remainder := segments[len(ancestor[key]):]
		return mountPathFor(ordinal[key]) + "/" + strings.Join(remainder, "/")
	}
	resolved.Items = make([]string, len(items))
	resolved.Logos = make([]string, len(items))
	resolved.Trickplays = make([]string, len(items))
	resolved.Arts = make([]string, len(items))
	resolved.Appearances = make([]string, len(items))
	resolved.Contributors = make([]string, len(items))
	for index := range items {
		resolved.Items[index] = rewrite(mediaRefs[index])
		resolved.Logos[index] = rewrite(logoRefs[index])
		resolved.Trickplays[index] = rewrite(trickRefs[index])
		resolved.Arts[index] = rewrite(artRefs[index])
		resolved.Appearances[index] = rewrite(appearanceRefs[index])
		resolved.Contributors[index] = rewrite(contributorRefs[index])
	}
	resolved.Next = rewrite(nextRef)
	return resolved, nil
}

// albumItem reports whether the item's block marks it as an album, the
// only kind of directory item the pod expands.
func albumItem(item PlayItem) bool {
	return item.Presentation != nil && isAlbum(*item.Presentation)
}

type resolvedPreferences struct {
	AudioLanguages    []string
	SubtitleLanguages []string
	Subtitles         string
	SDH               string
	TimeZone          string
}

// resolvePreferences takes each field from the Play, else the Player,
// else the default MediaPreferences. A nil source is skipped, and a
// field that none of them sets stays empty.
func resolvePreferences(play *PlaySpec, player *PlayerSpec, defaults *MediaPreferencesSpec) resolvedPreferences {
	var audio, subs [][]string
	var mode, sdh []string
	if play != nil {
		audio = append(audio, play.AudioLanguages)
		subs = append(subs, play.SubtitleLanguages)
		mode = append(mode, play.Subtitles)
		sdh = append(sdh, play.SDH)
	}
	if player != nil {
		audio = append(audio, player.AudioLanguages)
		subs = append(subs, player.SubtitleLanguages)
		mode = append(mode, player.Subtitles)
		sdh = append(sdh, player.SDH)
	}
	if defaults != nil {
		audio = append(audio, defaults.AudioLanguages)
		subs = append(subs, defaults.SubtitleLanguages)
		mode = append(mode, defaults.Subtitles)
		sdh = append(sdh, defaults.SDH)
	}
	// The time zone is one setting for the whole cluster, so only the
	// default MediaPreferences sets it.
	var zone string
	if defaults != nil {
		zone = defaults.TimeZone
	}
	return resolvedPreferences{
		AudioLanguages:    firstSetList(audio),
		SubtitleLanguages: firstSetList(subs),
		Subtitles:         firstSetString(mode),
		SDH:               firstSetString(sdh),
		TimeZone:          zone,
	}
}

// firstSetList returns the first list that is not nil. An empty list
// that is not nil means "no preference", and it overrides the lists after
// it.
func firstSetList(tiers [][]string) []string {
	for _, list := range tiers {
		if list != nil {
			return list
		}
	}
	return nil
}

// defaultFadeAfterSeconds is the time before the idle screen fades when
// no Player or default sets one.
const defaultFadeAfterSeconds int64 = 600

// defaultOffAfterSeconds is zero, so the panel never goes dark unless a
// Player or the default sets a time.
const defaultOffAfterSeconds int64 = 0

// defaultOffMode is the backlight, because a panel with its backlight at
// zero still responds to DDC, so the operator can turn it back on. Some
// panels stop responding to DDC when they are powered off.
const defaultOffMode = offModeBacklight

// With idleControllerOwn, the default, the operator creates the claim
// and the idle client pod. With idleControllerNone, nothing draws an
// idle screen: the operator creates no claim and no pod, and it does not
// set the panel. The name follows the StorageClass provisioner
// kubernetes.io/no-provisioner. Any other name belongs to another
// operator, which draws the screen.
const (
	idleControllerOwn  = "media.liken.sh/idle-screen"
	idleControllerNone = "media.liken.sh/none"
)

// resolvedIdle is one Player's idle policy. The idle client pod receives
// the two times as environment variables. OffMode is not passed to the
// pod: the operator applies it to the panel when the idle client asks
// for the panel to go off.
type resolvedIdle struct {
	Image string

	// Controller is the name of the operator that draws the idle
	// screen. It is never empty.
	Controller string

	FadeAfterSeconds int64
	OffAfterSeconds  int64
	OffMode          string
}

// resolveIdle takes each field from the Player, else the default, else
// the built-in value, so a Player that sets one field still gets the
// rest from the default. image is the operator's own idle client image,
// the built-in value for Image.
func resolveIdle(player, defaults *IdlePolicy, image string) resolvedIdle {
	var fade, off []*int64
	var mode, images, controllers []string
	if player != nil {
		fade = append(fade, player.FadeAfterSeconds)
		off = append(off, player.OffAfterSeconds)
		mode = append(mode, player.OffMode)
		images = append(images, player.Image)
		controllers = append(controllers, player.Controller)
	}
	if defaults != nil {
		fade = append(fade, defaults.FadeAfterSeconds)
		off = append(off, defaults.OffAfterSeconds)
		mode = append(mode, defaults.OffMode)
		images = append(images, defaults.Image)
		controllers = append(controllers, defaults.Controller)
	}
	return resolvedIdle{
		Image:            firstSetString(append(images, image)),
		Controller:       firstSetString(append(controllers, idleControllerOwn)),
		FadeAfterSeconds: firstSetSeconds(fade, defaultFadeAfterSeconds),
		OffAfterSeconds:  firstSetSeconds(off, defaultOffAfterSeconds),
		OffMode:          firstSetIdleMode(mode),
	}
}

// firstSetIdleMode returns the first mode that is set, or defaultOffMode.
func firstSetIdleMode(tiers []string) string {
	if mode := firstSetString(tiers); mode != "" {
		return mode
	}
	return defaultOffMode
}

// firstSetSeconds returns the first value that is not nil, or fallback.
// Zero counts as set, so a Player can turn a time off.
func firstSetSeconds(tiers []*int64, fallback int64) int64 {
	for _, value := range tiers {
		if value != nil {
			return *value
		}
	}
	return fallback
}

// firstSetString returns the first value that is not empty. Empty
// means unset, because no field resolved this way has a valid empty
// value.
func firstSetString(tiers []string) string {
	for _, value := range tiers {
		if value != "" {
			return value
		}
	}
	return ""
}

const (
	schemeNFS   = "nfs"
	schemeClaim = "claim"
)

// parseRef parses one URI by its scheme. An error fails the whole Play
// before the operator creates its pod, so a Play that cannot run leaves
// no objects behind.
func parseRef(raw, namespace string) (resolvedRef, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return resolvedRef{}, fmt.Errorf("the URI %q is malformed: %w", raw, err)
	}
	switch parsed.Scheme {
	case "https":
		return resolvedRef{passthrough: raw}, nil
	case patternScheme:
		ref, err := parsePattern(parsed, raw)
		if err != nil {
			return resolvedRef{}, err
		}
		return resolvedRef{pattern: ref}, nil
	case schemeNFS:
		ref, err := parseNFS(parsed, raw)
		if err != nil {
			return resolvedRef{}, err
		}
		return resolvedRef{nfs: ref}, nil
	case schemeClaim:
		ref, err := parseClaim(parsed, raw)
		if err != nil {
			return resolvedRef{}, err
		}
		if ref.namespace != namespace {
			return resolvedRef{}, fmt.Errorf(
				"the URI %q names a claim in the namespace %s, and a Play in the namespace %s mounts only a claim in its own namespace",
				raw, ref.namespace, namespace)
		}
		return resolvedRef{claim: ref}, nil
	case "":
		return resolvedRef{}, fmt.Errorf("the URI %q has no scheme, and the operator resolves https://, nfs://, claim://, and pattern:// URIs", raw)
	default:
		return resolvedRef{}, fmt.Errorf(
			"the scheme %s:// is not one the operator resolves, which are https://, nfs://, claim://, and pattern://",
			parsed.Scheme)
	}
}

// parseArt parses the URI of a file that goes with the media: a logo, a
// trickplay sheet, a cover, a spans file, or the .contributors directory.
// A pattern is a video, so a pattern:// URI here is an error.
func parseArt(raw, namespace string) (resolvedRef, error) {
	ref, err := parseRef(raw, namespace)
	if err == nil && ref.pattern != nil {
		return resolvedRef{}, fmt.Errorf("the URI %q names a pattern, which plays as an item and not as art", raw)
	}
	return ref, err
}

// parseNFS requires a server and at least two path segments: a
// directory to mount and the file or album folder in it. A trailing
// slash makes no difference, because splitPath drops empty segments.
func parseNFS(parsed *url.URL, raw string) (*nfsRef, error) {
	if parsed.Host == "" {
		return nil, fmt.Errorf("the URI %q has no NFS server", raw)
	}
	segments := splitPath(parsed.Path)
	if len(segments) < 2 {
		return nil, fmt.Errorf("the URI %q has no directory to mount before the file", raw)
	}
	return &nfsRef{server: parsed.Host, segments: segments}, nil
}

// claimForm ends each error for a malformed claim URI, so the Play's
// author can correct it.
const claimForm = "a claim URI is claim://<namespace>/<claim>/<path>"

// parseClaim requires a namespace, a claim, and a path inside the claim.
// One path segment is enough, because the whole claim is mounted.
func parseClaim(parsed *url.URL, raw string) (*claimRef, error) {
	if parsed.Host == "" {
		return nil, fmt.Errorf("the URI %q has no namespace, and %s", raw, claimForm)
	}
	claim, below, _ := strings.Cut(strings.TrimPrefix(parsed.Path, "/"), "/")
	if claim == "" {
		return nil, fmt.Errorf("the URI %q has no claim name, and %s", raw, claimForm)
	}
	segments := splitPath(below)
	if len(segments) == 0 {
		return nil, fmt.Errorf("the URI %q has no path in the claim, and %s", raw, claimForm)
	}
	return &claimRef{namespace: parsed.Host, claim: claim, segments: segments}, nil
}

func splitPath(path string) []string {
	var segments []string
	for _, segment := range strings.Split(path, "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}

// commonPrefix returns the leading segments that all the lists share.
func commonPrefix(lists [][]string) []string {
	if len(lists) == 0 {
		return nil
	}
	prefix := lists[0]
	for _, list := range lists[1:] {
		length := 0
		for length < len(prefix) && length < len(list) && prefix[length] == list[length] {
			length++
		}
		prefix = prefix[:length]
	}
	return prefix
}

func mountPathFor(ordinal int) string {
	return mediaMountPrefix + strconv.Itoa(ordinal)
}
