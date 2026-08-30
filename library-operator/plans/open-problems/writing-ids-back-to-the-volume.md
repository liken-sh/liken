# Writing ids back to the volume

The scanner derives an item's id from the provider id in its `.nfo`,
scoped by kind, such as `movie:tmdb:603`. That id is durable, because
the scanner reads it off the volume on every walk and the public
database behind it does not change. About a fifth of the lab's movies
have no sidecar and no provider id. Their id rests on the folder's path,
and a move of the folder breaks it, so the item and any watch state
keyed on it are lost. An alias that only a past state of the volume
produced, such as a renamed sidecar-less folder, does not come back
after a full catalog loss either, because the volume no longer names it.

Writing a minted, cleaner id back to the volume as a durable fact would
fix both. The scanner reads that id back on the next walk, exactly as it
reads a provider id, so the id survives a move and a full catalog loss.

The shape considered and set aside for now is a separate `liken` sidecar
file, not the `.nfo`. Jellyfin and Kodi own the `.nfo` and rewrite it,
so a tag the operator adds there is at risk. The `liken` file is
additive, it names nothing else, and a person can delete it to reset.
The scanner writes it only where the volume is writable and only when
the `Library` opts in, and it degrades to the derived id on a read-only
export, which is how the lab's production volume is mounted.

The project trusts the public databases' ids and defers writing its own.
The derived, provider-scoped id is the baseline this note would upgrade.
