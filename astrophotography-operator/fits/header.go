// Package fits reads the parts of a FITS file that this operator needs:
// the header of an HDU, the numeric columns of a binary table, and
// whether a file holds all the data its headers declare.
//
// The format is FITS version 4.0. A file is a sequence of HDUs. Each
// HDU is a header of 80-character cards, in blocks of 2,880 bytes,
// followed by a data area padded to a whole number of blocks.
package fits

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	BlockSize = 2880
	cardSize  = 80
	// maxHeaderBlocks stops ReadHeader on input that is not FITS and so
	// has no END card. The largest header the operator reads, an .axy
	// file's, is four blocks.
	maxHeaderBlocks = 1000
)

// Card is one card of a header. Value is the text of a string without
// its quotes, or any other value as written. A COMMENT or HISTORY card
// has its text in Value.
type Card struct {
	Key   string
	Value string
}

// Header is the cards of one HDU in file order, without the END card.
type Header struct {
	Cards []Card
}

// ErrMissing is returned when a header has no card for a keyword.
var ErrMissing = errors.New("not in the header")

// ReadHeader reads one header from r, through the block that holds the
// END card, so r is then at the start of the HDU's data.
func ReadHeader(r io.Reader) (Header, error) {
	var h Header
	block := make([]byte, BlockSize)
	for range maxHeaderBlocks {
		if _, err := io.ReadFull(r, block); err != nil {
			return Header{}, fmt.Errorf("reading the FITS header: %w", err)
		}
		for start := 0; start < BlockSize; start += cardSize {
			card := block[start : start+cardSize]
			key := strings.TrimRight(string(card[:8]), " ")
			if key == "END" {
				return h, nil
			}
			if key == "" {
				continue
			}
			h.Cards = append(h.Cards, Card{Key: key, Value: value(card)})
		}
	}
	return Header{}, fmt.Errorf("the header has no END card in its first %d blocks, so the input is not FITS", maxHeaderBlocks)
}

// value returns the value of one card. A card has a value when columns
// 9 and 10 are "= ". A string value is in quotes, and two quotes in a
// row inside it are one quote. Any other value ends at the "/" that
// starts the card's comment. A card without "= ", such as COMMENT or
// HISTORY, returns all the text after the keyword.
func value(card []byte) string {
	if !bytes.Equal(card[8:10], []byte("= ")) {
		return strings.TrimSpace(string(card[8:]))
	}
	field := strings.TrimLeft(string(card[10:]), " ")
	if !strings.HasPrefix(field, "'") {
		text, _, _ := strings.Cut(field, "/")
		return strings.TrimSpace(text)
	}
	var text strings.Builder
	for i := 1; i < len(field); i++ {
		if field[i] != '\'' {
			text.WriteByte(field[i])
			continue
		}
		if i+1 < len(field) && field[i+1] == '\'' {
			text.WriteByte('\'')
			i++
			continue
		}
		break
	}
	// Trailing spaces in a string are not significant (section 4.2.1).
	return strings.TrimRight(text.String(), " ")
}

// Get returns the value of the first card with the key, and whether
// there is one.
func (h Header) Get(key string) (string, bool) {
	for _, c := range h.Cards {
		if c.Key == key {
			return c.Value, true
		}
	}
	return "", false
}

// String returns a keyword's text, or an error that wraps ErrMissing.
func (h Header) String(key string) (string, error) {
	text, found := h.Get(key)
	if !found {
		return "", fmt.Errorf("%s: %w", key, ErrMissing)
	}
	return text, nil
}

// Int returns a keyword's integer value. It fails when the keyword is
// missing or its value is not an integer.
func (h Header) Int(key string) (int64, error) {
	text, err := h.String(key)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("keyword %s holds %q, which is not an integer", key, text)
	}
	return n, nil
}

// Float returns a keyword's real value. FITS allows D as well as E to
// start an exponent, and strconv.ParseFloat accepts only E, so Float
// replaces D with E first.
func (h Header) Float(key string) (float64, error) {
	text, err := h.String(key)
	if err != nil {
		return 0, err
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(text, "D", "E"), 64)
	if err != nil {
		return 0, fmt.Errorf("keyword %s holds %q, which is not a number", key, text)
	}
	return f, nil
}

func (h Header) Comments() []string {
	var out []string
	for _, c := range h.Cards {
		if c.Key == "COMMENT" {
			out = append(out, c.Value)
		}
	}
	return out
}

// dataSize returns the size in bytes of the HDU's data, before the
// padding to a whole block: |BITPIX| / 8 × GCOUNT × (PCOUNT + NAXIS1 ×
// ... × NAXISn), from section 4.4.1. An HDU with NAXIS = 0 has no
// data.
func (h Header) dataSize() (int64, error) {
	bitpix, err := h.Int("BITPIX")
	if err != nil {
		return 0, err
	}
	axes, err := h.Int("NAXIS")
	if err != nil {
		return 0, err
	}
	if axes == 0 {
		return 0, nil
	}
	elements := int64(1)
	for axis := int64(1); axis <= axes; axis++ {
		n, err := h.Int(fmt.Sprintf("NAXIS%d", axis))
		if err != nil {
			return 0, err
		}
		elements *= n
	}
	pcount, gcount := h.optional("PCOUNT", 0), h.optional("GCOUNT", 1)
	return max(bitpix, -bitpix) / 8 * gcount * (pcount + elements), nil
}

// optional returns an integer keyword, or otherwise when the keyword is
// missing or not an integer.
func (h Header) optional(key string, otherwise int64) int64 {
	n, err := h.Int(key)
	if err != nil {
		return otherwise
	}
	return n
}

func padded(size int64) int64 {
	return (size + BlockSize - 1) / BlockSize * BlockSize
}
