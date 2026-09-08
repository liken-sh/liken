---
title: Keep progress with Jellyfin
weight: 85
---

# Keep progress with Jellyfin

A cluster that runs Jellyfin beside this operator holds two copies of
one fact: where each person is in each title. The progress store holds
one and Jellyfin holds the other. `spec.jellyfin` on the `Catalog`
keeps the two the same, in both directions. A film started on a phone
resumes on the television, and a film started on the television
resumes on the phone.

## 1. Name the server on the Catalog

Make an API key on the Jellyfin server, under Dashboard and API Keys,
and put it in a `Secret` in the `Catalog`'s namespace. One key serves
every user, because an API key acts as an administrator.

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

The operator stands one pod and one `Service`, both named after the
`Catalog` with the suffix `-jellyfin`, beside the progress store. The
key reaches the pod through a `secretKeyRef`, so the operator never
reads it. The [Catalog](/docs/reference/catalogs/) reference describes
every field.

## 2. Set up the Webhook plugin

Jellyfin tells the role what a person plays through its Webhook
plugin. Install the plugin from Jellyfin's catalog and add a Generic
destination. Its URL is the `Service` the operator stood, in the
`Catalog`'s namespace:

    http://<catalog>-jellyfin.<namespace>.svc:8080/webhook

Set the rest of the destination like this.

* Notification types: `PlaybackStart`, `PlaybackProgress`, and
  `PlaybackStop`.
* Item types: enable Movies and Episodes.
* Request header: `Content-Type` with the value `application/json`.

The template renders the body the role reads. Paste it as one line,
exactly as it is here. The plugin's page stores it base64-encoded, so
anything that writes the plugin's configuration through Jellyfin's API
must encode the template field the same way.

```
{"event":"{{NotificationType}}","user":"{{{NotificationUsername}}}","userId":"{{UserId}}","itemId":"{{ItemId}}","itemType":"{{ItemType}}","seriesId":"{{SeriesId}}","season":"{{SeasonNumber}}","episode":"{{EpisodeNumber}}","positionTicks":"{{PlaybackPositionTicks}}","runTimeTicks":"{{RunTimeTicks}}","paused":"{{IsPaused}}","playedToCompletion":"{{PlayedToCompletion}}","tmdb":"{{Provider_tmdb}}","imdb":"{{Provider_imdb}}","tvdb":"{{Provider_tvdb}}"}
```

## 3. What crosses

A post from Jellyfin becomes one message on the media bus, and the
progress store records the position and the person against the title.
The newer timestamp wins, so a screen resumes from the last place
anything played.

A `Play` on a screen goes the other way. The role writes each person's
position to Jellyfin every ten seconds while the position moves, and
once more when the `Play` ends.

A person's Jellyfin user name must be their `Person` name. A Jellyfin
user with no `Person` of that name still records a row, and no screen
shows it, because no `Person` claims it.

## 4. The backfill

The operator runs one backfill on its own, with no step for you to
take. It waits until every durable copy of the progress store is up,
then runs one Job named after the `Catalog` with the suffix
`-jellyfin-backfill`. For every Jellyfin user, the Job reads the items
with a resume point and the items the user finished, and records each
one in the progress store under the title's provider ids, with the
position, whether the user finished it, and the date Jellyfin last
saw it played.

Jellyfin holds one state per item and no list of viewings, so the
backfill carries no history. An item with no provider ids is counted
and skipped. The backfill makes no `Person`: a Jellyfin user with no
`Person` of that name records rows that no screen shows.

A rerun is safe. The store keeps the newer timestamp, so a backfilled
row never overwrites a newer play, and a row the backfill writes twice
is the same row.

`status.jellyfin` on the `Catalog` says where the backfill stands:

```yaml
status:
  jellyfin:
    server: http://jellyfin.jellyfin.svc:8096
    backfill: Finished
    backfilled: "2026-09-08T14:02:11Z"
```

`backfill` is `Pending`, `Running`, `Failed`, or `Finished`. To run
the backfill again, clear `status.jellyfin` or change
`spec.jellyfin.url`, because the status names the server it ran
against. The counts of the run, users, items published, and items
skipped, are in the Job's log, which stays for an hour after the Job
ends.
