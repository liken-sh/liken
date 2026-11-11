package main

// billing.go is the credits fact's second record of a title's people:
// every person the provider credited, with the department and the job. A
// screen shows the billed cast, the directors, and the writers, and
// credits.yaml and the catalog hold those. The titles fact looks for the
// names of a title's people in the text of its end titles, and most of the
// names there are the people credits.yaml leaves out: the producers, the
// composer, the cinematographer, the editor, and the rest of the crew. So
// the fact writes them to .liken/billing.yaml beside credits.yaml, where the
// walk reads nothing, and the catalog stays the size it was.
//
// The file keeps the department and the job of each person, so a reader
// can take the heads of some departments and leave the rest without
// another ask of the provider.

import (
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const billingFileName = "billing.yaml"

// One line of billing.yaml. A person with two jobs holds two lines.
type billedPerson struct {
	Name       string `yaml:"name"`
	Department string `yaml:"department,omitempty"`
	Job        string `yaml:"job,omitempty"`
}

type billingFile struct {
	Billing []billedPerson `yaml:"billing"`
}

// The department and the job that TMDb's credits give no words for: a cast
// member holds a character and no job.
const (
	billingActingDepartment = "Acting"
	billingActorJob         = "Actor"
)

// Every person of TMDb's credits: the whole cast in its billing order, with
// no cut, and then the whole crew in the order TMDb states it. A person
// with no name is left out, because a credit names a person.
func tmdbBilling(answer tmdbCredits) []billedPerson {
	var billing []billedPerson
	for _, member := range answer.Cast {
		billing = append(billing, billedPerson{
			Name: member.Name, Department: billingActingDepartment, Job: billingActorJob,
		})
	}
	for _, member := range answer.Crew {
		for _, job := range member.jobs() {
			billing = append(billing, billedPerson{Name: member.Name, Department: member.Department, Job: job})
		}
	}
	return unionBilling(nil, billing)
}

// The billing is a set keyed by the person, the department, and the job,
// so two providers that credit one person for one job make one line.
func unionBilling(held, adding []billedPerson) []billedPerson {
	for _, person := range adding {
		person = billedPerson{
			Name:       strings.TrimSpace(person.Name),
			Department: strings.TrimSpace(person.Department),
			Job:        strings.TrimSpace(person.Job),
		}
		if person.Name == "" || billed(held, person) {
			continue
		}
		held = append(held, person)
	}
	return held
}

func billed(held []billedPerson, person billedPerson) bool {
	for _, other := range held {
		if other == person {
			return true
		}
	}
	return false
}

// The write of billing.yaml in the title's .liken directory. An answer
// with no billing leaves the file as it is: the IMDb datasets and TVmaze
// state no crew here, and a title one of them answered after TMDb keeps
// the crew TMDb gave.
func (e *enricher) writeBilling(folder string, billing []billedPerson) {
	if len(billing) == 0 {
		return
	}
	data, err := yaml.Marshal(billingFile{Billing: billing})
	if err == nil {
		_, err = e.writer.updateInto(filepath.Join(folder, likenDirectory), billingFileName,
			func([]byte) ([]byte, error) { return data, nil })
	}
	if err != nil {
		e.logf("could not write the billing at %s: %v", e.named(folder), err)
	}
}
