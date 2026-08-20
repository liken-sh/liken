package main

// The resolver is the extension point the design names: a new
// scheme becomes an entry here, and the Play spec never changes
// shape. Of the two schemes today, https:// costs the pod nothing
// but an argument, and nfs:// costs it a volume the kubelet mounts
// with the kernel's NFS client.

import (
	"fmt"
	"net/url"
	"path"
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
type resolution struct {
	Items   []string
	Volumes []Volume
	Mounts  []VolumeMount
}

// resolveURIs turns the spec's URIs into what the pod needs. An
// https URI resolves to nothing but itself, because any machine can
// stream it and seek in it with range requests.
//
// An nfs URI is ambiguous: nothing in nfs://host/export/dir/file
// says where the export ends and the path inside it begins. The
// resolver mounts everything above the file and passes the file
// name as the argument. Mounting deeper than the export's root is
// safe, because the NFS protocol serves a subdirectory of an export
// the same way it serves the export; the cost is one mount per
// distinct directory instead of one per export.
func resolveURIs(uris []string) (resolution, error) {
	var resolved resolution
	mounted := map[string]int{}
	for _, raw := range uris {
		parsed, err := url.Parse(raw)
		if err != nil {
			return resolution{}, fmt.Errorf("the URI %q does not parse: %v", raw, err)
		}
		switch parsed.Scheme {
		case "https":
			resolved.Items = append(resolved.Items, raw)
		case "nfs":
			item, err := resolved.mountNFS(parsed, raw, mounted)
			if err != nil {
				return resolution{}, err
			}
			resolved.Items = append(resolved.Items, item)
		case "":
			return resolution{}, fmt.Errorf("the URI %q carries no scheme; the operator resolves https:// and nfs://", raw)
		default:
			return resolution{}, fmt.Errorf(
				"the scheme %s:// is not one the operator resolves; it resolves https:// and nfs://",
				parsed.Scheme)
		}
	}
	return resolved, nil
}

// mountNFS turns one nfs URI into a path inside a mount, adding the
// volume only the first time its directory appears. An album is ten
// URIs in one directory, and ten mounts of the same export would be
// nine the kubelet performs for nothing.
func (r *resolution) mountNFS(parsed *url.URL, raw string, mounted map[string]int) (string, error) {
	if parsed.Host == "" {
		return "", fmt.Errorf("the URI %q names no NFS server", raw)
	}
	directory, file := path.Split(parsed.Path)
	directory = strings.TrimSuffix(directory, "/")
	if file == "" {
		return "", fmt.Errorf("the URI %q names a directory, not a file", raw)
	}
	if directory == "" {
		return "", fmt.Errorf("the URI %q names no directory to mount", raw)
	}

	export := parsed.Host + ":" + directory
	ordinal, held := mounted[export]
	if !held {
		ordinal = len(mounted) + 1
		mounted[export] = ordinal
		name := "media-" + strconv.Itoa(ordinal)
		r.Volumes = append(r.Volumes, Volume{
			Name: name,
			NFS:  &NFSVolumeSource{Server: parsed.Host, Path: directory, ReadOnly: true},
		})
		// Read-only in both places, on the volume and on the mount,
		// because a player never writes to a library.
		r.Mounts = append(r.Mounts, VolumeMount{
			Name:      name,
			MountPath: mountPathFor(ordinal),
			ReadOnly:  true,
		})
	}
	return mountPathFor(ordinal) + "/" + file, nil
}

func mountPathFor(ordinal int) string {
	return mediaMountPrefix + strconv.Itoa(ordinal)
}
