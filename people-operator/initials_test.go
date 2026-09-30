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

// A Person's colour comes from its name, so it is the same on every
// pass and after a change of display name.
func TestAPersonKeepsOneColour(t *testing.T) {
	first, second := personColour("ada"), personColour("ada")
	if first != second || !slices.Contains(palette, first) {
		t.Errorf("personColour(ada) = %v, then %v, want one colour of the palette", first, second)
	}
}

// The initials fill the square with the Person's colour, and carry the
// mark that tells them from a photograph.
func TestTheInitialsAreMarked(t *testing.T) {
	p := ada("")
	thumbnail := drawInitials(&p)

	if !isInitials(thumbnail) {
		t.Error("the initials carry no mark")
	}
	r, g, b, _ := decodeThumbnail(t, thumbnail).At(2, 2).RGBA()
	if got := colourOf(r, g, b); !near(got, personColour("ada")) {
		t.Errorf("the corner is %v, want the Person's colour %v", got, personColour("ada"))
	}
}

// A picture from a source never reads as initials.
func TestAPictureIsNotInitials(t *testing.T) {
	photo, err := bakeThumbnail(solid(t, 8, 8, green), blue)
	if err != nil {
		t.Fatal(err)
	}
	for _, thumbnail := range []string{photo, "", "data:image/jpeg;base64,/9j/"} {
		if isInitials(thumbnail) {
			t.Errorf("%.40q reads as initials", thumbnail)
		}
	}
}
