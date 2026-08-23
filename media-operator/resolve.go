package main

// The resolver is the extension point the design names: a new
// scheme becomes an entry here, and the Play spec never changes
// shape. Of the two schemes today, https:// costs the pod nothing
// but an argument, and nfs:// costs it a volume the kubelet mounts
// with the kernel's NFS client.
//
// The resolver reads the media and the art URIs in one pass. It groups every
// nfs:// URI by server, mounts each server's common ancestor once, and
// rewrites each file's path under that mount. So a film and its logo in one
// folder become one read-only mount of that folder.

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// mediaMountPrefix is where the NFS directories land in the playback
// pod, numbered by first appearance in the playlist. A directory
// name would need escaping to be a mount path; an ordinal never
// does.
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
	Volumes    []Volume
	Mounts     []VolumeMount
}

// nfsRef is one parsed nfs:// URI: the server it names, and the path segments
// below the server's root. The last segment is the file.
type nfsRef struct {
	server   string
	segments []string
}

// resolvedRef is one URI classified for the pod. A passthrough is an https://
// URL, or an empty art field the pod uses as it is. An nfs reference rewrites
// to a path under its server's mount.
type resolvedRef struct {
	passthrough string
	nfs         *nfsRef
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

	var serverOrder []string
	seen := map[string]bool{}
	dirLists := map[string][][]string{}
	register := func(n *nfsRef) {
		if !seen[n.server] {
			seen[n.server] = true
			serverOrder = append(serverOrder, n.server)
		}
		dirLists[n.server] = append(dirLists[n.server], n.segments[:len(n.segments)-1])
	}

	for index, item := range items {
		media, err := parseRef(item.URI)
		if err != nil {
			return resolution{}, err
		}
		mediaRefs[index] = media
		if media.nfs != nil {
			register(media.nfs)
		}

		logo, trickplay := "", ""
		if item.Presentation != nil {
			logo = item.Presentation.Logo
			trickplay = item.Presentation.Trickplay
		}
		if logo != "" {
			art, err := parseRef(logo)
			if err != nil {
				return resolution{}, err
			}
			logoRefs[index] = art
			if art.nfs != nil {
				register(art.nfs)
			}
		}
		if trickplay != "" {
			trick, err := parseRef(trickplay)
			if err != nil {
				return resolution{}, err
			}
			trickRefs[index] = trick
			if trick.nfs != nil {
				register(trick.nfs)
			}
		}
	}

	ancestor := map[string][]string{}
	ordinal := map[string]int{}
	var resolved resolution
	for index, server := range serverOrder {
		anc := commonPrefix(dirLists[server])
		ord := index + 1
		ancestor[server] = anc
		ordinal[server] = ord
		name := "media-" + strconv.Itoa(ord)
		resolved.Volumes = append(resolved.Volumes, Volume{
			Name: name,
			NFS:  &NFSVolumeSource{Server: server, Path: "/" + strings.Join(anc, "/"), ReadOnly: true},
		})
		// Read-only in both places, on the volume and on the mount,
		// because a player never writes to a library.
		resolved.Mounts = append(resolved.Mounts, VolumeMount{
			Name:      name,
			MountPath: mountPathFor(ord),
			ReadOnly:  true,
		})
	}

	rewrite := func(ref resolvedRef) string {
		if ref.nfs == nil {
			return ref.passthrough
		}
		remainder := ref.nfs.segments[len(ancestor[ref.nfs.server]):]
		return mountPathFor(ordinal[ref.nfs.server]) + "/" + strings.Join(remainder, "/")
	}
	resolved.Items = make([]string, len(items))
	resolved.Logos = make([]string, len(items))
	resolved.Trickplays = make([]string, len(items))
	for index := range items {
		resolved.Items[index] = rewrite(mediaRefs[index])
		resolved.Logos[index] = rewrite(logoRefs[index])
		resolved.Trickplays[index] = rewrite(trickRefs[index])
	}
	return resolved, nil
}

// resolvedPreferences is the settled value of each preference field, after
// the three tiers resolve.
type resolvedPreferences struct {
	AudioLanguages    []string
	SubtitleLanguages []string
	Subtitles         string
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
	return resolvedPreferences{
		AudioLanguages:    firstStatedList(audio),
		SubtitleLanguages: firstStatedList(subs),
		Subtitles:         firstStatedString(mode),
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

// parseRef classifies one URI. An https URI passes through. An nfs URI parses
// into a server and a path. Any other scheme, or a missing one, fails the
// whole Play, so a Play that can never run leaves no half-built objects behind.
func parseRef(raw string) (resolvedRef, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return resolvedRef{}, fmt.Errorf("the URI %q does not parse: %v", raw, err)
	}
	switch parsed.Scheme {
	case "https":
		return resolvedRef{passthrough: raw}, nil
	case "nfs":
		ref, err := parseNFS(parsed, raw)
		if err != nil {
			return resolvedRef{}, err
		}
		return resolvedRef{nfs: ref}, nil
	case "":
		return resolvedRef{}, fmt.Errorf("the URI %q carries no scheme; the operator resolves https:// and nfs://", raw)
	default:
		return resolvedRef{}, fmt.Errorf(
			"the scheme %s:// is not one the operator resolves; it resolves https:// and nfs://",
			parsed.Scheme)
	}
}

// parseNFS reads the server and the path segments from one nfs URI. A URI that
// names no server, no file, or no directory to mount fails, because none of
// the three resolves to a mount with a file under it.
func parseNFS(parsed *url.URL, raw string) (*nfsRef, error) {
	if parsed.Host == "" {
		return nil, fmt.Errorf("the URI %q names no NFS server", raw)
	}
	if strings.HasSuffix(parsed.Path, "/") {
		return nil, fmt.Errorf("the URI %q names a directory, not a file", raw)
	}
	segments := splitPath(parsed.Path)
	if len(segments) < 2 {
		return nil, fmt.Errorf("the URI %q names no directory to mount", raw)
	}
	return &nfsRef{server: parsed.Host, segments: segments}, nil
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
