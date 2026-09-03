package main

// providercheck.go holds the one call the operator makes against each
// MetadataProvider per pass, the Ready condition it writes from the answer,
// and the resolution of a Library's ordered sources to the provider that
// serves a concern.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"time"
)

// The endpoint the check calls. It is the cheapest authenticated read TMDb
// serves, so the check costs one request and names no title.
const (
	defaultProviderBase   = "https://api.themoviedb.org"
	tmdbConfigurationPath = "/3/configuration"
)

// The check must not hold a pass open. It is a variable so a test drives a
// short one.
var providerCheckTimeout = 10 * time.Second

// Every provider the pass read, keyed the way the report desk keys a Library,
// with the verdict this pass wrote on each.
type providerSet map[string]*MetadataProvider

// The provider a Library's sources resolve to for one concern: the first
// named provider that exists, is Ready, and lists the concern.
func (s providerSet) serving(namespace string, sources []string, concern string) *MetadataProvider {
	for _, name := range sources {
		provider, held := s[libraryKey(namespace, name)]
		if held && provider.ready() && provider.serves(concern) {
			return provider
		}
	}
	return nil
}

// What one check learned, or an empty reason for an answer that says nothing
// about the account.
type providerVerdict struct {
	reason  string
	message string
}

// The pass checks every provider once and answers with the set the Libraries
// are reconciled against. A check that fails is reported, and the provider
// keeps the verdict it carried.
func (o *operator) checkProviders(ctx context.Context, providers []MetadataProvider, now time.Time) providerSet {
	set := providerSet{}
	for index := range providers {
		provider := &providers[index]
		set[libraryKey(provider.Metadata.Namespace, provider.Metadata.Name)] = provider
		if err := o.checkProvider(ctx, provider, now); err != nil {
			fmt.Fprintf(os.Stderr, "checking the metadata provider %s/%s: %v\n",
				provider.Metadata.Namespace, provider.Metadata.Name, err)
		}
	}
	return set
}

// An empty verdict leaves the last condition standing. Two answers still
// produce one: a status that is neither 200 nor 401, and a Secret the API
// server would not serve.
func (o *operator) checkProvider(ctx context.Context, provider *MetadataProvider, now time.Time) error {
	verdict, err := o.reachProvider(ctx, provider)
	if verdict.reason == "" {
		return err
	}
	desired := deriveProviderStatus(provider, verdict, now)
	same, err := sameStatus(provider.Status, desired)
	if err != nil || same {
		return err
	}
	provider.Status = desired
	_, err = PutMetadataProviderStatus(ctx, o.client, provider)
	if errors.Is(err, ErrConflict) {
		// Something wrote the provider between the list and this write. The next
		// pass reads it again.
		return nil
	}
	return err
}

// The whole status of one provider from its verdict alone. The refusal time
// stands until another refusal replaces it, so a person reads when the key
// last failed even after it works again.
func deriveProviderStatus(provider *MetadataProvider, verdict providerVerdict, now time.Time) MetadataProviderStatus {
	status := MetadataProviderStatus{LastRefusal: provider.Status.LastRefusal}
	if verdict.reason == reasonRefused {
		status.LastRefusal = now
	}
	condition := Condition{
		Type:               conditionReady,
		Status:             ConditionFalse,
		ObservedGeneration: provider.Metadata.Generation,
		Reason:             verdict.reason,
		Message:            verdict.message,
	}
	if verdict.reason == reasonReachable {
		condition.Status = ConditionTrue
	}
	status.Conditions = SetCondition(slices.Clone(provider.Status.Conditions), condition, now)
	return status
}

// What each answer means: 200 is the account working, 401 is the provider
// refusing the key, no answer at all is Unreachable, and every other status
// leaves the last verdict.
func (o *operator) reachProvider(ctx context.Context, provider *MetadataProvider) (providerVerdict, error) {
	if provider.Spec.TMDb == nil {
		return providerVerdict{reason: reasonNoSecret,
			message: "the provider names no tmdb block"}, nil
	}
	reference := provider.Spec.TMDb.SecretRef
	secret, err := GetSecret(ctx, o.client, provider.Metadata.Namespace, reference.Name)
	if errors.Is(err, ErrNotFound) {
		return providerVerdict{reason: reasonNoSecret,
			message: fmt.Sprintf("the Secret %s does not exist in namespace %s",
				reference.Name, provider.Metadata.Namespace)}, nil
	}
	if err != nil {
		return providerVerdict{}, err
	}
	key := string(secret.Data[reference.secretKey()])
	if key == "" {
		return providerVerdict{reason: reasonNoSecret,
			message: fmt.Sprintf("the Secret %s holds no %s", reference.Name, reference.secretKey())}, nil
	}

	status, err := o.askProvider(ctx, key)
	if err != nil {
		return providerVerdict{reason: reasonUnreachable, message: err.Error()}, nil
	}
	switch status {
	case http.StatusOK:
		return providerVerdict{reason: reasonReachable,
			message: "the provider answered the configuration call"}, nil
	case http.StatusUnauthorized:
		return providerVerdict{reason: reasonRefused,
			message: "the provider refused the key in " + reference.Name}, nil
	}
	return providerVerdict{}, fmt.Errorf("the provider answered %d", status)
}

// The request carries a timeout of its own, so a provider that stops
// answering costs the pass its check and no more. The key travels in the form
// its shape names.
func (o *operator) askProvider(ctx context.Context, key string) (int, error) {
	asking, done := context.WithTimeout(ctx, providerCheckTimeout)
	defer done()

	request, err := http.NewRequestWithContext(asking, http.MethodGet,
		o.providerBase+tmdbConfigurationPath, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", jsonContentType)
	authorizeTMDb(request, key)

	response, err := o.providerClient.Do(request)
	if err != nil {
		return 0, err
	}
	drain(response.Body)
	return response.StatusCode, nil
}

// The Sources condition's reasons: every named provider resolves, one does
// not exist, the provider that serves a concern is not Ready, or no named
// provider serves a concern this library needs.
const (
	conditionSources = "Sources"

	reasonSourcesReady     = "SourcesReady"
	reasonProviderNotFound = "ProviderNotFound"
	reasonProviderNotReady = "ProviderNotReady"
	reasonConcernNotServed = "ConcernNotServed"
)

// What the Library's sources resolved to, in the shape a binding takes. An
// empty reason is a Library that names no source, and that Library carries no
// Sources condition at all.
type sourcesVerdict struct {
	reason  string
	message string
}

// The verdict on one Library's ordered sources. The concerns a Library needs
// from a provider are identity alone in this plan, so a list where none
// serves identity is a list that fills no gap.
func checkSources(library *Library, providers providerSet) sourcesVerdict {
	namespace := library.Metadata.Namespace
	if len(library.Spec.Sources) == 0 {
		return sourcesVerdict{}
	}
	for _, name := range library.Spec.Sources {
		if _, held := providers[libraryKey(namespace, name)]; !held {
			return sourcesVerdict{
				reason: reasonProviderNotFound,
				message: fmt.Sprintf("the MetadataProvider %s does not exist in namespace %s",
					name, namespace),
			}
		}
	}
	if providers.serving(namespace, library.Spec.Sources, concernIdentity) == nil {
		return unservedVerdict(namespace, library.Spec.Sources, providers, concernIdentity)
	}
	return sourcesVerdict{
		reason:  reasonSourcesReady,
		message: "the sources serve the concerns this library needs",
	}
}

// A list that fills no gap has two reasons, because they call for two
// repairs. A provider that lists the concern and failed its check is a key or
// a Secret to repair, and a list where no provider lists the concern at all
// is a source to add. The message names the provider and the reason its own
// check wrote.
func unservedVerdict(namespace string, sources []string, providers providerSet, concern string) sourcesVerdict {
	for _, name := range sources {
		provider, held := providers[libraryKey(namespace, name)]
		if !held || !provider.serves(concern) {
			continue
		}
		return sourcesVerdict{
			reason: reasonProviderNotReady,
			message: fmt.Sprintf("the MetadataProvider %s is not Ready, with the reason %s",
				name, provider.readyReason()),
		}
	}
	return sourcesVerdict{
		reason:  reasonConcernNotServed,
		message: "no source serves the " + concern + " concern",
	}
}
