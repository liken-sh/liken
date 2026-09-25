---
name: jellyfin
description: "Keep playback progress the same in both directions between the progress store and a Jellyfin server through spec.jellyfin on the Catalog. Use when a cluster runs Jellyfin beside the operator, or when a backfill of progress is needed."
---

This skill is the guide at https://library.liken.sh/docs/guides/jellyfin/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Keep progress with Jellyfin

A cluster that runs Jellyfin beside this operator holds two copies of
one fact: where each person is in each title. The progress store holds
one and Jellyfin holds the other. `spec.jellyfin` on the `Catalog`
keeps the two the same, in both directions. A film started on a phone
resumes on the television, and a film started on the television
resumes on the phone.

## 1. Name the server on the Catalog

Make an API key on the Jellyfin server, under Dashboard and API Keys,
and put it in a `Secret` in the `Catalog`'s namespace. One key covers
every user, because an API key has administrator rights.

    apiVersion: v1
    kind: Secret
    metadata:
      name: jellyfin-api-key
      namespace: media
    type: Opaque
    stringData:
      token: <your key>

Then name the server's address and that `Secret` on the `Catalog`.

    apiVersion: library.liken.sh/v1alpha1
    kind: Catalog
    spec:
      jellyfin:
        url: http://jellyfin.jellyfin.svc:8096
        secretRef:
          name: jellyfin-api-key
          key: token

The operator creates one pod and one `Service`, both named after the
`Catalog` with the suffix `-jellyfin`, beside the progress store. The
key reaches the pod through a `secretKeyRef`, so the operator never
reads it. The [Catalog](https://library.liken.sh/docs/reference/catalogs/) reference describes
every field.

## 2. Set up the Webhook plugin

Jellyfin tells the role what a person plays through its Webhook
plugin. Install the plugin from Jellyfin's catalog and add a Generic
destination. Its URL is the `Service` the operator created, in the
`Catalog`'s namespace:

    http://<catalog>-jellyfin.<namespace>.svc:8080/webhook

Set the rest of the destination like this.

* Notification types: `PlaybackStart`, `PlaybackProgress`,
  `PlaybackStop`, and `UserDataSaved`.
* Item types: enable Movies and Episodes.
* Request header: `Content-Type` with the value `application/json`.

The template renders the body the role reads. Paste it as one line,
exactly as it is here. The plugin's page stores it base64-encoded, so
anything that writes the plugin's configuration through Jellyfin's API
must encode the template field the same way.

```
{"event":"{{NotificationType}}","user":"{{{NotificationUsername}}}","userId":"{{UserId}}","itemId":"{{ItemId}}","itemType":"{{ItemType}}","seriesId":"{{SeriesId}}","season":"{{SeasonNumber}}","episode":"{{EpisodeNumber}}","positionTicks":"{{PlaybackPositionTicks}}","runTimeTicks":"{{RunTimeTicks}}","paused":"{{IsPaused}}","playedToCompletion":"{{PlayedToCompletion}}","saveReason":"{{SaveReason}}","played":"{{Played}}","tmdb":"{{Provider_tmdb}}","imdb":"{{Provider_imdb}}","tvdb":"{{Provider_tvdb}}"}
```

## 3. What crosses

A post from Jellyfin becomes one message on the media bus, and the
progress store records the position and the person against the title.
The newer timestamp wins, so a screen resumes from the last place
anything played.

A `Play` on a screen goes the other way. The role writes each person's
position to Jellyfin every ten seconds while the position moves, and
once more when the `Play` ends.

A work counts as watched when the time left is at or under a twentieth
of the work, and at or under five minutes. The rule takes whichever of
the two leaves less time. So a short episode is watched near its end,
and a long film is watched with up to five minutes left.

Where the file's [marks](https://library.liken.sh/docs/guides/enrichment/#intro-and-credits-marks)
place the credits in the second half of the work, the work counts as
watched from the start of those credits instead, whatever time is left.
The role reads the credits marks the way the player's up-next card
does. Candidates that overlap become one span, from the median start and
the median end of the group, so one submission that is off moves the
line little. Spans that do not overlap stay apart, and the line is the
start of the earliest span that starts in the second half. A credits
span in the first half is an opening title sequence and moves nothing.
Where no span starts in the second half, the rule above applies.

Every write to Jellyfin includes a played mark. The role reads the mark
off the position it writes, with that same rule. So a replay from the
start clears the mark, because the new position does not pass the rule.

A `UserDataSaved` post with the save reason `TogglePlayed` is a mark a
person set by hand in Jellyfin. The role records it as a position at
the end of the work, because Jellyfin stores a mark set by hand as a
state with no position. An unmark records a position at the start.

The role drops every other save reason. Each of those is the role's own
write to Jellyfin, posted back as a save. Reading one would record the
role's write as a play.

A person's Jellyfin user name must be their `Person` name. A Jellyfin
user with no `Person` of that name still records a row, and no screen
shows it, because no `Person` claims it.

## 4. The backfill

The operator runs one backfill on its own, with no step for you to
take. It waits until every durable copy of the progress store is up,
then runs one `Job` named after the `Catalog` with the suffix
`-jellyfin-backfill`.

For every Jellyfin user the `Job` reads two lists. It records an item
with a resume point at the position Jellyfin holds. It records an item
the user finished at the end of the work. Each row has the date Jellyfin
last recorded a play of that item.

Jellyfin holds one state per item and no list of viewings, so the
backfill records no history. An item with no provider ids is counted
and skipped. The backfill makes no `Person`: a Jellyfin user with no
`Person` of that name records rows that no screen shows.

A rerun is safe. The store keeps the newer timestamp, so a backfilled
row never overwrites a newer play, and a row the backfill writes twice
is the same row.

`status.jellyfin` on the `Catalog` reports the state of the backfill:

```yaml
status:
  jellyfin:
    server: http://jellyfin.jellyfin.svc:8096
    backfill: Finished
    backfilled: "2026-09-08T14:02:11Z"
```

`backfill` is `Pending`, `Running`, `Failed`, or `Finished`. To run the
backfill again, clear `status.jellyfin` or change `spec.jellyfin.url`,
because the status names the server it ran against. The `Job`'s log
holds the counts of the run: users, items published, and items skipped.
The log stays for an hour after the `Job` ends.

## 5. Share the volume with Jellyfin

Jellyfin writes beside the media too, unless its library settings say
otherwise. For this operator to be the only writer of art and
`.nfo` files, turn these off on each Jellyfin library that reads a
`Library`'s volume:

* Saving artwork into media folders. The art fact writes the images.
* The NFO metadata saver. The nfo fact writes the `.nfo` file.
* Any subtitle download plugin. Plan 60 covers subtitles.

Leave Jellyfin's trickplay extraction on. With extraction off for a
library, Jellyfin deletes the whole `.trickplay` directory beside every
video on each refresh, whoever made the directory. It deletes the rows
it holds for them too. So on a volume this operator tiles, Jellyfin's
extraction stays on, and the two race for each new title. The trickplay
`Job` claims the node's GPU so that it wins. A directory Jellyfin made
first holds the same sheets, so the fact leaves it alone.

Jellyfin imports a tile directory it did not make when the folder name
is the same, the files are present, and it holds no row of its own for
that width. It records the number of files in the folder as the
thumbnail count, where its own extraction records the number of
thumbnails. This operator writes nothing but the sheets into the folder,
so the count is the sheet count. Verified against Jellyfin 10.11.
