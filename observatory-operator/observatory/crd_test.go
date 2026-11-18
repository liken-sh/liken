package observatory

// These tests load the CRDs in deploy/ with the same code that the API
// server runs: the conversion to the internal schema, the validation of
// the whole definition, the structural validator, and the CEL
// validator. A CRD or a resource that a cluster would refuse fails
// here first.

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

func crdPath(kind Kind) string {
	return filepath.Join("..", "deploy", kind.Plural+"-crd.yaml")
}

func loadCRD(t *testing.T, kind Kind) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	raw, err := os.ReadFile(crdPath(kind))
	if err != nil {
		t.Fatalf("reading the CRD file of %s: %v", kind.Name, err)
	}
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.UnmarshalStrict(raw, crd); err != nil {
		t.Fatalf("decoding %s: %v", crdPath(kind), err)
	}
	return crd
}

// schemaOf returns the schema of the only version that a CRD serves.
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
// validators take, as the API server does.
func internalSchema(t *testing.T, kind Kind) *apiextensions.JSONSchemaProps {
	t.Helper()
	internal := &apiextensions.JSONSchemaProps{}
	if err := newScheme().Convert(schemaOf(t, kind), internal, nil); err != nil {
		t.Fatalf("converting the schema of %s: %v", kind.Name, err)
	}
	return internal
}

// The deploy/ directory has one CRD for each kind and no other CRD
// file, so a CRD with no matching Go kind fails here.
func TestDeployHoldsOneCRDForEachKind(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "deploy", "*-crd.yaml"))
	if err != nil {
		t.Fatalf("listing the CRD files in deploy: %v", err)
	}
	var want []string
	for _, kind := range Kinds {
		want = append(want, crdPath(kind))
	}
	slices.Sort(files)
	slices.Sort(want)
	if !slices.Equal(files, want) {
		t.Errorf("deploy holds the CRD files %v, want %v", files, want)
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

// Each CRD serves one version, stores that version, and has a status
// subresource, so the operator's status writes cannot change a spec
// that a person wrote. A kind with no status has no subresource.
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
			if hasSubresource := v.Subresources != nil && v.Subresources.Status != nil; hasSubresource != kind.HasStatus() {
				t.Errorf("status subresource = %v, want %v", hasSubresource, kind.HasStatus())
			}
		})
	}
}

// shortNames lists each kind's short names. takenNames lists the names
// that built-in kinds and the kinds of common add-ons already use. When
// a short name collides with one of them, kubectl picks one of the two
// kinds without reporting the conflict.
var shortNames = map[string][]string{
	"Observatory": {"obs"}, "Telescope": {"tel"}, "OpticalTube": {"ota"},
	"OpticalTrain": {"train"}, "Mount": {"mnt"}, "Camera": {"cam"},
	"FilterWheel": {"fw"}, "Focuser": {"foc"}, "Rotator": {"rot"},
	"DustCap": {"cap"}, "FlatPanel": {"flat"}, "PolarAligner": {"pac"},
	"GPS": nil, "Dome": nil, "WeatherStation": {"weather"},
	"Switch": {"sw"}, "Receiver": {"rx"},
	"Guider": nil, "Reservation": {"rsv"}, "Horizon": nil,
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
				t.Errorf("%s and %s both use the name %q", other, kind.Name, name)
			}
			seen[name] = kind.Name
		}
		for _, name := range names.ShortNames {
			if slices.Contains(takenNames, name) {
				t.Errorf("the short name %q of %s is already used by another resource", name, kind.Name)
			}
		}
	}
}

// selectableFields lists, by kind, the fields that a list or a watch can
// select on. Each kind with a parent selects on the parent's field, so
// `kubectl get cam --field-selector spec.opticalTrain=east-imaging`
// lists one train's cameras. A Reservation also selects on its holder,
// so astrophotography-operator can list and watch only its own
// reservations. TestTheAPIServerWouldAcceptEachCRD checks that each
// path names a field of a type that the API server can select on.
var selectableFields = map[string][]string{
	"Observatory": nil, "Telescope": {".spec.observatory"},
	"OpticalTube": {".spec.telescope"}, "OpticalTrain": {".spec.telescope"},
	"Mount": {".spec.telescope"}, "Camera": {".spec.opticalTrain"},
	"FilterWheel": {".spec.opticalTrain"}, "Focuser": {".spec.opticalTrain"},
	"Rotator": {".spec.opticalTrain"}, "DustCap": {".spec.opticalTrain"},
	"FlatPanel": {".spec.opticalTrain"}, "PolarAligner": {".spec.telescope"},
	"GPS": {".spec.telescope"}, "Dome": {".spec.observatory"},
	"WeatherStation": {".spec.observatory"},
	"Switch":         {".spec.telescope", ".spec.observatory"},
	"Receiver":       {".spec.telescope", ".spec.observatory"},
	"Guider":         {".spec.telescope"},
	"Reservation":    {".spec.telescope", ".spec.holder"},
	"Horizon":        nil,
}

func TestEachCRDSelectsOnItsFields(t *testing.T) {
	for _, kind := range Kinds {
		t.Run(kind.Name, func(t *testing.T) {
			var got []string
			for _, field := range loadCRD(t, kind).Spec.Versions[0].SelectableFields {
				got = append(got, field.JSONPath)
			}
			if !slices.Equal(got, selectableFields[kind.Name]) {
				t.Errorf("selectableFields = %v, want %v", got, selectableFields[kind.Name])
			}
		})
	}
}

// The API server validates the whole definition before it serves it.
// That validation compiles each CEL rule and estimates its cost, and a
// rule whose estimated cost is too high makes the API server refuse
// the whole CRD.
func TestTheAPIServerWouldAcceptEachCRD(t *testing.T) {
	for _, kind := range Kinds {
		t.Run(kind.Name, func(t *testing.T) {
			internal := &apiextensions.CustomResourceDefinition{}
			if err := newScheme().Convert(loadCRD(t, kind), internal, nil); err != nil {
				t.Fatalf("converting the CRD of %s to the internal type: %v", kind.Name, err)
			}
			// The API server sets the stored versions when it creates
			// the definition, so a manifest does not have them.
			internal.Status.StoredVersions = []string{Version}
			if errs := crdvalidation.ValidateCustomResourceDefinition(t.Context(), internal); len(errs) > 0 {
				t.Errorf("the API server would refuse the CRD of %s: %v", kind.Name, errs)
			}
		})
	}
}

// `kubectl explain` prints each field's description, and the
// descriptions give the units, so every field needs one.
func TestEveryFieldHasADescription(t *testing.T) {
	for _, kind := range Kinds {
		t.Run(kind.Name, func(t *testing.T) {
			for _, path := range undescribed(schemaOf(t, kind), "") {
				t.Errorf("the schema field %s has no description", path)
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

// A printer column whose path matches no field prints an empty column
// with no error, so each path must resolve in the schema. A column of
// type string prints any value, so only the other types must match the
// field's type.
func TestEachPrinterColumnMatchesAField(t *testing.T) {
	filter := regexp.MustCompile(`\[[^\]]*\]`)
	for _, kind := range Kinds {
		t.Run(kind.Name, func(t *testing.T) {
			crd := loadCRD(t, kind)
			schema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema
			columns := crd.Spec.Versions[0].AdditionalPrinterColumns
			if !slices.ContainsFunc(columns, func(c apiextensionsv1.CustomResourceColumnDefinition) bool {
				return c.Name == "Age" && c.Priority == 0
			}) {
				t.Error("the default view has no Age column")
			}
			for _, column := range columns {
				if column.JSONPath == ".metadata.creationTimestamp" {
					continue
				}
				path := filter.ReplaceAllString(column.JSONPath, "[]")
				if field := resolve(schema, strings.Split(strings.TrimPrefix(path, "."), ".")); field == nil {
					t.Errorf("the JSONPath %s of column %s matches no field of the schema", column.JSONPath, column.Name)
				} else if column.Type != "string" && field.Type != column.Type {
					t.Errorf("column %s has type %s, want %s, the type of the field at %s", column.Name, column.Type, field.Type, column.JSONPath)
				}
			}
		})
	}
}

// resolve follows a column's path through the schema. A step that ends
// in [] continues into the array's items.
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

// `kubectl apply` sends a custom resource a JSON merge patch, which
// replaces a whole list when it differs from the live one. The API
// server writes a default inside a list's items into the stored list,
// so the live list never equals the list in the file, and every apply
// of an unchanged file reports the resource as configured. So no schema
// has a default inside a list, and the operator applies those defaults
// itself.
func TestNoCRDStatesADefaultInsideAList(t *testing.T) {
	for _, kind := range Kinds {
		t.Run(kind.Name, func(t *testing.T) {
			for _, path := range listDefaults(schemaOf(t, kind), "", false) {
				t.Errorf("the schema field %s sets a default inside a list", path)
			}
		})
	}
}

func TestListDefaultsFindsADefaultInsideAList(t *testing.T) {
	half := apiextensionsv1.JSON{Raw: []byte("0.5")}
	schema := &apiextensionsv1.JSONSchemaProps{Properties: map[string]apiextensionsv1.JSONSchemaProps{
		"top": {Type: "number", Default: &half},
		"actions": {Type: "array", Items: &apiextensionsv1.JSONSchemaPropsOrArray{Schema: &apiextensionsv1.JSONSchemaProps{
			Type: "object", Properties: map[string]apiextensionsv1.JSONSchemaProps{
				"within": {Type: "number", Default: &half},
			},
		}}},
	}}
	if got := listDefaults(schema, "", false); !slices.Equal(got, []string{".actions[].within"}) {
		t.Errorf("listDefaults = %v, want [.actions[].within]", got)
	}
}

// listDefaults returns the path of each default under an array's items.
// A default outside every list is allowed.
func listDefaults(schema *apiextensionsv1.JSONSchemaProps, path string, inList bool) []string {
	var found []string
	if inList && schema.Default != nil {
		found = append(found, path)
	}
	for name, property := range schema.Properties {
		found = append(found, listDefaults(&property, path+"."+name, inList)...)
	}
	if schema.Items != nil && schema.Items.Schema != nil {
		found = append(found, listDefaults(schema.Items.Schema, path+"[]", true)...)
	}
	if schema.AdditionalProperties != nil && schema.AdditionalProperties.Schema != nil {
		found = append(found, listDefaults(schema.AdditionalProperties.Schema, path+"{}", inList)...)
	}
	return found
}
