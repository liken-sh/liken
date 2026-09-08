// What the progress store answers a screen: where one play reached, the
// work it names, and the thread rule the continue-watching row walks.

pub mod thread;

use super::draw::Date;

/// The part of a work a play must reach before a screen counts it
/// finished, as a percentage. Credits run past the story, so a play that
/// stopped in them is finished. This is a first setting, not a ruling.
pub const FINISHED_PERCENT: i64 = 90;

/// Whether a play reached the end of its work. A work with no duration is
/// never finished, because nothing says how long it is.
pub fn finished(position: i64, duration: i64) -> bool {
    duration > 0 && position >= duration * FINISHED_PERCENT / 100
}

/// The position and the duration of one play as a page draws them,
/// `H:MM:SS / H:MM:SS`. The longer of the two decides whether both carry
/// hours, so the two numbers of one line are spelled the same way.
pub fn clock(position: i64, duration: i64) -> String {
    let hours = position.max(duration) >= 3_600;
    format!(
        "{} / {}",
        spelled(position, hours),
        spelled(duration, hours)
    )
}

// One number of that line: hours, minutes, and seconds where the line
// carries hours, and minutes and seconds where it does not. A position
// before the start of the work reads as the start.
fn spelled(seconds: i64, hours: bool) -> String {
    let seconds = seconds.max(0);
    match hours {
        true => format!(
            "{}:{:02}:{:02}",
            seconds / 3_600,
            (seconds % 3_600) / 60,
            seconds % 60
        ),
        false => format!("{:02}:{:02}", seconds / 60, seconds % 60),
    }
}

/// Where one `Play` of one work reached.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Progress {
    /// The `Play`'s name, which is the store's whole key.
    pub play: String,
    /// How far into the work the play reached, in seconds.
    pub position: i64,
    /// How long the work is, in seconds, and zero where the store holds
    /// none.
    pub duration: i64,
    /// Whether the position passed the finished mark. See [`finished`].
    pub finished: bool,
    /// Whether the `Play` still runs, which the store writes as an `ended`
    /// of zero.
    pub running: bool,
    /// The last write of the row, in Unix seconds.
    pub recorded: i64,
    /// The aired season number, zero for a work that has none.
    pub season: i64,
    /// The aired episode number, zero for a work that has none.
    pub episode: i64,
}

impl Progress {
    /// How far the play reached, as a slot carries it. The slot drops the
    /// rest, because a bar needs the two numbers and nothing else.
    pub fn played(&self) -> Played {
        Played {
            position: self.position,
            duration: self.duration,
        }
    }
}

/// How far one play of a work reached, as the slot that draws it carries
/// it: the second a resume starts at, and how long the work is.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Played {
    /// How far into the work the play reached, in seconds.
    pub position: i64,
    /// How long the work is, in seconds, and zero where the store holds
    /// none.
    pub duration: i64,
}

impl Played {
    /// The share of the work the play reached, which the bar under the art
    /// draws. A work with no duration reads as nothing watched, because
    /// nothing says how long it is.
    pub fn fraction(&self) -> f32 {
        match self.duration > 0 {
            true => self.position as f32 / self.duration as f32,
            false => 0.0,
        }
    }
}

/// One play of one work an audience is on: the catalog columns a slot
/// needs, and where the play reached.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Resume {
    /// The library that holds the work, as `namespace/name`.
    pub library: String,
    /// `movie` or `series`, which names the item table the row came from.
    pub kind: String,
    /// The movie's or the series' own id inside that library.
    pub id: String,
    /// The name a person reads.
    pub title: String,
    /// The year or the date of release, as the catalog stores it.
    pub released: String,
    /// The path of the primary art, relative to the library root.
    pub art: String,
    /// Where the play reached. For a series it is one episode row.
    pub progress: Progress,
    // Whether the play names exactly the people the read was made for, and
    // not those people and more. The thread rule reads it.
    pub exact: bool,
}

/// One play as `--print-progress` writes it: nine tab-separated fields,
/// so a drill reads the store with no screen.
pub fn line(resume: &Resume) -> String {
    let numbers = if resume.progress.season == 0 && resume.progress.episode == 0 {
        "-".to_string()
    } else {
        format!("S{}E{}", resume.progress.season, resume.progress.episode)
    };
    let state = match resume.progress {
        Progress { finished: true, .. } => "finished",
        Progress { running: true, .. } => "running",
        _ => "-",
    };

    let audience = match resume.exact {
        true => "exact",
        false => "more",
    };

    format!(
        "{}\t{}\t{}\t{}\t{}\t{}/{}\t{}\t{}\t{}",
        resume.kind,
        resume.library,
        resume.id,
        resume.title,
        numbers,
        resume.progress.position,
        resume.progress.duration,
        state,
        stamp(resume.progress.recorded),
        audience,
    )
}

/// Unix seconds as an RFC 3339 time in UTC, through the crate's own
/// civil-from-days arithmetic, so the print needs no date crate.
pub fn stamp(seconds: i64) -> String {
    let day = seconds.rem_euclid(86_400);

    format!(
        "{}T{:02}:{:02}:{:02}Z",
        Date::from_seconds(seconds).iso(),
        day / 3_600,
        (day % 3_600) / 60,
        day % 60,
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_play_past_nine_tenths_of_its_work_is_finished() {
        assert!(finished(90, 100));
        assert!(finished(100, 100));
        assert!(!finished(89, 100));
    }

    #[test]
    fn a_work_with_no_duration_is_never_finished() {
        assert!(!finished(0, 0));
        assert!(!finished(600, 0));
    }

    #[test]
    fn a_work_an_hour_or_longer_carries_hours_in_both_numbers() {
        assert_eq!(clock(2_912, 6_730), "0:48:32 / 1:52:10");
    }

    #[test]
    fn a_work_under_an_hour_carries_minutes_and_seconds() {
        assert_eq!(clock(425, 1_320), "07:05 / 22:00");
    }

    #[test]
    fn a_play_past_an_hour_of_a_shorter_work_still_carries_hours() {
        assert_eq!(clock(3_600, 1_320), "1:00:00 / 0:22:00");
    }

    #[test]
    fn a_play_that_reached_nothing_reads_as_the_start() {
        assert_eq!(clock(0, 1_320), "00:00 / 22:00");
    }

    #[test]
    fn a_play_carries_the_share_of_the_work_it_reached() {
        let played = Progress {
            position: 600,
            duration: 2_400,
            ..Progress::default()
        }
        .played();
        assert_eq!(
            played,
            Played {
                position: 600,
                duration: 2_400
            }
        );
        assert_eq!(played.fraction(), 0.25);
    }

    #[test]
    fn a_play_of_a_work_with_no_duration_reads_as_nothing_watched() {
        assert_eq!(
            Played {
                position: 600,
                duration: 0
            }
            .fraction(),
            0.0
        );
    }

    fn resume(progress: Progress) -> Resume {
        Resume {
            library: "screening/films".into(),
            kind: "movie".into(),
            id: "movie:tmdb:1".into(),
            title: "Some Film (1999)".into(),
            released: "1999".into(),
            art: "poster.jpg".into(),
            progress,
            exact: true,
        }
    }

    #[test]
    fn a_movie_line_carries_a_dash_where_a_work_has_no_numbers() {
        let line = line(&resume(Progress {
            play: "one".into(),
            position: 600,
            duration: 6_000,
            running: true,
            recorded: 1_757_160_842,
            ..Progress::default()
        }));
        assert_eq!(
            line,
            "movie\tscreening/films\tmovie:tmdb:1\tSome Film (1999)\t-\t600/6000\trunning\t\
             2025-09-06T12:14:02Z\texact"
        );
    }

    #[test]
    fn a_play_of_more_people_than_the_audience_ends_its_line_with_more() {
        let mut resume = resume(Progress::default());
        resume.exact = false;
        assert!(line(&resume).ends_with("\tmore"));
    }

    #[test]
    fn an_episode_line_carries_its_season_and_episode() {
        let line = line(&resume(Progress {
            play: "two".into(),
            position: 1_300,
            duration: 1_320,
            finished: true,
            season: 2,
            episode: 5,
            ..Progress::default()
        }));
        assert!(line.contains("\tS2E5\t1300/1320\tfinished\t"), "{line}");
    }

    #[test]
    fn a_play_that_stopped_short_reads_as_neither() {
        let line = line(&resume(Progress {
            play: "three".into(),
            position: 60,
            duration: 6_000,
            ..Progress::default()
        }));
        assert!(line.contains("\t60/6000\t-\t"), "{line}");
    }

    #[test]
    fn the_epoch_stamps_as_midnight() {
        assert_eq!(stamp(0), "1970-01-01T00:00:00Z");
        assert_eq!(stamp(86_399), "1970-01-01T23:59:59Z");
    }
}
