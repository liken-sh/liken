// The invented catalog the tests of a person's page read: one person
// credited in films and one series, with the plays the tests name.

use std::collections::HashMap;

use crate::catalog::Person as Entry;
use crate::catalog::{
    Answer, Change, Credits, Episode, FileFacts, Franchise, GenreEntry, LibraryEntry, Membership,
    MovieDetails, MovieSet, PlayItem, Progress, Query, Reach, Selection, SeriesDetails, Slot,
    Source, Work,
};
use crate::harness::Waker;

pub const LIBRARY: &str = "screening/films";
pub const PATH: &str = ".contributors/A Player";

// The number of the one series among the works.
const SERIES: usize = 3;

// A catalog of one person credited in movies and one series, so the page
// holds both kinds and a select opens each. The works are numbered from
// 1, and the `n`th came out in `2000 - n`, so the first is the newest.
#[derive(Default)]
pub struct People {
    // Whether the person's entry holds a biography file.
    pub biography: bool,
    // Whether the person's entry left the store, which is what a
    // re-read finds after the scanner dropped them.
    pub gone: bool,
    // How many works the person is credited in.
    pub works: usize,
    // Whether the person is credited as an actor alone.
    pub acting: bool,
    // Whether the scanner has credited the person in one more work,
    // released before every other.
    pub older: bool,
    // Whether one more work has no release date.
    pub undated: bool,
    // The entry's date of birth.
    pub born: &'static str,
    // Who finished which work: a film's every play past the finished
    // mark, or every episode of the series.
    pub finished: Vec<(&'static str, usize)>,
}

pub fn id(number: usize) -> String {
    format!("title:{number}")
}

impl People {
    fn work(&self, number: usize) -> Slot {
        Slot {
            library: LIBRARY.to_string(),
            kind: match number == SERIES {
                true => "series".into(),
                false => "movies".into(),
            },
            id: id(number),
            title: format!("Title {number}"),
            released: format!("{}-05-02", 2000 - number),
            art: format!("{number}.jpg"),
            parts: match self.acting {
                true => "as The Part".into(),
                false => "Director, as The Part".into(),
            },
            ..Slot::default()
        }
    }

    fn finished(&self, person: &str, number: usize) -> bool {
        self.finished
            .iter()
            .any(|(name, work)| *name == person && *work == number)
    }
}

impl Source for People {
    fn franchises(&mut self) -> Vec<crate::catalog::FranchiseEntry> {
        Vec::new()
    }

    fn franchises_of(&mut self, _library: &str, _id: &str) -> Vec<Membership> {
        Vec::new()
    }

    fn franchise(&mut self, _library: &str, _id: &str) -> Option<Franchise> {
        None
    }

    fn libraries(&mut self) -> Vec<LibraryEntry> {
        Vec::new()
    }

    fn genres(&mut self) -> Vec<GenreEntry> {
        Vec::new()
    }

    // The works newest first, as the catalog answers them.
    fn wall(&mut self, query: &Query) -> Answer {
        let Query::Person { path, .. } = query else {
            return Answer::default();
        };
        if path != PATH || self.gone {
            return Answer::default();
        }
        let older = self.older.then(|| Slot {
            id: "title:older".into(),
            released: "1900-05-02".into(),
            ..self.work(0)
        });
        let undated = self.undated.then(|| Slot {
            id: "title:undated".into(),
            released: String::new(),
            ..self.work(0)
        });
        Answer {
            name: "A Player".into(),
            slots: (1..=self.works)
                .map(|number| self.work(number))
                .chain(older)
                .chain(undated)
                .collect(),
        }
    }

    fn movie(&mut self, _library: &str, id: &str) -> Option<MovieDetails> {
        (id != "title:3").then(|| MovieDetails {
            title: format!("Film {id}"),
            backdrop: format!("{id}.backdrop.jpg"),
            ..MovieDetails::default()
        })
    }

    fn series(&mut self, _library: &str, id: &str) -> Option<SeriesDetails> {
        (id == "title:3").then(|| SeriesDetails {
            title: "The Serial".into(),
            backdrop: "serial.backdrop.jpg".into(),
            ..SeriesDetails::default()
        })
    }

    // The series holds two episodes, the second aired long ago, so the
    // series no longer runs.
    fn episodes(&mut self, _library: &str, series: &str) -> Vec<Episode> {
        if series != id(SERIES) {
            return Vec::new();
        }
        (1..=2)
            .map(|episode| Episode {
                id: format!("episode:{episode}"),
                season: 1,
                episode,
                released: "1998-01-01".into(),
                ..Episode::default()
            })
            .collect()
    }

    fn film_reach(&mut self, people: &[String]) -> HashMap<Work, Reach> {
        (1..=self.works)
            .filter(|number| *number != SERIES)
            .map(|number| {
                let reach = Reach {
                    people: people
                        .iter()
                        .filter(|person| self.finished(person, number))
                        .map(|person| (person.clone(), 1.0))
                        .collect(),
                };
                ((LIBRARY.to_string(), id(number)), reach)
            })
            .collect()
    }

    fn episode_progress(
        &mut self,
        _library: &str,
        series: &str,
        people: &[String],
    ) -> Vec<Progress> {
        let all = people
            .iter()
            .all(|person| series == id(SERIES) && self.finished(person, SERIES));
        match all {
            true => (1..=2)
                .map(|episode| Progress {
                    season: 1,
                    episode,
                    finished: true,
                    ..Progress::default()
                })
                .collect(),
            false => Vec::new(),
        }
    }

    fn set(&mut self, _library: &str, _id: &str) -> Option<MovieSet> {
        None
    }

    fn play(&mut self, _library: &str, _selection: &Selection) -> Vec<PlayItem> {
        Vec::new()
    }

    fn credits(&mut self, _library: &str, _id: &str) -> Credits {
        Credits::default()
    }

    fn person(&mut self, library: &str, path: &str) -> Option<Entry> {
        if path != PATH || self.gone {
            return None;
        }
        Some(Entry {
            library: library.to_string(),
            path: path.to_string(),
            name: "A Player".into(),
            born: self.born.into(),
            died: "2019-11-30".into(),
            biography: self.biography,
            headshot: true,
            biography_library: match self.biography {
                true => library.to_string(),
                false => String::new(),
            },
            biography_path: path.to_string(),
            headshot_library: library.to_string(),
            headshot_path: path.to_string(),
        })
    }

    fn files(&mut self, _library: &str, _item: &str) -> Vec<FileFacts> {
        Vec::new()
    }

    fn pool(&mut self) -> Vec<crate::catalog::pool::Candidate> {
        Vec::new()
    }

    fn changed(&mut self) -> Change {
        Change::None
    }

    fn wake_by(&mut self, _wake: Waker) {}
}
