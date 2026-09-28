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
// that answered is asked again once an hour, which is 24 calls a day. A key
// past its limit is asked again on the same hour, because every call before
// the limit resets counts against it, and OMDb does not publish when it
// resets. Every other verdict is asked again every five minutes. An outage
// ends on its own, and the libraries that name the provider wait for it. A
// refused key, a missing Secret, and a missing key end when a person
// repairs the Secret, and the pass does not read the Secret between calls,
// so the shorter interval is what shows the repair within minutes.
const (
	providerReadyInterval = time.Hour
	providerDownInterval  = 5 * time.Minute
)

// What the last call saw: the provider's identity and generation at the
// time, when it went out, the verdict it earned, and whether the API server
// stored that verdict. An edit of the provider is a change the verdict can
// depend on. A provider deleted and created again under the same name has
// a new uid and starts again at generation 1, so the uid is what keeps it
// from inheriting the old provider's note.
type providerCall struct {
	uid        string
	generation int64
	at         time.Time
	verdict    providerVerdict
	written    bool
}

// The key of one provider in the operator's notes.
func providerCallKey(provider *MetadataProvider) string {
	return libraryKey(provider.Metadata.Namespace, provider.Metadata.Name)
}

// The note of the last call to this provider, if the provider is still the
// one that call was about.
func (o *operator) providerCallNote(provider *MetadataProvider) (providerCall, bool) {
	last, held := o.providerCalls[providerCallKey(provider)]
	if !held || last.uid != provider.Metadata.UID || last.generation != provider.Metadata.Generation {
		return providerCall{}, false
	}
	return last, true
}

// Whether this pass calls the provider: the operator has no note of it, the
// provider changed since the last call, or the interval the last verdict
// earned has passed.
func (o *operator) providerCallDue(provider *MetadataProvider, now time.Time) bool {
	last, held := o.providerCallNote(provider)
	if !held {
		return true
	}
	return !now.Before(last.at.Add(providerCallInterval(last.verdict.reason)))
}

// The interval one reason earns. An answer that says the account works
// takes the long one, and so does an answer that says the key has spent
// its calls, because a sooner call spends more of them.
func providerCallInterval(reason string) time.Duration {
	if reason == reasonReachable || reason == reasonLimitReached {
		return providerReadyInterval
	}
	return providerDownInterval
}

// The note of one call and the verdict it earned, which no status holds
// yet.
func (o *operator) noteProviderCall(provider *MetadataProvider, now time.Time, verdict providerVerdict) {
	o.providerCalls[providerCallKey(provider)] = providerCall{
		uid:        provider.Metadata.UID,
		generation: provider.Metadata.Generation,
		at:         now,
		verdict:    verdict,
	}
}

// The API server stored the verdict of the last call, so a pass that is
// not due has nothing to write.
func (o *operator) noteProviderWritten(provider *MetadataProvider) {
	if last, held := o.providerCallNote(provider); held {
		last.written = true
		o.providerCalls[providerCallKey(provider)] = last
	}
}

// The operator drops the note of a provider that the API server reported
// gone during a status write. A provider deleted between passes keeps its
// note, and the uid in the note keeps a provider created again under the
// same name from reading it. The pass does not drop the notes of providers
// its list lacks, because a list that fails reads as no provider at all,
// and dropping every note then would call every provider on the next pass.
func (o *operator) forgetProviderCall(provider *MetadataProvider) {
	delete(o.providerCalls, providerCallKey(provider))
}
