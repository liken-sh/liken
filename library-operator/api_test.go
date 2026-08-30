package main

// These tests cover the three places the wire types do more than
// carry a field: the condition list's transition rule, the settings
// block a kind selects, and the decoder that names what serves a
// PersistentVolume.

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

var (
	firstSeen = time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	laterSeen = time.Date(2026, 8, 29, 10, 30, 0, 0, time.UTC)
)

// A condition that keeps its verdict keeps its transition time, so
// kubectl answers how long the library has been Ready. A condition
// that flips takes the new time, and either way the reason and the
// message are the ones this pass wrote.
func TestSetConditionMovesTheTimeOnlyWhenTheVerdictFlips(t *testing.T) {
	cases := []struct {
		name     string
		standing []Condition
		written  Condition
		want     time.Time
	}{
		{
			name:    "a new type takes this pass's time",
			written: Condition{Type: conditionReady, Status: ConditionFalse, Reason: reasonNoReport},
			want:    laterSeen,
		},
		{
			name: "the same verdict holds the standing time",
			standing: []Condition{
				{Type: conditionReady, Status: ConditionTrue, Reason: reasonReady, LastTransitionTime: firstSeen},
			},
			written: Condition{Type: conditionReady, Status: ConditionTrue, Reason: reasonReady},
			want:    firstSeen,
		},
		{
			name: "a flipped verdict takes this pass's time",
			standing: []Condition{
				{Type: conditionReady, Status: ConditionTrue, Reason: reasonReady, LastTransitionTime: firstSeen},
			},
			written: Condition{Type: conditionReady, Status: ConditionFalse, Reason: reasonPodFailed},
			want:    laterSeen,
		},
		{
			name: "another type is left where it stands",
			standing: []Condition{
				{Type: conditionBound, Status: ConditionTrue, Reason: reasonBound, LastTransitionTime: firstSeen},
			},
			written: Condition{Type: conditionReady, Status: ConditionTrue, Reason: reasonReady},
			want:    laterSeen,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			conditions := SetCondition(testCase.standing, testCase.written, laterSeen)

			ready := conditions[len(conditions)-1]
			if ready.Type != conditionReady {
				t.Fatalf("the last condition is %q, want %q", ready.Type, conditionReady)
			}
			if !ready.LastTransitionTime.Equal(testCase.want) {
				t.Errorf("lastTransitionTime = %v, want %v", ready.LastTransitionTime, testCase.want)
			}
			if ready.Reason != testCase.written.Reason {
				t.Errorf("reason = %q, want %q", ready.Reason, testCase.written.Reason)
			}
		})
	}
}

// One type holds one entry. A second write of the same type replaces
// the first rather than appending beside it, which is the rule the
// CRD's list-type map enforces on the server.
func TestSetConditionKeepsOneEntryPerType(t *testing.T) {
	conditions := SetCondition(nil, Condition{Type: conditionBound, Status: ConditionFalse}, firstSeen)
	conditions = SetCondition(conditions, Condition{Type: conditionReady, Status: ConditionFalse}, firstSeen)
	conditions = SetCondition(conditions, Condition{Type: conditionBound, Status: ConditionTrue}, laterSeen)

	if len(conditions) != 2 {
		t.Fatalf("%d conditions, want 2", len(conditions))
	}
	if conditions[0].Status != ConditionTrue {
		t.Errorf("the Bound verdict is %q, want True", conditions[0].Status)
	}
}

// The kind selects the block, and a kind with no block of its own
// selects nothing. The scanner receives what this answers.
func TestTheSettingsBlockFollowsTheKind(t *testing.T) {
	movies := &LibrarySettings{Image: "ghcr.io/example/movies:1"}
	series := &LibrarySettings{Image: "ghcr.io/example/series:1"}
	cases := []struct {
		name string
		spec LibrarySpec
		want *LibrarySettings
	}{
		{name: "movies", spec: LibrarySpec{Kind: libraryKindMovies, Movies: movies}, want: movies},
		{name: "series", spec: LibrarySpec{Kind: libraryKindSeries, Series: series}, want: series},
		{name: "a kind this build does not serve", spec: LibrarySpec{Kind: "photos"}, want: nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.spec.settings(); got != testCase.want {
				t.Errorf("settings = %v, want %v", got, testCase.want)
			}
		})
	}
}

// The spec carries the ignore list as a top-level array, so the walk
// reads the folders to skip whatever kind the library holds.
func TestLibrarySpecDecodesTheIgnoreList(t *testing.T) {
	var spec LibrarySpec
	if err := json.Unmarshal([]byte(`{"kind":"movies","ignore":["#recycle",".incoming"]}`), &spec); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(spec.Ignore, []string{"#recycle", ".incoming"}) {
		t.Errorf("ignore = %v, want the two folders", spec.Ignore)
	}
}

// The decoder names the volume's source key, whichever driver serves
// it, and reads an NFS volume in full because a media reference over
// NFS is the server and the export path.
func TestAPersistentVolumeNamesWhatServesIt(t *testing.T) {
	cases := []struct {
		name       string
		spec       string
		wantSource string
		wantServer string
		wantPath   string
	}{
		{
			name:       "an NFS export",
			spec:       `{"capacity":{"storage":"8Ti"},"accessModes":["ReadOnlyMany"],"nfs":{"server":"movies.example","path":"/volume1/movies"}}`,
			wantSource: "nfs",
			wantServer: "movies.example",
			wantPath:   "/volume1/movies",
		},
		{
			name:       "a CSI driver this operator does not know",
			spec:       `{"storageClassName":"fast","csi":{"driver":"example.csi","volumeHandle":"pvc-1"}}`,
			wantSource: "csi",
		},
		{
			name:       "a disk on one node",
			spec:       `{"local":{"path":"/mnt/movies"},"nodeAffinity":{},"volumeMode":"Filesystem"}`,
			wantSource: "local",
		},
		{
			name: "settings and nothing that serves them",
			spec: `{"claimRef":{"name":"movies"},"persistentVolumeReclaimPolicy":"Retain"}`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			volume := &PersistentVolume{}
			if err := json.Unmarshal([]byte(`{"spec":`+testCase.spec+`}`), volume); err != nil {
				t.Fatal(err)
			}

			if volume.Spec.Source != testCase.wantSource {
				t.Errorf("source = %q, want %q", volume.Spec.Source, testCase.wantSource)
			}
			if testCase.wantServer == "" {
				return
			}
			if volume.Spec.NFS.Server != testCase.wantServer {
				t.Errorf("server = %q, want %q", volume.Spec.NFS.Server, testCase.wantServer)
			}
			if volume.Spec.NFS.Path != testCase.wantPath {
				t.Errorf("path = %q, want %q", volume.Spec.NFS.Path, testCase.wantPath)
			}
		})
	}
}

// A spec the decoder cannot read is an error the caller reports, not a
// volume with an empty source.
func TestAPersistentVolumeSpecThatIsNotAnObjectFails(t *testing.T) {
	cases := []struct {
		name string
		spec string
	}{
		{name: "the spec is a string", spec: `"nfs"`},
		{name: "the source is not an object", spec: `{"nfs":"movies.example:/volume1/movies"}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := json.Unmarshal([]byte(`{"spec":`+testCase.spec+`}`), &PersistentVolume{})

			if err == nil {
				t.Fatal("err = nil, want the decoder's own failure")
			}
		})
	}
}

// The catalog agent gossips on the pod's own address, and nothing
// knows that address until the kubelet starts the pod, so the operator
// declares the variable and the downward API fills it. The keys here
// are the ones the kubelet reads, so the test writes them out in full
// and reads them back.
func TestAnEnvVarCarriesADownwardAPIReference(t *testing.T) {
	cases := []struct {
		name     string
		variable EnvVar
		want     string
	}{
		{
			name:     "a literal value",
			variable: EnvVar{Name: "LIBRARY_KIND", Value: libraryKindMovies},
			want:     `{"name":"LIBRARY_KIND","value":"movies"}`,
		},
		{
			name: "the pod's own address",
			variable: EnvVar{Name: "POD_IP", ValueFrom: &EnvVarSource{
				FieldRef: &ObjectFieldSelector{FieldPath: "status.podIP"},
			}},
			want: `{"name":"POD_IP","valueFrom":{"fieldRef":{"fieldPath":"status.podIP"}}}`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			written, err := json.Marshal(testCase.variable)
			if err != nil {
				t.Fatal(err)
			}
			if string(written) != testCase.want {
				t.Errorf("marshalled %s, want %s", written, testCase.want)
			}

			read := EnvVar{}
			if err := json.Unmarshal(written, &read); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(read, testCase.variable) {
				t.Errorf("read back %+v, want %+v", read, testCase.variable)
			}
		})
	}
}
