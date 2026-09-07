package main

// namespacecatalog.go holds the Catalog wire type, the namespaced resource
// that owns a namespace's shared catalog. A namespace has exactly one
// Catalog. It sizes the durable volume every catalog agent takes, and it
// owns the catalog Service and EndpointSlice. It is hand-written, like the
// Library type in api.go.

import (
	"fmt"
	"sort"
	"strings"
)

// The Catalog shares the Library's group and version.
const catalogAPIVersion = libraryAPIVersion

// A Catalog is the namespace's declaration of where the shared catalog is
// stored and how large each agent's copy is. The operator reads the spec
// and writes the status. The Go type is NamespaceCatalog because the
// Corrosion client in catalog.go already holds the Catalog name.
type NamespaceCatalog struct {
	APIVersion string        `json:"apiVersion,omitempty"`
	Kind       string        `json:"kind,omitempty"`
	Metadata   ObjectMeta    `json:"metadata"`
	Spec       CatalogSpec   `json:"spec"`
	Status     CatalogStatus `json:"status"`
}

type CatalogList struct {
	Metadata ListMeta           `json:"metadata"`
	Items    []NamespaceCatalog `json:"items"`
}

// CatalogSpec is the storage every catalog agent in the namespace uses,
// with room for the catalog-wide settings the design grows into.
type CatalogSpec struct {
	Storage CatalogStorage `json:"storage"`
	// The claim the progress store's pod takes.
	Progress CatalogProgress `json:"progress,omitzero"`
	// The claims each Library's scan and enrichment Jobs take.
	Libraries CatalogLibraries `json:"libraries,omitzero"`
	// The settings every screen pod in the namespace takes.
	Screens CatalogScreens `json:"screens,omitzero"`
}

// The progress store's claim. It has a size and a class of its own,
// because the progress rows are small next to the catalog, and a
// namespace that keeps its two central stores on a durable class does
// not want a catalog-sized volume for them. Each field defaults to the
// field of the same name under spec.storage.
type CatalogProgress struct {
	Size             string `json:"size,omitempty"`
	StorageClassName string `json:"storageClassName,omitempty"`
	// How many durable copies of the progress store the namespace stands.
	// The copies are peers, and each one holds a claim of its own.
	Replicas int `json:"replicas,omitempty"`
}

// The class each Library's scanner and enrichment claims bind to. A
// Library's claims are working copies: a Job rebuilds one from the
// catalog of record, so they belong on a local class when the catalog
// of record is on a durable one. There is no size, because every agent
// holds the whole catalog and takes spec.storage.size. An empty value
// defaults to spec.storage.storageClassName.
type CatalogLibraries struct {
	StorageClassName string `json:"storageClassName,omitempty"`
}

// The screens' half of the Catalog. StorageClassName classes both of a
// screen's claims, and it is a field of its own because the namespace's
// one durable copy and a screen's replica may want different classes.
// An empty value makes the operator omit the class, so the cluster's
// default StorageClass binds the claims. The catalog claim takes
// spec.storage.size and the art claim takes artCache.size, because the
// rows and the art have different sizes and different lives.
type CatalogScreens struct {
	StorageClassName string          `json:"storageClassName,omitempty"`
	ArtCache         CatalogArtCache `json:"artCache,omitzero"`
}

// The volume a screen's browser keeps its scaled art on, which it
// draws the wall from after a restart.
type CatalogArtCache struct {
	Size string `json:"size,omitempty"`
}

// Size is the one namespace-wide catalog volume size, because each agent
// holds the whole namespace's catalog. StorageClassName is optional. An
// empty StorageClassName makes the operator omit the class, so the
// cluster's default StorageClass binds the claim.
type CatalogStorage struct {
	Size             string `json:"size,omitempty"`
	StorageClassName string `json:"storageClassName,omitempty"`
	// The claim the first copy of the catalog mounts in place of one the
	// operator provisions, for a namespace whose catalog volume a person
	// makes themselves. Every copy after the first takes a claim the
	// operator provisions.
	ClaimName string `json:"claimName,omitempty"`
	// How many durable copies of the catalog the namespace stands. The
	// copies are peers that Corrosion syncs from one another, and each one
	// holds a claim of its own.
	Replicas int `json:"replicas,omitempty"`
}

// The copies a Catalog stands when it asks for none. One copy is what a
// namespace stood before the field existed.
const defaultStoreReplicas = 1

// How many copies of the catalog the Catalog asks for. The API server
// defaults the field, so a zero is a Catalog read from anywhere but the
// API server, and it takes the same default.
func catalogReplicaCount(catalog *NamespaceCatalog) int {
	if catalog.Spec.Storage.Replicas > 0 {
		return catalog.Spec.Storage.Replicas
	}
	return defaultStoreReplicas
}

// How many copies of the progress store the Catalog asks for.
func progressReplicaCount(catalog *NamespaceCatalog) int {
	if catalog.Spec.Progress.Replicas > 0 {
		return catalog.Spec.Progress.Replicas
	}
	return defaultStoreReplicas
}

// The cluster the Catalog stands, so a person reads one object to see the
// namespace's catalog: the member agent pods, the storage the agents were
// given, the durable copies of each store, the screens and the claims
// they run on, and the conditions.
type CatalogStatus struct {
	Members     []string        `json:"members,omitempty"`
	StorageSize string          `json:"storageSize,omitempty"`
	Replicas    CatalogReplicas `json:"replicas,omitzero"`
	Screens     []CatalogScreen `json:"screens,omitempty"`
	Conditions  []Condition     `json:"conditions,omitempty"`
}

// The durable copies of the namespace's two stores: how many are up, and
// how many the Catalog asks for.
type CatalogReplicas struct {
	Catalog  StoreReplicas `json:"catalog,omitzero"`
	Progress StoreReplicas `json:"progress,omitzero"`
}

// One store's copies. Ready counts the copies the kubelet reports up, and
// Wanted is the count the Catalog asks for.
type StoreReplicas struct {
	Ready  int `json:"ready"`
	Wanted int `json:"wanted"`
}

// One screen pod of the namespace: the Player it draws for, the claim
// its catalog agent runs on, the claim its art cache is on, the node it
// runs on, and its phase. A screen in a namespace this operator
// provisions no claims for names neither.
type CatalogScreen struct {
	Player   string `json:"player,omitempty"`
	Claim    string `json:"claim,omitempty"`
	ArtClaim string `json:"artClaim,omitempty"`
	Node     string `json:"node,omitempty"`
	Phase    string `json:"phase,omitempty"`
}

// The default catalog volume size. A catalog of movies and series is
// megabytes, and a catalog of a photo library in the millions is low
// gigabytes, so the default is small.
const defaultCatalogSize = "1Gi"

// catalogStorageSize resolves the size the agents take: the Catalog's own
// value, or the small default when it names none.
func catalogStorageSize(catalog *NamespaceCatalog) string {
	if catalog.Spec.Storage.Size != "" {
		return catalog.Spec.Storage.Size
	}
	return defaultCatalogSize
}

// progressStorageSize resolves the size the progress claim takes: the
// spec.progress value, or the catalog's size when it names none.
func progressStorageSize(catalog *NamespaceCatalog) string {
	if catalog.Spec.Progress.Size != "" {
		return catalog.Spec.Progress.Size
	}
	return catalogStorageSize(catalog)
}

// progressStorageClass resolves the class the progress claim binds to:
// the spec.progress value, or the catalog's class when it names none.
// An empty result makes the builder omit the class, so the cluster's
// default StorageClass binds the claim.
func progressStorageClass(catalog *NamespaceCatalog) string {
	if catalog.Spec.Progress.StorageClassName != "" {
		return catalog.Spec.Progress.StorageClassName
	}
	return catalog.Spec.Storage.StorageClassName
}

// libraryStorageClass resolves the class a Library's scanner and
// enrichment claims bind to: the spec.libraries value, or the
// catalog's class when it names none. An empty result makes the
// builders omit the class, so the cluster's default StorageClass binds
// the claims.
func libraryStorageClass(catalog *NamespaceCatalog) string {
	if catalog.Spec.Libraries.StorageClassName != "" {
		return catalog.Spec.Libraries.StorageClassName
	}
	return catalog.Spec.Storage.StorageClassName
}

// The default art cache size. Scaled art is larger than the rows it
// belongs to, so the default is above the catalog's.
const defaultArtCacheSize = "2Gi"

// artCacheSize is the size a screen's art claim takes: the Catalog's
// own value, or the default when it names none.
func artCacheSize(catalog *NamespaceCatalog) string {
	if catalog.Spec.Screens.ArtCache.Size != "" {
		return catalog.Spec.Screens.ArtCache.Size
	}
	return defaultArtCacheSize
}

// The condition this operator publishes on a Catalog, and the reasons
// it takes. Ready reports the cluster the Catalog stands.
const (
	catalogConditionReady = "Ready"

	catalogReasonStanding     = "Standing"
	catalogReasonManyCatalogs = "ManyCatalogs"
	// The catalog pod has not started, and the catalog pod
	// failed.
	catalogReasonPodPending = "PodPending"
	catalogReasonPodFailed  = "PodFailed"
)

// catalogChoice is the namespace's single Catalog, or the reason a Library
// cannot proceed without exactly one. It is a binding-like value: a nil
// catalog carries the reason and message a Library's Ready condition
// reports.
type catalogChoice struct {
	catalog *NamespaceCatalog
	// The pod that Catalog stands, as the pass read it, or nil
	// when it does not stand yet.
	pod     *Pod
	reason  string
	message string
}

// singleCatalog reduces a namespace's Catalog objects to the one the
// operator uses. There are three answers: no Catalog, exactly one, and more
// than one. The operator uses the single one and refuses to stand two.
func singleCatalog(catalogs []*NamespaceCatalog) catalogChoice {
	switch len(catalogs) {
	case 0:
		return catalogChoice{reason: reasonNoCatalog, message: "the namespace has no Catalog"}
	case 1:
		return catalogChoice{catalog: catalogs[0]}
	default:
		return catalogChoice{reason: reasonManyCatalogs, message: manyCatalogsMessage(catalogs)}
	}
}

// manyCatalogsMessage names the conflict a person reads to fix it: the
// count and the names of the Catalogs in the namespace.
func manyCatalogsMessage(catalogs []*NamespaceCatalog) string {
	names := make([]string, 0, len(catalogs))
	for _, catalog := range catalogs {
		names = append(names, catalog.Metadata.Name)
	}
	sort.Strings(names)
	return fmt.Sprintf("the namespace has %d Catalogs (%s); the operator stands none until one remains",
		len(catalogs), strings.Join(names, ", "))
}

// catalogsByNamespace groups the cluster's Catalogs by namespace, each list
// sorted by name, so the choice and its messages read the same way every
// pass.
func catalogsByNamespace(catalogs []NamespaceCatalog) map[string][]*NamespaceCatalog {
	byNamespace := map[string][]*NamespaceCatalog{}
	for index := range catalogs {
		catalog := &catalogs[index]
		byNamespace[catalog.Metadata.Namespace] = append(byNamespace[catalog.Metadata.Namespace], catalog)
	}
	for namespace := range byNamespace {
		list := byNamespace[namespace]
		sort.Slice(list, func(one, other int) bool {
			return list[one].Metadata.Name < list[other].Metadata.Name
		})
	}
	return byNamespace
}
