package main

// providercadence.go decides when the operator calls a provider's check.
// A pass reads every MetadataProvider, and a pass runs at least every ten
// seconds, so a check on every pass would spend the provider's allowance on
// the check alone: OMDb's free key is a thousand calls a day, and a check
// every ten seconds is 8640. The check therefore calls the provider only when
// its verdict can have changed, and reads the provider's Secret only for
// that call. The Ready condition stays the record a person reads. The
// operator holds its own note of the last call in memory, so an operator
// that starts again calls every provider once.

import "time"

// How long the operator keeps a verdict before the next call. A provider
// that answered is asked again once an hour, which is 24 calls a day. Every
// other verdict is asked again every five minutes. An outage ends on its
// own, and the libraries that name the provider wait for it. A refused key,
// a missing Secret, and a missing key end when a person repairs the
// Secret, and the pass does not read the Secret between calls, so the
// shorter interval is what shows the repair within minutes.
const (
	providerReadyInterval = time.Hour
	providerDownInterval  = 5 * time.Minute
)

// What the last call saw: the provider's generation at the time, when it
// went out, and the reason it earned. An edit of the provider is a change
// the verdict can depend on.
type providerCall struct {
	generation int64
	at         time.Time
	reason     string
}

// The key of one provider in the operator's notes.
func providerCallKey(provider *MetadataProvider) string {
	return libraryKey(provider.Metadata.Namespace, provider.Metadata.Name)
}

// Whether this pass calls the provider: the operator has no note of it, the
// provider changed since the last call, or the interval the last verdict
// earned has passed.
func (o *operator) providerCallDue(provider *MetadataProvider, now time.Time) bool {
	last, held := o.providerCalls[providerCallKey(provider)]
	if !held || last.generation != provider.Metadata.Generation {
		return true
	}
	return !now.Before(last.at.Add(providerCallInterval(last.reason)))
}

// The interval one reason earns. Only an answer that says the account works
// takes the long one.
func providerCallInterval(reason string) time.Duration {
	if reason == reasonReachable {
		return providerReadyInterval
	}
	return providerDownInterval
}

// The note of one call and the verdict it earned.
func (o *operator) noteProviderCall(provider *MetadataProvider, now time.Time, reason string) {
	o.providerCalls[providerCallKey(provider)] = providerCall{
		generation: provider.Metadata.Generation,
		at:         now,
		reason:     reason,
	}
}

// The operator drops the note of one call when the verdict it earned was
// not written, so the next pass decides again.
func (o *operator) forgetProviderCall(provider *MetadataProvider) {
	delete(o.providerCalls, providerCallKey(provider))
}
