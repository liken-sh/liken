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
}

// Size is the one namespace-wide catalog volume size, because each agent
// holds the whole namespace's catalog. StorageClassName is optional. An
// empty StorageClassName makes the operator omit the class, so the
// cluster's default StorageClass binds the claim.
type CatalogStorage struct {
	Size             string `json:"size,omitempty"`
	StorageClassName string `json:"storageClassName,omitempty"`
}

// CatalogStatus reports the cluster the Catalog stands, so a person reads
// one object to see the namespace's catalog: the member agent pods, the
// storage the agents were given, and the conditions.
type CatalogStatus struct {
	Members     []string    `json:"members,omitempty"`
	StorageSize string      `json:"storageSize,omitempty"`
	Conditions  []Condition `json:"conditions,omitempty"`
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

// The condition this operator publishes on a Catalog, and the reasons
// it takes. Ready reports the cluster the Catalog stands.
const (
	catalogConditionReady = "Ready"

	catalogReasonStanding     = "Standing"
	catalogReasonManyCatalogs = "ManyCatalogs"
)

// catalogChoice is the namespace's single Catalog, or the reason a Library
// cannot proceed without exactly one. It is a binding-like value: a nil
// catalog carries the reason and message a Library's Ready condition
// reports.
type catalogChoice struct {
	catalog *NamespaceCatalog
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
