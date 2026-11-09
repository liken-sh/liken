// How a person or an audience came to finish a work, as the status line
// on a movie's page says it. A play on a screen of this cluster ran
// through the film, so the line names the month it finished in. A mark a
// person set by hand says nothing about when they saw the film, so the
// line says it was marked and gives no date. The store keeps a row from
// outside the cluster, such as one from Jellyfin, at the second it was
// written, and that row does not say whether a person watched the film or
// set it watched there, so the line gives no date for it either.

use super::Progress;

/// Where one play's row in the progress store came from.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum Origin {
    /// A row the store took from outside the cluster, such as Jellyfin.
    #[default]
    Outside,
    /// A `Play` on a screen of this cluster, whose position moved as the
    /// playback pod reported it.
    Screen,
    /// A mark a person set at a screen with Mark watched or Clear
    /// progress.
    Mark,
}

impl Origin {
    /// The origin of the row of this name and library. The operator writes
    /// a `Play`'s library on its row, and writes a mark and a row from
    /// outside with none. A mark's row takes the name the browser gives
    /// the mark, which `bus::mark::PREFIX` starts.
    pub fn of(play: &str, library: &str) -> Self {
        match (
            library.is_empty(),
            play.starts_with(crate::bus::mark::PREFIX),
        ) {
            (false, _) => Self::Screen,
            (true, true) => Self::Mark,
            (true, false) => Self::Outside,
        }
    }
}

/// How a work was finished.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Finish {
    /// A play on a screen of this cluster passed the finished mark. `at`
    /// is the play's last write, in Unix seconds.
    Played { at: i64 },
    /// A person marked the work watched by hand.
    Marked,
    /// Only a row from outside the cluster finished the work.
    Recorded,
}

/// How these plays finished the work, and nothing where none of them
/// did. The plays are those after the latest clear. The latest screen play
/// that finished the work names the date, because a person who watched a
/// film twice saw it last then. A mark answers only where no screen play
/// finished the work, so a person who marked a film they had watched
/// keeps the date of the watch.
pub fn finish(plays: &[&Progress]) -> Option<Finish> {
    let finished = || plays.iter().filter(|play| play.finished);
    if let Some(at) = finished()
        .filter(|play| play.origin == Origin::Screen)
        .map(|play| play.recorded)
        .max()
    {
        return Some(Finish::Played { at });
    }
    if finished().any(|play| play.origin == Origin::Mark) {
        return Some(Finish::Marked);
    }
    finished().next().map(|_| Finish::Recorded)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn play(origin: Origin, finished: bool, recorded: i64) -> Progress {
        Progress {
            origin,
            finished,
            recorded,
            ..Progress::default()
        }
    }

    #[test]
    fn a_row_takes_its_origin_from_its_library_and_its_name() {
        let cases = [
            ("living-room-a-film-x2k9p", "movies", Origin::Screen),
            ("mark-living-room-1794107779", "", Origin::Mark),
            ("jellyfin-user-item", "", Origin::Outside),
        ];
        for (name, library, want) in cases {
            assert_eq!(Origin::of(name, library), want, "{name}");
        }
    }

    #[test]
    fn the_latest_screen_play_that_finished_names_the_date() {
        let plays = [
            play(Origin::Screen, true, 100),
            play(Origin::Outside, true, 400),
            play(Origin::Screen, true, 300),
            play(Origin::Screen, false, 500),
            play(Origin::Mark, true, 600),
        ];
        let plays: Vec<&Progress> = plays.iter().collect();
        assert_eq!(finish(&plays), Some(Finish::Played { at: 300 }));
    }

    #[test]
    fn a_mark_answers_where_no_screen_play_finished_and_an_outside_row_after_it() {
        let cases = [
            (
                vec![
                    play(Origin::Screen, false, 100),
                    play(Origin::Mark, true, 200),
                    play(Origin::Outside, true, 300),
                ],
                Some(Finish::Marked),
            ),
            (
                vec![play(Origin::Outside, true, 300)],
                Some(Finish::Recorded),
            ),
            (vec![play(Origin::Screen, false, 100)], None),
            (Vec::new(), None),
        ];
        for (plays, want) in cases {
            let plays: Vec<&Progress> = plays.iter().collect();
            assert_eq!(finish(&plays), want, "{plays:?}");
        }
    }
}
