package main

import "testing"

// Each entry of the people file carries the thumbnail people-operator
// wrote into the Person's status, so the picker draws a face. A Person
// with no thumbnail, on a cluster where people-operator does not run,
// has no thumbnail key, and the picker draws its letter.
func TestPeopleFileCarriesTheThumbnailOfEachPerson(t *testing.T) {
	tests := []struct {
		name   string
		person Person
		want   string
	}{
		{
			name: "a thumbnail",
			person: Person{
				Metadata: ObjectMeta{Name: "ada"},
				Spec:     PersonSpec{DisplayName: "Ada"},
				Status:   PersonStatus{Thumbnail: "data:image/jpeg;base64,/9j/"},
			},
			want: `[{"name":"ada","displayName":"Ada","thumbnail":"data:image/jpeg;base64,/9j/"}]`,
		},
		{
			name:   "no thumbnail",
			person: Person{Metadata: ObjectMeta{Name: "ada"}, Spec: PersonSpec{DisplayName: "Ada"}},
			want:   `[{"name":"ada","displayName":"Ada"}]`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := peopleFile([]Person{test.person})
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Errorf("people = %s, want %s", got, test.want)
			}
		})
	}
}
