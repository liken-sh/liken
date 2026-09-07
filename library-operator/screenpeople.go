package main

// The people file a screen reads: the namespace's Person list, cut to
// the two fields the browser draws, written as one ConfigMap per screen
// namespace and mounted into every screen pod there. The browser holds
// no API credential, so the list reaches it as a file, and the kubelet
// rewrites the file within its sync period when the map changes, so a
// Person added later shows on the next ask with no pod restart.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

// The map's name in every screen namespace, the key the file is
// projected under, and where the pod mounts it.
const (
	peopleConfigMapName = "library-people"
	peopleFileName      = "people.json"
	peopleMountPath     = "/etc/library/people"
	peopleVolumeName    = "people"
)

// One entry of the file, in the shape the browser's audience reads.
type screenPerson struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// peopleFile is the file's text for these people, in name order so two
// passes over one list write one text. A Person with no display name
// is shown by its name.
func peopleFile(people []Person) (string, error) {
	entries := make([]screenPerson, 0, len(people))
	for index := range people {
		person := &people[index]
		if person.Metadata.deleting() {
			continue
		}
		display := person.Spec.DisplayName
		if display == "" {
			display = person.Metadata.Name
		}
		entries = append(entries, screenPerson{Name: person.Metadata.Name, DisplayName: display})
	}
	slices.SortFunc(entries, func(one, other screenPerson) int {
		return strings.Compare(one.Name, other.Name)
	})
	text, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	return string(text), nil
}

// The map for one screen namespace. It is owned by the namespace's one
// Catalog where there is one, so the garbage collector removes it with
// the Catalog, and it stands unowned where there is none.
func buildPeopleConfigMap(namespace string, owners []OwnerReference, people []Person) (*ConfigMap, error) {
	text, err := peopleFile(people)
	if err != nil {
		return nil, err
	}
	return &ConfigMap{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Metadata: ObjectMeta{
			Name:            peopleConfigMapName,
			Namespace:       namespace,
			Labels:          map[string]string{scannerLabelKey: screenLabelValue},
			OwnerReferences: owners,
		},
		Data: map[string]string{peopleFileName: text},
	}, nil
}

// standPeopleConfigMap creates the map where none stands and rewrites
// it where its text or its owners differ. Everything else on the live
// object is the API server's.
func (o *operator) standPeopleConfigMap(ctx context.Context, namespace string, owners []OwnerReference, people []Person) error {
	desired, err := buildPeopleConfigMap(namespace, owners, people)
	if err != nil {
		return err
	}
	live, err := GetConfigMap(ctx, o.client, namespace, peopleConfigMapName)
	if errors.Is(err, ErrNotFound) {
		_, err := CreateConfigMap(ctx, o.client, desired)
		if errors.Is(err, ErrConflict) {
			return nil
		}
		return err
	}
	if err != nil {
		return err
	}
	if live.Data[peopleFileName] == desired.Data[peopleFileName] &&
		slices.Equal(live.Metadata.OwnerReferences, desired.Metadata.OwnerReferences) {
		return nil
	}
	live.Metadata.OwnerReferences = desired.Metadata.OwnerReferences
	live.Data = desired.Data
	if _, err := UpdateConfigMap(ctx, o.client, live); err != nil {
		return fmt.Errorf("rewriting the people of %s: %w", namespace, err)
	}
	return nil
}

// The volume the screen pod projects the map as, and the path the
// browser reads. Optional, so a screen starts before the pass has
// written the map, with no people until it has.
func peopleVolume() Volume {
	optional := true
	return Volume{Name: peopleVolumeName, ConfigMap: &ConfigMapVolumeSource{
		Name:     peopleConfigMapName,
		Optional: &optional,
	}}
}

func peopleFilePath() string {
	return path.Join(peopleMountPath, peopleFileName)
}
