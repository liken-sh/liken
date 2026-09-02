package main

// The resolver is the extension point the design names: a new
// scheme becomes an entry here, and the Play spec never changes
// shape. Of the three schemes today, https:// costs the pod nothing
// but an argument, nfs:// costs it a volume the kubelet mounts with
// the kernel's NFS client, and claim:// costs it a claim the kubelet
// resolves to whatever storage backs it.
//
// The resolver reads the media and the art URIs in one pass. It groups every
// nfs:// URI by server, mounts each server's common ancestor once, and
// rewrites each file's path under that mount. So a film and its logo in one
// folder become one read-only mount of that folder. A claim:// URI groups by
// claim name and mounts the claim at its own root, because the claim already
// bounds what the pod can read. The servers and the claims share one run of
// mount numbers, in the order the playlist first names each of them.

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// mediaMountPrefix is where the media mounts land in the playback
// pod, NFS directories and claims alike, numbered by first appearance
// in the playlist. A directory or claim name would need escaping to
// be a mount path; an ordinal never does.
const mediaMountPrefix = "/media/"

// resolution is one resolved playlist: the player's arguments in
// spec order, and the volumes and mounts the pod needs to reach
// them.
//
// Logos is the resolved logo for each item, in spec order. An item with no
// logo has an empty string.
type resolution struct {
	Items []string
	Logos []string
	// Trickplays is the resolved trickplay reference for each item, in spec
	// order. An item with no trickplay has an empty string.
	Trickplays []string
	// Arts is the resolved cover reference for each item, in spec order. It
	// resolves the way the logo does, and an item with no art has an empty
	// string.
	Arts    []string
	Volumes []Volume
	Mounts  []VolumeMount
}

// nfsRef is one parsed nfs:// URI: the server it names, and the path segments
// below the server's root. The last segment is the file.
type nfsRef struct {
	server   string
	segments []string
}

// claimRef is one parsed claim:// URI: the claim it names, and the path
// segments under the claim's root. The last segment is the file, or an
// album's folder.
type claimRef struct {
	claim    string
	segments []string
}

// resolvedRef is one URI classified for the pod. A passthrough is an https://
// URL, or an empty art field the pod uses as it is. An nfs reference rewrites
// to a path under its server's mount, and a claim reference to a path under
// its claim's mount.
type resolvedRef struct {
	passthrough string
	nfs         *nfsRef
	claim       *claimRef
}

// mountKey names one mount the pod needs: an NFS server or a claim. Both
// kinds key one map and one run of ordinals, so a mount's number says when
// the playlist first named it and nothing about its kind.
type mountKey struct {
	scheme string
	name   string
}

// mount reports the mount a reference needs and the path segments under it.
// A passthrough reports none, because the pod uses the URL as it is.
func (r resolvedRef) mount() (mountKey, []string, bool) {
	switch {
	case r.nfs != nil:
		return mountKey{scheme: schemeNFS, name: r.nfs.server}, r.nfs.segments, true
	case r.claim != nil:
		return mountKey{scheme: schemeClaim, name: r.claim.claim}, r.claim.segments, true
	}
	return mountKey{}, nil, false
}

// resolvePlay turns the spec's items into what the pod needs. An https
// URI resolves to nothing but itself, because any machine can stream it
// and seek in it with range requests.
//
// An nfs URI is ambiguous about where the export ends and the path inside it
// begins. So the resolver mounts the common ancestor of every nfs URI on one
// server, and passes each file's path under that mount. Mounting a wider
// subtree than one file is safe, because the mount is read-only.
func resolvePlay(items []PlayItem) (resolution, error) {
	mediaRefs := make([]resolvedRef, len(items))
	logoRefs := make([]resolvedRef, len(items))
	trickRefs := make([]resolvedRef, len(items))
	artRefs := make([]resolvedRef, len(items))

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
		// Only an nfs reference narrows its mount, to the common
		// ancestor of its directories. A claim mounts at its root,
		// so it contributes no directory.
		if ref.nfs != nil {
			dirLists[key] = append(dirLists[key], segments[:len(segments)-1])
		}
	}

	for index, item := range items {
		// A directory item is refused unless its block marks it as an album.
		// The pod expands an album into one timeline and one playlist entry,
		// and a directory mpv expands itself would add entries the operator
		// never counted, so every later item's block and art would land on
		// the wrong track.
		if strings.HasSuffix(item.URI, "/") && !albumItem(item) {
			return resolution{}, fmt.Errorf(
				"the URI %q names a directory; mark the item as an album with type music and hint album, or name a file",
				item.URI)
		}
		media, err := parseRef(item.URI)
		if err != nil {
			return resolution{}, err
		}
		mediaRefs[index] = media
		register(media)

		logo, trickplay, cover := "", "", ""
		if item.Presentation != nil {
			logo = item.Presentation.Logo
			trickplay = item.Presentation.Trickplay
			cover = item.Presentation.Art
		}
		if logo != "" {
			art, err := parseRef(logo)
			if err != nil {
				return resolution{}, err
			}
			logoRefs[index] = art
			register(art)
		}
		if trickplay != "" {
			trick, err := parseRef(trickplay)
			if err != nil {
				return resolution{}, err
			}
			trickRefs[index] = trick
			register(trick)
		}
		if cover != "" {
			art, err := parseRef(cover)
			if err != nil {
				return resolution{}, err
			}
			artRefs[index] = art
			register(art)
		}
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
		// Read-only in both places, on the volume and on the mount,
		// because a player never writes to a library.
		resolved.Mounts = append(resolved.Mounts, VolumeMount{
			Name:      volume.Name,
			MountPath: mountPathFor(ord),
			ReadOnly:  true,
		})
	}

	rewrite := func(ref resolvedRef) string {
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
	for index := range items {
		resolved.Items[index] = rewrite(mediaRefs[index])
		resolved.Logos[index] = rewrite(logoRefs[index])
		resolved.Trickplays[index] = rewrite(trickRefs[index])
		resolved.Arts[index] = rewrite(artRefs[index])
	}
	return resolved, nil
}

// albumItem says whether one item's block marks it as an album, the one shape
// of item the pod expands from a directory.
func albumItem(item PlayItem) bool {
	return item.Presentation != nil && isAlbum(*item.Presentation)
}

// resolvedPreferences is the settled value of each preference field, after
// the three tiers resolve.
type resolvedPreferences struct {
	AudioLanguages    []string
	SubtitleLanguages []string
	Subtitles         string
	// The resolved wall-clock zone, from the default tier alone.
	TimeZone string
}

// resolvePreferences settles each field on its own, Play then Player then the
// default. A nil source is a tier that does not exist and is skipped, and a
// field no tier states resolves to nothing.
func resolvePreferences(play *PlaySpec, player *PlayerSpec, defaults *MediaPreferencesSpec) resolvedPreferences {
	var audio, subs [][]string
	var mode []string
	if play != nil {
		audio = append(audio, play.AudioLanguages)
		subs = append(subs, play.SubtitleLanguages)
		mode = append(mode, play.Subtitles)
	}
	if player != nil {
		audio = append(audio, player.AudioLanguages)
		subs = append(subs, player.SubtitleLanguages)
		mode = append(mode, player.Subtitles)
	}
	if defaults != nil {
		audio = append(audio, defaults.AudioLanguages)
		subs = append(subs, defaults.SubtitleLanguages)
		mode = append(mode, defaults.Subtitles)
	}
	// The timezone is a household setting, one per cluster, so it reads the
	// default tier alone, not the Play or the Player.
	var zone string
	if defaults != nil {
		zone = defaults.TimeZone
	}
	return resolvedPreferences{
		AudioLanguages:    firstStatedList(audio),
		SubtitleLanguages: firstStatedList(subs),
		Subtitles:         firstStatedString(mode),
		TimeZone:          zone,
	}
}

// firstStatedList returns the first list a tier states. A nil list is unset,
// and an empty non-nil list is a tier stating no preference, which overrides a
// lower tier.
func firstStatedList(tiers [][]string) []string {
	for _, list := range tiers {
		if list != nil {
			return list
		}
	}
	return nil
}

// defaultFadeAfterSeconds is the fade window a cluster that states
// nothing takes: ten minutes of quiet, then the idle screen fades.
const defaultFadeAfterSeconds int64 = 600

// Darkening hardware is opt-in, so a cluster that states no
// window keeps its panels lit.
const defaultOffAfterSeconds int64 = 0

// The built-in mode is the backlight, because a panel at zero
// backlight still answers DDC and the restore cannot strand.
const defaultOffMode = offModeBacklight

// The two controller names this operator answers to.
// idleControllerOwn is the built-in default, and this operator stands
// the claim and the idle client pod for it.
// Under idleControllerNone nothing draws an idle screen on the unit:
// this operator stands no claim and no pod, and it leaves the panel
// alone. The precedent is kubernetes.io/no-provisioner. Every other
// name is a delegate, and this operator compares a name to these two
// and to nothing else.
const (
	idleControllerOwn  = "media.liken.sh/idle-screen"
	idleControllerNone = "media.liken.sh/none"
)

// resolvedIdle is one Player's settled idle policy. The two windows
// are set on the idle pod as plain values, and the off mode stays
// with the operator, which writes the override.
type resolvedIdle struct {
	// The image the idle client runs, always set, because the operator's
	// own IDLE_IMAGE is the floor under both tiers.
	Image string

	// The name of the operator that draws this unit's idle
	// screen, always set, because idleControllerOwn is the floor under
	// both tiers.
	Controller string

	FadeAfterSeconds int64

	// The settled off window, and the override the operator
	// applies when it runs out.
	OffAfterSeconds int64
	OffMode         string
}

// resolveIdle settles each idle field on its own: the Player's block,
// then the household default, then the built-in. Field by field, so a
// Player that states one field still inherits the rest. image is the
// client the operator reads from IDLE_IMAGE, the last tier under the
// two the cluster states.
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
		Image:            firstStatedString(append(images, image)),
		Controller:       firstStatedString(append(controllers, idleControllerOwn)),
		FadeAfterSeconds: firstStatedSeconds(fade, defaultFadeAfterSeconds),
		OffAfterSeconds:  firstStatedSeconds(off, defaultOffAfterSeconds),
		OffMode:          firstStatedIdleMode(mode),
	}
}

// firstStatedIdleMode is firstStatedString with the built-in
// mode as the floor, so an off mode no tier states is the backlight.
func firstStatedIdleMode(tiers []string) string {
	if mode := firstStatedString(tiers); mode != "" {
		return mode
	}
	return defaultOffMode
}

// firstStatedSeconds returns the first value a tier states. A nil
// pointer is unset, and an explicit zero is a statement, so a Player
// can turn a window off rather than only shorten it.
func firstStatedSeconds(tiers []*int64, fallback int64) int64 {
	for _, value := range tiers {
		if value != nil {
			return *value
		}
	}
	return fallback
}

// firstStatedString returns the first value a tier states. Subtitles has no
// empty enum value, so an empty string is unset.
func firstStatedString(tiers []string) string {
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

// parseRef classifies one URI. An https URI passes through. An nfs URI parses
// into a server and a path, and a claim URI into a claim name and a path.
// Any other scheme, or a missing one, fails the whole Play, so a Play that
// can never run leaves no half-built objects behind.
func parseRef(raw string) (resolvedRef, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return resolvedRef{}, fmt.Errorf("the URI %q does not parse: %v", raw, err)
	}
	switch parsed.Scheme {
	case "https":
		return resolvedRef{passthrough: raw}, nil
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
		return resolvedRef{claim: ref}, nil
	case "":
		return resolvedRef{}, fmt.Errorf("the URI %q carries no scheme; the operator resolves https://, nfs://, and claim://", raw)
	default:
		return resolvedRef{}, fmt.Errorf(
			"the scheme %s:// is not one the operator resolves; it resolves https://, nfs://, and claim://",
			parsed.Scheme)
	}
}

// parseNFS reads the server and the path segments from one nfs URI. A URI that
// names no server, or no directory to mount, fails, because neither resolves
// to a mount with the item under it.
//
// The last segment may name a file or a folder, because a music album is
// one item and one folder. A trailing slash changes nothing, because the
// empty last segment drops out of the split.
func parseNFS(parsed *url.URL, raw string) (*nfsRef, error) {
	if parsed.Host == "" {
		return nil, fmt.Errorf("the URI %q names no NFS server", raw)
	}
	segments := splitPath(parsed.Path)
	if len(segments) < 2 {
		return nil, fmt.Errorf("the URI %q names no directory to mount", raw)
	}
	return &nfsRef{server: parsed.Host, segments: segments}, nil
}

// parseClaim reads the claim name and the path segments from one claim URI.
// A URI that names no claim, or no path inside it, fails, because neither
// names a file the pod can reach. One segment is enough, because the claim's
// own root is the mount and no directory has to be chosen.
func parseClaim(parsed *url.URL, raw string) (*claimRef, error) {
	if parsed.Host == "" {
		return nil, fmt.Errorf("the URI %q names no claim", raw)
	}
	segments := splitPath(parsed.Path)
	if len(segments) == 0 {
		return nil, fmt.Errorf("the URI %q names no path in the claim", raw)
	}
	return &claimRef{claim: parsed.Host, segments: segments}, nil
}

// splitPath breaks a URL path into its non-empty segments.
func splitPath(path string) []string {
	var segments []string
	for _, segment := range strings.Split(path, "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}

// commonPrefix returns the longest run of leading segments every list shares.
// Two files in one directory share that directory. Two files in sibling
// directories share the parent.
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
