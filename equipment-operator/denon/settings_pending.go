package denon

// Pending decides which declared fields the operator sends. A field
// the receiver reports is compared with it: the operator sends the
// field only while the receiver reports another value, so a restart
// against a receiver that already holds the declared settings sends
// nothing, and a change made at the receiver is driven back. A field
// the receiver does not report cannot be compared, so it is compared
// with the value the operator applied before: it is sent once when the
// spec changes it, and not again. After a restart the operator has
// applied nothing yet, so it does not send an unreported field until
// the spec changes that field.

// Pending answers the declared fields to send, as Settings that hold
// only those fields. observed is what the receiver reports, previous is
// the settings the operator applied before, and known says whether
// previous holds anything since the operator started. The fields are
// compared by the wire command each one carries, which is the same
// command for the same value. A declared value no command can carry is
// always pending when it would be sent, so ApplySettings reports its
// error.
func (w Settings) Pending(observed, previous Settings, known bool) Settings {
	var pending Settings
	for _, spec := range settingsTable {
		want, wantErr, declared := spec.command(&w)
		if !declared {
			continue
		}
		reported, reportedErr, held := spec.command(&observed)
		if held && reportedErr == nil && wantErr == nil && reported == want {
			continue
		}
		if !held {
			before, beforeErr, had := spec.command(&previous)
			if !known || (had && beforeErr == nil && wantErr == nil && before == want) {
				continue
			}
		}
		spec.take(&pending, &w)
	}
	for channel, value := range w.ChannelVolumes {
		if reported, held := observed.ChannelVolumes[channel]; held && reported == value {
			continue
		} else if !held {
			before, had := previous.ChannelVolumes[channel]
			if !known || (had && before == value) {
				continue
			}
		}
		if pending.ChannelVolumes == nil {
			pending.ChannelVolumes = map[string]float64{}
		}
		pending.ChannelVolumes[channel] = value
	}
	return pending
}
