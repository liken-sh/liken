// A series thread whose audience watched every episode the catalog held
// stays in the row on the episode after the last, but the row sorts it by
// the play that finished that episode, so the thread sinks while the
// audience waits for more. When the next episode arrives, the thread
// sorts by the arrival instead, so the series comes to the front of the
// row the day there is something new to watch.
//
// The audience was caught up when every episode after the one they
// finished arrived after the play that finished it. A thread with an
// episode the catalog already held at that play is a thread the audience
// left, and a thread that stands inside an episode resumes it, so both
// keep the time of their play.

use crate::catalog::Episode;
use crate::catalog::progress::thread::Offer;

// The second the row sorts this series thread by when the episode it
// offers is new to a caught-up audience, and nothing for any other
// thread. `episodes` is the series in aired order, the same list the walk
// indexed, so the offer's leaf is an index into it.
pub fn new_episode(episodes: &[Episode], offer: &Offer) -> Option<i64> {
    if offer.progress.is_some() {
        return None;
    }
    let after = episodes.get(offer.leaf..)?;
    let caught_up = after.iter().all(|episode| episode.added > offer.recorded);
    let offered = after.first()?;
    caught_up.then(|| offered.added.max(offer.recorded))
}
