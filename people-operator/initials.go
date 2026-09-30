package main

// When a Person has no picture, the operator draws one: the person's
// initials in white on a colour taken from the Person's name. The
// operator draws it once, so every screen shows the same face for one
// person, and no screen needs a fallback of its own.

import (
	"bytes"
	"encoding/base64"
	"hash/fnv"
	"image"
	"image/color"
	"image/draw"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// palette holds the backdrop colours. Each one is dark enough that
// white text on it has a contrast ratio of at least 4.5 to 1, the WCAG
// AA level for text, so every person's initials read on every screen.
var palette = []color.RGBA{
	{0xB7, 0x1C, 0x1C, 0xFF}, // red
	{0xAD, 0x14, 0x57, 0xFF}, // pink
	{0x6A, 0x1B, 0x9A, 0xFF}, // purple
	{0x45, 0x27, 0xA0, 0xFF}, // deep purple
	{0x28, 0x35, 0x93, 0xFF}, // indigo
	{0x15, 0x65, 0xC0, 0xFF}, // blue
	{0x00, 0x69, 0x5C, 0xFF}, // teal
	{0x2E, 0x7D, 0x32, 0xFF}, // green
	{0x55, 0x6B, 0x2F, 0xFF}, // olive
	{0xBF, 0x36, 0x0C, 0xFF}, // deep orange
	{0x5D, 0x40, 0x37, 0xFF}, // brown
	{0x37, 0x47, 0x4F, 0xFF}, // blue grey
}

// personColour answers the backdrop of one Person. The hash is of the
// object's name, which never changes, so a person keeps one colour
// when they change their display name or nickname.
func personColour(name string) color.RGBA {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(name))
	return palette[hash.Sum32()%uint32(len(palette))]
}

// initials answers the letters a Person's picture shows: the first
// letter of the nickname, which is one word, or the first letters of
// the first and the last word of the display name.
func initials(spec personSpec) string {
	words := strings.Fields(spec.DisplayName)
	if spec.Nickname != "" {
		words = strings.Fields(spec.Nickname)[:1]
	}
	if len(words) == 0 {
		return ""
	}
	letters := firstLetter(words[0])
	if len(words) > 1 {
		letters += firstLetter(words[len(words)-1])
	}
	return letters
}

func firstLetter(word string) string {
	letter, _ := utf8.DecodeRuneInString(word)
	return string(unicode.ToUpper(letter))
}

// Go Bold is the face of the initials. It is part of
// golang.org/x/image as Go source, so the static binary in the
// operator's scratch image carries it, with no font file to install
// and no font to find at run time. Its BSD licence lets the project
// ship it. It covers Latin, Greek, and Cyrillic letters. A letter
// outside those draws as the font's empty box.
var initialsFont = mustParseFont(gobold.TTF)

func mustParseFont(ttf []byte) *opentype.Font {
	parsed, err := opentype.Parse(ttf)
	if err != nil {
		panic(err)
	}
	return parsed
}

// Two letters take a smaller size than one, so both fit the circle
// that the person picker cuts from the square.
const (
	oneLetterSize = 128
	twoLetterSize = 104
	initialsDPI   = 72
)

// drawInitials answers the thumbnail of a Person with no picture.
func drawInitials(p *person) string {
	out := image.NewRGBA(image.Rect(0, 0, thumbnailSide, thumbnailSide))
	draw.Draw(out, out.Bounds(), image.NewUniform(personColour(p.Metadata.Name)), image.Point{}, draw.Src)

	letters := initials(p.Spec)
	size := float64(oneLetterSize)
	if utf8.RuneCountInString(letters) > 1 {
		size = twoLetterSize
	}
	// NewFace fails only for options it cannot use, and these are
	// constants that it takes.
	face, _ := opentype.NewFace(initialsFont, &opentype.FaceOptions{Size: size, DPI: initialsDPI, Hinting: font.HintingFull})
	defer face.Close()

	writer := &font.Drawer{Dst: out, Src: image.White, Face: face}
	// The letters are centred on the square: across by their advance,
	// and down by the cap height of the face, which is the part of a
	// capital letter that shows.
	width := writer.MeasureString(letters)
	bounds, _ := font.BoundString(face, "H")
	capHeight := -bounds.Min.Y
	writer.Dot = fixed.Point26_6{
		X: (fixed.I(thumbnailSide) - width) / 2,
		Y: (fixed.I(thumbnailSide) + capHeight) / 2,
	}
	writer.DrawString(letters)
	return thumbnailURI(markInitials(encodeJPEG(out)))
}

// A failure never replaces a photograph with initials, so after a
// failed read the operator must tell a photograph from initials it
// drew. The status holds no other record of that, and a drawing
// compared with the initials of the current name misses a person
// renamed since. So the operator marks the initials it draws with a
// JPEG comment segment (COM, 0xFFFE) right after the start of the
// image. Every JPEG decoder skips a comment, so a screen draws the
// picture as it is.
var initialsMark = func() []byte {
	text := "people-operator initials"
	length := len(text) + 2
	return append([]byte{0xFF, 0xD8, 0xFF, 0xFE, byte(length >> 8), byte(length)}, text...)
}()

// markInitials puts the comment after the start-of-image marker, the
// first two bytes of every JPEG.
func markInitials(encoded []byte) []byte {
	return append(append([]byte{}, initialsMark...), encoded[2:]...)
}

// isInitials reports whether a thumbnail is initials the operator
// drew. The mark is 30 bytes, which base64 writes as the first 40
// characters of the data.
func isInitials(thumbnail string) bool {
	payload, found := strings.CutPrefix(thumbnail, thumbnailPrefix)
	head := base64.StdEncoding.EncodedLen(len(initialsMark))
	if !found || len(payload) < head {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(payload[:head])
	return err == nil && bytes.Equal(decoded, initialsMark)
}
