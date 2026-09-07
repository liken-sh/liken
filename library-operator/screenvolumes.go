package main

// The two volumes a screen pod carries beside its libraries: the rows
// its catalog agent syncs, and the art its browser scaled. Each is a
// claim of the screen's own in a namespace with one Catalog, and an
// emptyDir in a namespace with none.

import (
	"strconv"
	"strings"
)

// The browser keeps every piece of art it scaled on the node's local
// disk, at this path, on the volume of this name.
const (
	artCacheVolumeName = "art-cache"
	artCacheMountPath  = "/var/cache/media-browser"
	// The cap on the emptyDir a screen with no Catalog holds: the
	// browser's own 512 MiB default plus the headroom below.
	artCacheSizeLimit = "640Mi"
)

// The room an atomic write needs for its temporary file before the
// rename. The operator keeps it out of the budget it gives the browser,
// so the cache never fills the volume it is on.
const artCacheHeadroom = 128 << 20

// The volume the catalog agent's state is on: the screen's own claim
// where the namespace holds one Catalog, and an emptyDir where it does not.
func screenCatalogVolume(player *Player, catalog *NamespaceCatalog) Volume {
	if catalog == nil {
		return Volume{Name: catalogVolumeName, EmptyDir: &EmptyDirVolumeSource{}}
	}
	return Volume{Name: catalogVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
		ClaimName: screenClaimName(player.Metadata.Name),
	}}
}

// The volume the scaled art is on, by the same rule as the catalog
// volume, so a screen that restarts draws the wall from art it already
// scaled.
func screenArtVolume(player *Player, catalog *NamespaceCatalog) Volume {
	if catalog == nil {
		return Volume{Name: artCacheVolumeName, EmptyDir: &EmptyDirVolumeSource{
			SizeLimit: artCacheSizeLimit,
		}}
	}
	return Volume{Name: artCacheVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
		ClaimName: screenArtClaimName(player.Metadata.Name),
	}}
}

// What the browser is told to keep under: the claim's size less the
// headroom. A screen on the emptyDir is told nothing and keeps the
// browser's own default, and so is a screen whose Catalog states a size
// the operator cannot read.
func artCacheArgs(catalog *NamespaceCatalog) []string {
	if catalog == nil {
		return nil
	}
	size, ok := parseBinaryQuantity(artCacheSize(catalog))
	if !ok || size <= artCacheHeadroom {
		return nil
	}
	return []string{"--cache-budget", strconv.FormatInt(size-artCacheHeadroom, 10)}
}

// The binary suffixes a Kubernetes quantity carries. The operator reads
// a size for its bytes here and nowhere else, so this is the whole of
// what it parses.
var binaryQuantityUnits = []struct {
	suffix string
	scale  int64
}{
	{"Ki", 1 << 10},
	{"Mi", 1 << 20},
	{"Gi", 1 << 30},
	{"Ti", 1 << 40},
}

// The bytes a quantity names. A plain integer is bytes. A decimal
// suffix or an exponent is refused rather than guessed at, and the
// caller then passes no budget.
func parseBinaryQuantity(size string) (int64, bool) {
	scale := int64(1)
	for _, unit := range binaryQuantityUnits {
		if digits, found := strings.CutSuffix(size, unit.suffix); found {
			size, scale = digits, unit.scale
			break
		}
	}
	count, err := strconv.ParseInt(size, 10, 64)
	if err != nil {
		return 0, false
	}
	return count * scale, true
}
