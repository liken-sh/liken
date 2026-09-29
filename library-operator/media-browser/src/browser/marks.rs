// The marks a person sets from a page: watched, or cleared. The browser
// publishes each mark for everyone at the screen, and the progress role
// writes it into the store, because the screen pod holds the store
// read-only. The page redraws when the store changes, the same way it
// redraws for a play's position.

use super::{Browser, lines};
use crate::art::Art;
use crate::bus::mark::{self, TitleMark};
use crate::catalog::{Selection, Source};
use crate::log::opaque;

/// Where this browser publishes its marks: the branch of the tree the
/// namespace's plays are under, and the `Player` whose screen this is. A
/// run the operator named no branch for publishes no mark.
#[derive(Debug, Default)]
pub struct Marks {
    branch: String,
    player: String,
    // The second of the last mark this browser published. The second names
    // the mark's topic and its row, so a second press within the same
    // second moves to the next second. The later press then keeps a later
    // time, and the store's thread rule stands on it.
    last: i64,
}

impl Marks {
    pub fn new(branch: String, player: String) -> Self {
        Self {
            branch,
            player,
            last: 0,
        }
    }

    // The second this press publishes under: now, or the second after the
    // last mark where that is later.
    fn stamp(&mut self, now: i64) -> i64 {
        self.last = now.max(self.last + 1);
        self.last
    }
}

impl<S: Source, A: Art> Browser<S, A> {
    // Publish one mark on the work the page named, for the people at the
    // screen. A mark is retained, because the person presses once and
    // nothing repeats it: a progress role that is down reads it when it
    // subscribes again.
    //
    // A watched mark with no duration sends nothing. The store reads a
    // position as finished only against a duration, so that mark would
    // read as not started, which is the other mark.
    pub(super) fn request_mark(
        &mut self,
        library: &str,
        selection: &Selection,
        mark: TitleMark,
        duration: i64,
    ) {
        let people = self.audience.current(self.clock).to_vec();
        let asked = format!(
            "mark {} in {library} {} for {}",
            selection.named(),
            mark.word(),
            lines::people(&people)
        );
        self.did(format!("marked {} {}", selection.named(), mark.word()));
        if mark == TitleMark::Watched && duration <= 0 {
            self.log.line(format!(
                "{asked}: sent nothing, because the catalog holds no duration for it"
            ));
            return;
        }
        let identity = self.source.identity(library, selection);
        let Some((topic, at)) = self.mark_route(&asked) else {
            return;
        };
        self.publish_mark(
            &topic,
            mark::payload(mark, &self.marks.player, &people, &identity, duration, at),
        );
        self.log.line(format!(
            "{asked}: sent the mark at {} of {duration} on {topic}",
            mark.position(duration)
        ));
    }

    // Publish the marks Pick up here names, the earlier episodes watched and
    // the later ones cleared, as one retained message for the whole press.
    // One message keeps the press to one topic and one `at`: a mark for each
    // episode would move each one to a second of its own, and the last would
    // stand in the future over the play the same press starts.
    pub(super) fn request_pick_up(
        &mut self,
        library: &str,
        series: &str,
        episodes: &[mark::Episode],
    ) {
        let people = self.audience.current(self.clock).to_vec();
        let watched = episodes
            .iter()
            .filter(|episode| episode.mark == TitleMark::Watched)
            .count();
        let counts = format!(
            "{} of {} watched and clear {}",
            lines::count(watched, "episode"),
            opaque(series),
            episodes.len() - watched
        );
        let asked = format!("mark {counts} in {library} for {}", lines::people(&people));
        self.did(format!("marked {counts}"));
        let Some(first) = episodes.first() else {
            self.log.line(format!(
                "{asked}: sent nothing, because no episode is left to mark"
            ));
            return;
        };
        // An episode records against its series, so the first episode's
        // identity carries the aliases every entry of the list takes.
        let identity = self.source.identity(
            library,
            &Selection::Episode {
                series: series.to_string(),
                season: first.season,
                episode: first.episode,
            },
        );
        let Some((topic, at)) = self.mark_route(&asked) else {
            return;
        };
        self.publish_mark(
            &topic,
            mark::list_payload(&self.marks.player, &people, &identity.aliases, episodes, at),
        );
        let last = episodes[episodes.len() - 1];
        self.log.line(format!(
            "{asked}: sent the mark on S{}E{} to S{}E{} on {topic}",
            first.season, first.episode, last.season, last.episode
        ));
    }

    // The topic and the second of one press, or nothing where this run
    // cannot publish a mark, with a line that says why.
    fn mark_route(&mut self, asked: &str) -> Option<(String, i64)> {
        if self.bus.is_none() {
            self.log.line(format!(
                "{asked}: sent nothing, because this run has no bus"
            ));
            return None;
        }
        if self.marks.branch.is_empty() {
            self.log.line(format!(
                "{asked}: sent nothing, because the operator named no plays topic"
            ));
            return None;
        }
        let at = self.marks.stamp((self.now)());
        Some((mark::topic(&self.marks.branch, &self.marks.player, at), at))
    }

    // A mark is retained, because the person presses once and nothing
    // repeats it: a progress role that is down reads it when it subscribes
    // again.
    fn publish_mark(&self, topic: &str, payload: Vec<u8>) {
        if let Some(bus) = &self.bus {
            bus.publish(topic, payload, true);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_second_press_in_the_same_second_takes_the_next_second() {
        let mut marks = Marks::new("liken/library/plays/house".into(), "den".into());
        assert_eq!(marks.stamp(100), 100);
        assert_eq!(marks.stamp(100), 101);
        assert_eq!(marks.stamp(100), 102);
        assert_eq!(marks.stamp(200), 200);
    }
}
