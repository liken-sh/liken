package main

// When a Person has no picture, the operator draws one: the person's
// initials in white on a colour chosen from the Person's name. The
// drawing goes in status.thumbnail like a picture from a source, so every screen
// shows the same image and no screen needs a fallback of its own.

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
// AA level for text, so the initials stay readable on any of them.
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

// personColour returns the backdrop colour for one Person. It hashes
// metadata.name, which does not change, so the colour stays the same
// when the person edits their display name or nickname.
func personColour(name string) color.RGBA {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(name))
	return palette[hash.Sum32()%uint32(len(palette))]
}

// initials returns the letters to draw for a Person. A nickname gives
// the first letter of its first word. Without one, the display name
// gives the first letters of its first and last words. The CRD accepts
// a nickname of only spaces, which has no word, so such a nickname
// counts as none.
func initials(spec personSpec) string {
	words := strings.Fields(spec.DisplayName)
	if nickname := strings.Fields(spec.Nickname); len(nickname) > 0 {
		words = nickname[:1]
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

// initialsFont is Go Bold. golang.org/x/image ships it as Go source, so
// it is compiled into the binary and the scratch image needs no font
// file. It covers Latin, Greek, and Cyrillic letters. Any other letter
// draws as the font's empty box.
var initialsFont = mustParseFont(gobold.TTF)

func mustParseFont(ttf []byte) *opentype.Font {
	parsed, err := opentype.Parse(ttf)
	if err != nil {
		panic(err)
	}
	return parsed
}

// Two letters use a smaller size than one so that both fit inside the
// circle that the person picker crops from the square.
const (
	oneLetterSize = 128
	twoLetterSize = 104
	initialsDPI   = 72
)

func drawInitials(p *person) string {
	out := image.NewRGBA(image.Rect(0, 0, thumbnailSide, thumbnailSide))
	draw.Draw(out, out.Bounds(), image.NewUniform(personColour(p.Metadata.Name)), image.Point{}, draw.Src)

	letters := initials(p.Spec)
	size := float64(oneLetterSize)
	if utf8.RuneCountInString(letters) > 1 {
		size = twoLetterSize
	}
	// NewFace fails only for options it cannot use, and these constant
	// options are valid.
	face, _ := opentype.NewFace(initialsFont, &opentype.FaceOptions{Size: size, DPI: initialsDPI, Hinting: font.HintingFull})
	defer face.Close()

	writer := &font.Drawer{Dst: out, Src: image.White, Face: face}
	// Horizontally, the text is centred by its advance width. Vertically,
	// it is centred by the height of a capital H, because initials are
	// capitals and have no descenders.
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

// initialsMark is a JPEG comment segment (COM, 0xFFFE) that goes right
// after the start-of-image marker of every initials drawing. After a
// failed read, the operator keeps a real picture but redraws initials.
// The status has no field that says which one it holds, so the
// operator marks its own drawings. JPEG decoders skip comment segments.
var initialsMark = func() []byte {
	text := "people-operator initials"
	length := len(text) + 2
	return append([]byte{0xFF, 0xD8, 0xFF, 0xFE, byte(length >> 8), byte(length)}, text...)
}()

// markInitials replaces the start-of-image marker, the first two bytes
// of a JPEG, with initialsMark, which begins with the same marker.
func markInitials(encoded []byte) []byte {
	return append(append([]byte{}, initialsMark...), encoded[2:]...)
}

// isInitials decodes only the base64 characters that cover the mark.
func isInitials(thumbnail string) bool {
	payload, found := strings.CutPrefix(thumbnail, thumbnailPrefix)
	head := base64.StdEncoding.EncodedLen(len(initialsMark))
	if !found || len(payload) < head {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(payload[:head])
	return err == nil && bytes.Equal(decoded, initialsMark)
}
