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

// The call every PeerTube instance answers with no account. It is the
// cheapest read an instance serves, so the check costs one small request.
const peertubeCheckPath = "/api/v1/config"

// The search the trailer fact uses, asked for no rows at all, so the check
// reads that the collection answers and carries no result back.
const archiveCheckPath = archiveSearchPath +
	"?q=collection%3A" + archiveCollection + "&rows=0&output=json"

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

// Whether every source this Library names has a verdict. A provider the pass
// has listed but not checked yet reaches a Job as no block at all, and that
// Job answers a refresh entry with a partial source list and closes it, so the
// pass stands no Job of this Library until every verdict is written. A
// provider the check refused has its verdict and blocks nothing. A name that
// no MetadataProvider answers blocks nothing either, because no pass will ever
// write a verdict on an object that does not exist.
func (s providerSet) everySourceChecked(namespace string, sources []string) bool {
	for _, name := range sources {
		if provider, held := s[libraryKey(namespace, name)]; held && !provider.checked() {
			return false
		}
	}
	return true
}

// A group of facts resolves to the first provider that serves any one of
// them, which is what stands the container that runs the group.
func (s providerSet) servingAny(namespace string, sources, facts []string) *MetadataProvider {
	for _, fact := range facts {
		if provider := s.serving(namespace, sources, fact); provider != nil {
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
		started := time.Now()
		err := o.checkProvider(ctx, provider, now)
		o.metrics.observeReconcile(kindMetadataProvider, time.Since(started), err)
		if err != nil {
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

// What each answer means. 200 is the account working, 401 is the provider
// refusing the key, no HTTP answer at all is Unreachable, and every other
// status is Unavailable. A provider that is down says nothing about the
// account, and the check still writes a verdict, because every Job of a
// Library that names this provider waits for one.
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

	status, err := o.askProvider(ctx, provider, key)
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
	return providerVerdict{reason: reasonUnavailable,
		message: fmt.Sprintf("the provider answered %d", status)}, nil
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
func (o *operator) askProvider(ctx context.Context, provider *MetadataProvider, key string) (int, error) {
	asking, done := context.WithTimeout(ctx, providerCheckTimeout)
	defer done()

	reach := blockOf(provider.block()).reach
	request, err := http.NewRequestWithContext(asking, http.MethodGet,
		o.providerBase(provider)+reach.path, nil)
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

// The address the check calls for one provider: the endpoint the block
// names where it names one, and the fixed address of the service where it
// does not. A test replaces the fixed addresses; an endpoint in the spec
// stands as written.
func (o *operator) providerBase(provider *MetadataProvider) string {
	if endpoint := provider.endpoint(); endpoint != "" {
		return endpoint
	}
	return o.providerBases[provider.block()]
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

// The reason an entry of status.sources reports when the namespace holds no
// MetadataProvider of that name at all. A missing object has no Ready
// condition, so this reason is the operator's own. Every other reason in the
// list is the provider's.
const reasonMissing = "Missing"

// What one name in spec.sources resolved to on this pass: the block of the
// MetadataProvider that answers for it, whether the provider passed its last
// check, and the reason its Ready condition reports. The list is written to
// status.sources so a person reads which providers the Jobs receive without
// opening a pod's environment.
type librarySource struct {
	Name   string `json:"name"`
	Block  string `json:"block,omitempty"`
	Ready  bool   `json:"ready"`
	Reason string `json:"reason,omitempty"`
}

// One entry per name in spec.sources, in spec order, from the same providerSet
// the pass resolves the blocks and the endpoints from, so the status reports
// the sources the Jobs of this pass receive. A name that spec.sources repeats
// gets one entry per repeat, which is why the list is atomic and not keyed by
// name.
func resolveSources(library *Library, providers providerSet) []librarySource {
	var resolved []librarySource
	for _, name := range library.Spec.Sources {
		entry := librarySource{Name: name, Reason: reasonMissing}
		if provider, held := providers[libraryKey(library.Metadata.Namespace, name)]; held {
			entry.Block = provider.block()
			entry.Ready = provider.ready()
			entry.Reason = provider.readyReason()
		}
		resolved = append(resolved, entry)
	}
	return resolved
}

// The ready sources counted against the sources named, in the form 6/6. The
// SOURCES column of kubectl get prints only this string, so a library that
// asks four of the six sources it names shows 4/6 there.
func sourcesSummary(resolved []librarySource) string {
	if len(resolved) == 0 {
		return ""
	}
	ready := 0
	for _, source := range resolved {
		if source.Ready {
			ready++
		}
	}
	return fmt.Sprintf("%d/%d", ready, len(resolved))
}
