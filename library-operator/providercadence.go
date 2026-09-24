package main

// providercadence.go decides when the operator calls a provider's check.
// A pass reads every MetadataProvider, and a pass runs at least every ten
// seconds, so a check on every pass would spend the provider's allowance on
// the check alone: OMDb's free key is a thousand calls a day, and a check
// every ten seconds is 8640. The check therefore calls the provider only when
// its verdict can have changed. The Ready condition stays the record a person
// reads. The operator holds its own note of the last call in memory, so an
// operator that starts again calls every provider once.

import "time"

// How long the operator keeps a verdict before the next call. A provider that answered
// is asked again once an hour, which is 24 calls a day, and a provider that
// refused the key waits the same hour, because a refused key does not repair
// itself: the edit of its Secret is what calls again. A provider that gave no
// answer, or an answer that says nothing about the account, is asked again
// every five minutes, because an outage ends on its own and the libraries
// that name the provider wait for it.
const (
	providerReadyInterval = time.Hour
	providerDownInterval  = 5 * time.Minute
)

// What the last call saw: the provider's generation and its Secret's
// resourceVersion at the time, when it went out, and the reason it earned.
// An edit of either object is a change the verdict can depend on.
type providerCall struct {
	generation    int64
	secretVersion string
	at            time.Time
	reason        string
}

// The key of one provider in the operator's notes.
func providerCallKey(provider *MetadataProvider) string {
	return libraryKey(provider.Metadata.Namespace, provider.Metadata.Name)
}

// Whether this pass calls the provider: the operator has no note of it, the
// provider or its Secret changed since the last call, or the interval the
// last verdict earned has passed.
func (o *operator) providerCallDue(provider *MetadataProvider, secretVersion string, now time.Time) bool {
	last, held := o.providerCalls[providerCallKey(provider)]
	if !held || last.generation != provider.Metadata.Generation || last.secretVersion != secretVersion {
		return true
	}
	return !now.Before(last.at.Add(providerCallInterval(last.reason)))
}

// The interval one reason earns. Only an answer that says nothing about the
// account takes the short one.
func providerCallInterval(reason string) time.Duration {
	if reason == reasonUnreachable || reason == reasonUnavailable {
		return providerDownInterval
	}
	return providerReadyInterval
}

// The note of one call and the verdict it earned.
func (o *operator) noteProviderCall(provider *MetadataProvider, secretVersion string, now time.Time, reason string) {
	o.providerCalls[providerCallKey(provider)] = providerCall{
		generation:    provider.Metadata.Generation,
		secretVersion: secretVersion,
		at:            now,
		reason:        reason,
	}
}
