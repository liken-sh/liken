# Progress to Jellyfin over the bus

Plan 47. A small consumer on the media bus mirrors a `Person`'s
playback progress into Jellyfin, so the same film shows the same
position in both, and Jellyfin's own clients pick up where a `liken`
screen left off.

## The problem

The progress store (plan 14) holds where each person is in each
item, and the screens read and write it. A house that also runs
Jellyfin has a second copy of that fact, kept by Jellyfin's clients,
and the two never meet. A film started on the television and finished
on a phone shows two positions.

## The shape

The bus already carries a position report for every running `Play`,
once a second, and the `Play` names the item and the people watching
it. A consumer that joins the bus with a plain MQTT client, the way
the manual describes for any outside program, can map the item to a
Jellyfin item by its provider ids and post the position through
Jellyfin's playback progress API under each person's Jellyfin user.
The reverse direction, Jellyfin's progress into the store, reads
Jellyfin's user data on a schedule and writes the store through the
same contract the screens use.

Open questions for the design: where the map from `Person` to
Jellyfin user lives, whether the consumer is a container the library
operator ships or a Home Assistant automation, and which direction
wins when both moved.
