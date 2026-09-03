package main

// providercheck.go holds the one call the operator makes against each
// MetadataProvider per pass, the Ready condition it writes from the answer,
// and the resolution of a Library's ordered sources to the provider that
// serves a fact.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"time"
)

// The one call each provider answers for the check: the path, and how the key
// travels on it. A provider that takes no key authorizes nothing. The call is
// the cheapest read each provider serves, so a pass costs one request per
// account.
type providerReach struct {
	path      string
	authorize func(*http.Request, string)
}

var providerReaches = map[string]providerReach{
	providerBlockTMDb:   {path: tmdbConfigurationPath, authorize: authorizeTMDb},
	providerBlockOMDb:   {path: omdbCheckPath, authorize: authorizeParameter(omdbAPIKeyParameter)},
	providerBlockFanart: {path: fanartCheckPath, authorize: authorizeParameter(fanartAPIKeyParam)},
	providerBlockTVmaze: {path: tvmazeCheckPath},
}

// The address of each provider the check calls, which a test replaces with a
// server of its own.
func defaultProviderBases() map[string]string {
	return map[string]string{
		providerBlockTMDb:   tmdbAPIBase,
		providerBlockOMDb:   omdbAPIBase,
		providerBlockFanart: fanartAPIBase,
		providerBlockTVmaze: tvmazeAPIBase,
	}
}

// A key that travels as a query parameter, in the shape the check calls an
// authorization in.
func authorizeParameter(name string) func(*http.Request, string) {
	return func(request *http.Request, key string) {
		queryKey(name, key)(request)
	}
}

// The check must not hold a pass open. It is a variable so a test drives a
// short one.
var providerCheckTimeout = 10 * time.Second

// Every provider the pass read, keyed the way the report desk keys a Library,
// with the verdict this pass wrote on each.
type providerSet map[string]*MetadataProvider

// The provider a Library's sources resolve to for one fact: the first
// named provider that exists, is Ready, and lists the fact.
func (s providerSet) serving(namespace string, sources []string, fact string) *MetadataProvider {
	for _, name := range sources {
		provider, held := s[libraryKey(namespace, name)]
		if held && provider.ready() && provider.serves(fact) {
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
// last failed even after it works again. The facts are what the provider
// serves right now, so a provider that is not Ready reports none, and the
// list reads as what this provider can be asked for today.
func deriveProviderStatus(provider *MetadataProvider, verdict providerVerdict, now time.Time) MetadataProviderStatus {
	status := MetadataProviderStatus{
		LastRefusal: provider.Status.LastRefusal,
		Provider:    provider.block(),
	}
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
		status.Facts = provider.servedFacts()
	}
	status.Conditions = SetCondition(slices.Clone(provider.Status.Conditions), condition, now)
	return status
}

// What each answer means: 200 is the account working, 401 is the provider
// refusing the key, no answer at all is Unreachable, and every other status
// leaves the last verdict.
func (o *operator) reachProvider(ctx context.Context, provider *MetadataProvider) (providerVerdict, error) {
	block := provider.block()
	if block == "" {
		return providerVerdict{reason: reasonNoSecret,
			message: "the provider names no block"}, nil
	}
	// A provider that takes no key skips the Secret, because TVmaze serves its
	// free tier to anyone.
	key, verdict, err := o.providerKey(ctx, provider)
	if verdict.reason != "" || err != nil {
		return verdict, err
	}

	status, err := o.askProvider(ctx, block, key)
	if err != nil {
		return providerVerdict{reason: reasonUnreachable, message: err.Error()}, nil
	}
	switch status {
	case http.StatusOK:
		return providerVerdict{reason: reasonReachable,
			message: "the provider answered the check call"}, nil
	case http.StatusUnauthorized:
		return providerVerdict{reason: reasonRefused,
			message: "the provider refused the key of " + block}, nil
	}
	return providerVerdict{}, fmt.Errorf("the provider answered %d", status)
}

// The key of one provider, out of the Secret its block names. An empty key
// and an empty verdict together are a provider that needs none.
func (o *operator) providerKey(ctx context.Context, provider *MetadataProvider) (string, providerVerdict, error) {
	reference := provider.secretRef()
	if reference == nil {
		return "", providerVerdict{}, nil
	}
	secret, err := GetSecret(ctx, o.client, provider.Metadata.Namespace, reference.Name)
	if errors.Is(err, ErrNotFound) {
		return "", providerVerdict{reason: reasonNoSecret,
			message: fmt.Sprintf("the Secret %s does not exist in namespace %s",
				reference.Name, provider.Metadata.Namespace)}, nil
	}
	if err != nil {
		return "", providerVerdict{}, err
	}
	key := string(secret.Data[reference.secretKey()])
	if key == "" {
		return "", providerVerdict{reason: reasonNoSecret,
			message: fmt.Sprintf("the Secret %s holds no %s", reference.Name, reference.secretKey())}, nil
	}
	return key, providerVerdict{}, nil
}

// The request carries a timeout of its own, so a provider that stops
// answering costs the pass its check and no more. The key travels in the form
// its shape names.
func (o *operator) askProvider(ctx context.Context, block, key string) (int, error) {
	asking, done := context.WithTimeout(ctx, providerCheckTimeout)
	defer done()

	reach := providerReaches[block]
	request, err := http.NewRequestWithContext(asking, http.MethodGet,
		o.providerBases[block]+reach.path, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", jsonContentType)
	if reach.authorize != nil {
		reach.authorize(request, key)
	}

	response, err := o.providerClient.Do(request)
	if err != nil {
		return 0, err
	}
	drain(response.Body)
	return response.StatusCode, nil
}

// The Sources condition's reasons: every named provider resolves, one does
// not exist, the provider that serves a fact is not Ready, or no named
// provider serves a fact this library needs.
const (
	conditionSources = "Sources"

	reasonSourcesReady     = "SourcesReady"
	reasonProviderNotFound = "ProviderNotFound"
	reasonProviderNotReady = "ProviderNotReady"
	reasonFactNotServed    = "FactNotServed"
)

// What the Library's sources resolved to, in the shape a binding takes. An
// empty reason is a Library that names no source, and that Library carries no
// Sources condition at all.
type sourcesVerdict struct {
	reason  string
	message string
}

// The verdict on one Library's ordered sources. The facts a Library needs
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
	if providers.serving(namespace, library.Spec.Sources, factIdentity) == nil {
		return unservedVerdict(namespace, library.Spec.Sources, providers, factIdentity)
	}
	return sourcesVerdict{
		reason:  reasonSourcesReady,
		message: "the sources serve the facts this library needs",
	}
}

// A list that fills no gap has two reasons, because they call for two
// repairs. A provider that lists the fact and failed its check is a key or
// a Secret to repair, and a list where no provider lists the fact at all
// is a source to add. The message names the provider and the reason its own
// check wrote.
func unservedVerdict(namespace string, sources []string, providers providerSet, fact string) sourcesVerdict {
	for _, name := range sources {
		provider, held := providers[libraryKey(namespace, name)]
		if !held || !provider.serves(fact) {
			continue
		}
		return sourcesVerdict{
			reason: reasonProviderNotReady,
			message: fmt.Sprintf("the MetadataProvider %s is not Ready, with the reason %s",
				name, provider.readyReason()),
		}
	}
	return sourcesVerdict{
		reason:  reasonFactNotServed,
		message: "no source serves the " + fact + " fact",
	}
}
