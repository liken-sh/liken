package main

import (
	"slices"
	"testing"
)

func TestInitials(t *testing.T) {
	cases := []struct {
		name string
		spec personSpec
		want string
	}{
		{"one word", personSpec{DisplayName: "Ada"}, "A"},
		{"the first and the last word", personSpec{DisplayName: "Ada King Lovelace"}, "AL"},
		{"the nickname over the display name", personSpec{DisplayName: "Ada Lovelace", Nickname: "countess"}, "C"},
		{"a letter outside ASCII", personSpec{DisplayName: "élodie"}, "É"},
		{"no name", personSpec{DisplayName: "  "}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := initials(c.spec); got != c.want {
				t.Errorf("initials = %q, want %q", got, c.want)
			}
		})
	}
}

// A Person's colour depends only on its name, so it is the same on every
// pass and after a change of display name.
func TestAPersonKeepsOneColour(t *testing.T) {
	first, second := personColour("ada"), personColour("ada")
	if first != second || !slices.Contains(palette, first) {
		t.Errorf("personColour(ada) = %v, then %v, want one colour of the palette", first, second)
	}
}

// The initials drawing is filled with the Person's colour and carries
// the mark that tells it from a picture from a source.
func TestTheInitialsAreMarked(t *testing.T) {
	p := ada("")
	thumbnail := drawInitials(&p)

	if !isInitials(thumbnail) {
		t.Error("isInitials is false for the drawn initials, want true")
	}
	r, g, b, _ := decodeThumbnail(t, thumbnail).At(2, 2).RGBA()
	if got := colourOf(r, g, b); !near(got, personColour("ada")) {
		t.Errorf("the corner is %v, want the Person's colour %v", got, personColour("ada"))
	}
}

// A picture from a source is never detected as initials.
func TestAPictureIsNotInitials(t *testing.T) {
	photo, err := makeThumbnail(solid(t, 8, 8, green), blue)
	if err != nil {
		t.Fatalf("making the thumbnail of a picture: %v", err)
	}
	for _, thumbnail := range []string{photo, "", "data:image/jpeg;base64,/9j/"} {
		if isInitials(thumbnail) {
			t.Errorf("isInitials(%.40q) is true, want false", thumbnail)
		}
	}
}
