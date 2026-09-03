package main

// The .contributors/ store at a library root. What names a person's directory,
// what contributor.yaml holds, and how the credits fact creates an entry where
// none exists. The three contributor facts fill the entry, and
// contributorfacts.go holds them.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// The directory at the library root that holds one directory per person, and
// the three files an entry holds. The store is a dot name, so every ecosystem
// player skips it, and the walk reads it as an exception.
const (
	contributorsDirectory    = ".contributors"
	contributorFileName      = "contributor.yaml"
	contributorBiographyName = "biography.txt"
	contributorHeadshotName  = "headshot.jpg"
)

// The schemes a slug may take its suffix from, in the order the suffix prefers
// them, and the scheme every contributor gap keys on.
const contributorTMDbScheme = "tmdb"

var contributorSchemes = []string{contributorTMDbScheme, "imdb"}

// Contributor.yaml, the file the credits fact creates and the contributor.ids
// fact fills. The ids carry every scheme the providers gave, so one person
// joins across libraries by any of them.
type contributorFile struct {
	Name string      `yaml:"name"`
	IDs  providerIDs `yaml:"ids,omitempty"`
	Born string      `yaml:"born,omitempty"`
	Died string      `yaml:"died,omitempty"`
}

// The part a person took on the title. The part tells the cast from the crew,
// and the role, which only an actor carries, is the character they played.
const (
	creditPartActor    = "actor"
	creditPartDirector = "director"
	creditPartWriter   = "writer"
)

// One line of credits.yaml: the person, the part, the billing order, and the
// directory in .contributors/ the person's own files are in. The path is
// relative to the library root, which is the form the contributors table
// holds, so a reader joins the two with no rewriting.
type creditEntry struct {
	Name        string `yaml:"name"`
	Part        string `yaml:"part,omitempty"`
	Role        string `yaml:"role,omitempty"`
	Order       int    `yaml:"order"`
	Contributor string `yaml:"contributor,omitempty"`
}

// The slug that names a person's directory: the name in natural order, lower-
// cased, folded to ASCII, with every run of other characters becoming one
// hyphen. It is slug's own folding, the one the item slugs take, because a
// person reads the file tree and both names read the same way. A name that
// folds away to nothing keeps the person's provider id alone.
func contributorSlug(name string, ids providerIDs) string {
	if key := slug(name, 0); key != "" {
		return key
	}
	return contributorIDMark(ids)
}

// The suffix that tells two people of one slug apart: the scheme and the id,
// as in tmdb-31. The first scheme the person carries wins, and a person with
// no id at all carries no mark.
func contributorIDMark(ids providerIDs) string {
	for _, scheme := range contributorSchemes {
		if id := ids[scheme]; id != "" {
			return scheme + "-" + id
		}
	}
	return ""
}

// Where one slug's directory sits: under the first two characters of the
// slug. First letters of names bunch up, and one letter would hold thousands
// of the people a large library credits; two characters keep the biggest
// bucket near a thousand at thirty thousand people, at the cost of one
// directory read to reach a person. A slug of one character is its own
// bucket, and a hyphen in the second place stays, because the bucket is a
// prefix of the slug and nothing else.
func contributorDirectory(slug string) string {
	if slug == "" {
		return ""
	}
	return path.Join(contributorsDirectory, slug[:min(2, len(slug))], slug)
}

// Reads one contributor.yaml, with the bytes it read, which the ids fact
// hashes. A file that is not there is no error, because the credits fact
// creates it and every other fact reads it after.
func readContributorFile(file string) (contributorFile, []byte, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return contributorFile{}, nil, nil
	}
	if err != nil {
		return contributorFile{}, nil, err
	}
	var held contributorFile
	if err := yaml.Unmarshal(data, &held); err != nil {
		return contributorFile{}, nil, fmt.Errorf("reading %s: %w", file, err)
	}
	return held, data, nil
}

// The bytes of one entry, and the hash the ids fact records for them. Every
// writer of contributor.yaml marshals it the same way, so a file this operator
// wrote hashes to what its ledger holds, and a hand edit does not.
func marshalContributorFile(file contributorFile) []byte {
	// The marshal of this shape cannot fail, because every field of it is a
	// string or the ids map, and the ids marshal by hand into a flow mapping.
	data, _ := yaml.Marshal(file)
	return data
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Whether the entry at a slug is this person. The first scheme both carry
// decides it. An entry that carries no id under a scheme this credit holds is
// the same person, because nothing tells them apart and a store that split
// them would hold one person twice.
func (f contributorFile) isPerson(ids providerIDs) bool {
	for _, scheme := range contributorSchemes {
		held, mine := f.IDs[scheme], ids[scheme]
		if held == "" || mine == "" {
			continue
		}
		return held == mine
	}
	return true
}

// The directory one credited person's files sit in, created with
// contributor.yaml where none exists. The plain slug belongs to the first
// person written under it, and a second person of the same name takes the slug
// with the id suffix. The read of the entry that is already there is what
// tells the two apart, so the answer is the same whichever title reaches the
// person first, and a run over a library that already holds the person writes
// nothing.
func (e *enricher) contributorFor(person creditedPerson) (string, error) {
	slug := contributorSlug(person.Name, person.IDs)
	if slug == "" {
		return "", nil
	}
	candidates := []string{slug}
	if mark := contributorIDMark(person.IDs); mark != "" && mark != slug {
		candidates = append(candidates, slug+"-"+mark)
	}
	for _, candidate := range candidates {
		directory := contributorDirectory(candidate)
		held, data, err := readContributorFile(filepath.Join(e.root, directory, contributorFileName))
		if err != nil {
			return "", err
		}
		if data == nil {
			return directory, e.createContributor(directory, person)
		}
		if held.isPerson(person.IDs) {
			return directory, nil
		}
	}
	return "", nil
}

// The entry the credits fact creates: the name, and every id the provider gave
// at credit time. The create never lands on a file that exists, so a person
// another title wrote, or a person edited by hand, is left as it is.
func (e *enricher) createContributor(directory string, person creditedPerson) error {
	data := marshalContributorFile(contributorFile{Name: person.Name, IDs: person.IDs})
	_, err := e.writer.createInto(filepath.Join(e.root, directory), contributorFileName, data)
	return err
}

// The credits fact's second write: credits.yaml in the title's own .liken
// directory, which is the fact's ledger file, so the credits, the answer, and
// the attempts are one file with one writer. Every person named here has an
// entry in .contributors/ by the time it lands.
func (e *enricher) writeCredits(folder string, answer factAnswer) {
	entries := make([]creditEntry, 0, len(answer.Cast)+len(answer.Directors)+len(answer.Writers))
	for _, actor := range answer.Cast {
		entries = append(entries, creditEntry{
			Name: actor.Name, Part: creditPartActor, Role: actor.Role, Order: actor.Order,
			Contributor: e.contributorPath(actor.person()),
		})
	}
	// The crew take the orders after the cast. The order is the key of a credit
	// beside the title, and the cast holds the orders from zero up to its own
	// length, so a director who also acts holds two credits with two orders.
	for _, crew := range []struct {
		part   string
		people []creditedPerson
	}{
		{part: creditPartDirector, people: answer.Directors},
		{part: creditPartWriter, people: answer.Writers},
	} {
		for _, person := range crew.people {
			entries = append(entries, creditEntry{
				Name: person.Name, Part: crew.part, Order: len(entries),
				Contributor: e.contributorPath(person),
			})
		}
	}
	err := e.writer.updateLikenLedger(folder, factCredits, func(ledger *likenLedger) {
		ledger.Credits = entries
	})
	if err != nil {
		e.logf("could not write the credits at %s: %v", folder, err)
	}
}

// The directory of one credited person. Where the store cannot be read or
// written, the credit keeps the name and no directory, and the run goes on:
// the credit is the fact, and the directory is a link to it.
func (e *enricher) contributorPath(person creditedPerson) string {
	directory, err := e.contributorFor(person)
	if err != nil {
		e.logf("could not write the entry of %s: %v", person.Name, err)
	}
	return directory
}
