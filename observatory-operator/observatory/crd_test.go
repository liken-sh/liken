package observatory

// These tests read the CRDs in deploy/ through the same code the API
// server runs: the conversion to the internal schema, the check of the
// whole definition, the structural validator, and the CEL validator.
// A CRD that the cluster would refuse, or a resource it would refuse,
// fails here first.

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/install"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	crdvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

// crdPath is the file that holds one kind's CRD.
func crdPath(kind Kind) string {
	return filepath.Join("..", "deploy", kind.Plural+"-crd.yaml")
}

func loadCRD(t *testing.T, kind Kind) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	raw, err := os.ReadFile(crdPath(kind))
	if err != nil {
		t.Fatal(err)
	}
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.UnmarshalStrict(raw, crd); err != nil {
		t.Fatalf("decoding %s: %v", crdPath(kind), err)
	}
	return crd
}

// schemaOf is the schema of the one version that a CRD serves.
func schemaOf(t *testing.T, kind Kind) *apiextensionsv1.JSONSchemaProps {
	t.Helper()
	return loadCRD(t, kind).Spec.Versions[0].Schema.OpenAPIV3Schema
}

func newScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	install.Install(scheme)
	return scheme
}

// internalSchema converts a CRD's schema to the internal type that the
// validators take, the same conversion the API server does.
func internalSchema(t *testing.T, kind Kind) *apiextensions.JSONSchemaProps {
	t.Helper()
	internal := &apiextensions.JSONSchemaProps{}
	if err := newScheme().Convert(schemaOf(t, kind), internal, nil); err != nil {
		t.Fatalf("converting the schema of %s: %v", kind.Name, err)
	}
	return internal
}

// The deploy/ directory holds one CRD for each kind and nothing else
// that a test does not name, so a CRD with no Go type fails here.
func TestDeployHoldsOneCRDForEachKind(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "deploy", "*-crd.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, kind := range Kinds {
		want = append(want, crdPath(kind))
	}
	slices.Sort(files)
	slices.Sort(want)
	if !slices.Equal(files, want) {
		t.Errorf("CRD files = %v, want %v", files, want)
	}
}

func TestEachCRDNamesItsKind(t *testing.T) {
	for _, kind := range Kinds {
		t.Run(kind.Name, func(t *testing.T) {
			crd := loadCRD(t, kind)
			names := crd.Spec.Names
			cases := []struct{ field, got, want string }{
				{"metadata.name", crd.Name, kind.Plural + "." + Group},
				{"group", crd.Spec.Group, Group},
				{"scope", string(crd.Spec.Scope), string(apiextensionsv1.NamespaceScoped)},
				{"kind", names.Kind, kind.Name},
				{"listKind", names.ListKind, kind.Name + "List"},
				{"plural", names.Plural, kind.Plural},
				{"singular", names.Singular, strings.ToLower(kind.Name)},
				{"categories", strings.Join(names.Categories, ","), "astro"},
			}
			for _, c := range cases {
				if c.got != c.want {
					t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
				}
			}
		})
	}
}

// Each CRD serves one version, stores it, and splits the status into
// its own subresource, so the operator's status writes never touch a
// spec a person wrote.
func TestEachCRDServesOneVersionWithAStatusSubresource(t *testing.T) {
	for _, kind := range Kinds {
		t.Run(kind.Name, func(t *testing.T) {
			versions := loadCRD(t, kind).Spec.Versions
			if len(versions) != 1 {
				t.Fatalf("%d versions, want 1", len(versions))
			}
			v := versions[0]
			if v.Name != Version || !v.Served || !v.Storage {
				t.Errorf("version %s served=%v storage=%v, want %s served and stored",
					v.Name, v.Served, v.Storage, Version)
			}
			if v.Subresources == nil || v.Subresources.Status == nil {
				t.Error("no status subresource")
			}
		})
	}
}

// The short names, and the names that kubectl already answers for
// built-in kinds and the kinds of common add-ons. A short name that
// collides makes kubectl pick one of the two kinds without saying so.
var shortNames = map[string][]string{
	"Observatory": {"obs"}, "Telescope": {"tel"}, "OpticalTube": {"ota"},
	"OpticalTrain": {"train"}, "Mount": {"mnt"}, "Camera": {"cam"},
	"FilterWheel": {"fw"}, "Focuser": {"foc"}, "Rotator": {"rot"},
	"DustCap": {"cap"}, "FlatPanel": {"flat"}, "PolarAligner": {"pac"},
	"GPS": nil, "Dome": nil, "WeatherStation": {"weather"},
	"SkyQualityMeter": {"sqm"}, "Switch": {"sw"}, "Receiver": {"rx"},
	"Guider": nil, "Reservation": {"rsv"},
}

var takenNames = []string{
	// Built-in kinds.
	"cm", "cs", "csr", "cj", "crd", "crds", "deploy", "ds", "ep", "ev",
	"hpa", "ing", "limits", "netpol", "no", "ns", "pc", "pdb", "po", "psp",
	"pv", "pvc", "quota", "rc", "rs", "sa", "sc", "sts", "svc", "ipaddr",
	// Add-ons: Flux, cert-manager, Prometheus, snapshots, and liken.
	"ks", "hr", "gitrepo", "helmrepo", "ocirepo", "hc", "ea", "cert",
	"certs", "cr", "crs", "prom", "smon", "pmon", "am", "vs", "vsc",
	"vsclass", "tv",
}

func TestTheShortNamesAreUniqueAndFree(t *testing.T) {
	seen := map[string]string{}
	for _, kind := range Kinds {
		names := loadCRD(t, kind).Spec.Names
		if !slices.Equal(names.ShortNames, shortNames[kind.Name]) {
			t.Errorf("%s shortNames = %v, want %v", kind.Name, names.ShortNames, shortNames[kind.Name])
		}
		for _, name := range append([]string{names.Singular, names.Plural}, names.ShortNames...) {
			if other, held := seen[name]; held {
				t.Errorf("%s and %s both answer %q", other, kind.Name, name)
			}
			seen[name] = kind.Name
		}
		for _, name := range names.ShortNames {
			if slices.Contains(takenNames, name) {
				t.Errorf("%s's short name %q is taken", kind.Name, name)
			}
		}
	}
}

// The API server checks the whole definition before it serves it,
// and that check compiles each CEL rule and estimates its cost. A rule
// that the estimator refuses makes the cluster refuse the whole CRD.
func TestTheAPIServerWouldAcceptEachCRD(t *testing.T) {
	for _, kind := range Kinds {
		t.Run(kind.Name, func(t *testing.T) {
			internal := &apiextensions.CustomResourceDefinition{}
			if err := newScheme().Convert(loadCRD(t, kind), internal, nil); err != nil {
				t.Fatal(err)
			}
			// The API server fills the stored versions in when it creates
			// the definition; a manifest states none.
			internal.Status.StoredVersions = []string{Version}
			if errs := crdvalidation.ValidateCustomResourceDefinition(t.Context(), internal); len(errs) > 0 {
				t.Errorf("the API server would refuse the definition: %v", errs)
			}
		})
	}
}

// `kubectl explain` prints each field's description, and the
// descriptions hold the units, so every field has one.
func TestEveryFieldHasADescription(t *testing.T) {
	for _, kind := range Kinds {
		t.Run(kind.Name, func(t *testing.T) {
			for _, path := range undescribed(schemaOf(t, kind), "") {
				t.Errorf("%s has no description", path)
			}
		})
	}
}

func undescribed(schema *apiextensionsv1.JSONSchemaProps, path string) []string {
	var missing []string
	if path != "" && strings.TrimSpace(schema.Description) == "" {
		missing = append(missing, path)
	}
	for name, property := range schema.Properties {
		missing = append(missing, undescribed(&property, path+"."+name)...)
	}
	if schema.Items != nil && schema.Items.Schema != nil && schema.Items.Schema.Type == "object" {
		missing = append(missing, undescribed(schema.Items.Schema, path+"[]")...)
	}
	return missing
}

// A printer column whose path names no field prints an empty column
// with no error, so each path must resolve in the schema. A column of
// type string prints any value, so only the other types must match.
func TestEachPrinterColumnNamesAField(t *testing.T) {
	filter := regexp.MustCompile(`\[[^\]]*\]`)
	for _, kind := range Kinds {
		t.Run(kind.Name, func(t *testing.T) {
			crd := loadCRD(t, kind)
			schema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema
			columns := crd.Spec.Versions[0].AdditionalPrinterColumns
			if !slices.ContainsFunc(columns, func(c apiextensionsv1.CustomResourceColumnDefinition) bool {
				return c.Name == "Age" && c.Priority == 0
			}) {
				t.Error("no Age column in the default view")
			}
			for _, column := range columns {
				if column.JSONPath == ".metadata.creationTimestamp" {
					continue
				}
				path := filter.ReplaceAllString(column.JSONPath, "[]")
				if field := resolve(schema, strings.Split(strings.TrimPrefix(path, "."), ".")); field == nil {
					t.Errorf("column %s: %s names no field", column.Name, column.JSONPath)
				} else if column.Type != "string" && field.Type != column.Type {
					t.Errorf("column %s is %s, and %s is %s", column.Name, column.Type, column.JSONPath, field.Type)
				}
			}
		})
	}
}

// resolve walks a column's path through the schema. A step that ends
// in [] steps into the array's items.
func resolve(schema *apiextensionsv1.JSONSchemaProps, steps []string) *apiextensionsv1.JSONSchemaProps {
	for _, step := range steps {
		name, array := strings.CutSuffix(step, "[]")
		property, held := schema.Properties[name]
		if !held {
			return nil
		}
		schema = &property
		if array {
			if schema.Items == nil || schema.Items.Schema == nil {
				return nil
			}
			schema = schema.Items.Schema
		}
	}
	return schema
}
