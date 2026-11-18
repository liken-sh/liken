package denon

// Pending returns a Settings with only the declared fields that the
// operator must send. observed is what the receiver reports, previous is
// what the operator applied earlier, and known is false when previous
// does not record what the receiver has, as after a restart.
//
// A field that the receiver reports is sent when the declared value
// differs from the reported one, so a restart sends nothing to a
// receiver that already has the declared settings, and a change made at
// the receiver is reverted. A field that the receiver does not report
// is sent when the declared value differs from previous. When known is
// false, Pending sends no unreported field.
//
// Two values are equal when they produce the same wire command. A
// declared value that no command can represent is never equal to
// another, so Pending returns it and ApplySettings reports its error,
// except for an unreported field while known is false.
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
