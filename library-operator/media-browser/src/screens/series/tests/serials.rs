use super::super::*;
use crate::catalog::franchise::{Entry, Held, SERIES as SERIES_KIND};
use crate::catalog::{Answer, Episode};
use crate::catalog::{
    Change, Credit, CreditSlot, Credits, FileFacts, Franchise, GenreEntry, LibraryEntry,
    Membership, MovieDetails, MovieSet, Person, PlayItem, Query, SeriesDetails,
};
use crate::harness::Waker;

pub const SERIES: &str = "series:one";

// Three seasons of five, three, and six episodes, which fill two rows,
// one row, and two rows at four across.
const SEASONS: [i64; 3] = [5, 3, 6];

#[derive(Default)]
pub struct Serials {
    // Whether the series holds any episode at all. A library the scanner
    // has not reached looks like that.
    pub empty: bool,
    // How many episodes the last season holds, so a re-read can shorten
    // the wall under the focus.
    pub last: Option<i64>,
    // Whether the series credits anybody at all.
    pub credits: bool,
    // Whether the episodes carry no release at all, which is what a
    // library the enrichers have not reached looks like.
    pub undated: bool,
    // Whether the series belongs to a franchise, which puts a strip
    // between the last season and the stripes.
    pub franchise: bool,
    // The episode count of each season, or the three of SEASONS where the
    // test names none.
    pub seasons: Vec<i64>,
    // How far the audience reached in each episode a play of theirs names,
    // and the people the last read of it named.
    pub progress: Vec<crate::catalog::Progress>,
    pub watching: Vec<String>,
}

impl Serials {
    // The episode count of every season.
    fn counts(&self) -> Vec<i64> {
        let mut counts = match self.seasons.is_empty() {
            true => SEASONS.to_vec(),
            false => self.seasons.clone(),
        };
        if let (Some(last), Some(end)) = (self.last, counts.last_mut()) {
            *end = last;
        }
        counts
    }
}

// The Library of kind franchises the fake holds, and the one franchise
// in it.
const ORDERS: &str = "screening/orders";
const CYCLE: &str = "franchise:name:the-cycle";

impl Source for Serials {
    fn franchises(&mut self) -> Vec<crate::catalog::FranchiseEntry> {
        Vec::new()
    }

    // One franchise of two serials, which is what puts a franchise
    // strip on the page.
    fn franchises_of(&mut self, _library: &str, _id: &str) -> Vec<Membership> {
        if !self.franchise {
            return Vec::new();
        }
        vec![Membership {
            movies: 0,
            series: 2,
            library: ORDERS.into(),
            id: CYCLE.into(),
            title: "The Cycle".into(),
            members: [SERIES, "series:two"]
                .iter()
                .enumerate()
                .map(|(position, id)| Entry {
                    position: position as i64,
                    kind: SERIES_KIND.into(),
                    alias: format!("series:tvdb:{position}"),
                    title: format!("Serial {id}"),
                    held: Some(Held {
                        arts: Vec::new(),
                        library: "screening/serials".into(),
                        id: (*id).to_string(),
                        kind: "series".into(),
                        title: format!("Serial {id}"),
                        art: format!("{id}.jpg"),
                        released: "2004".into(),
                        slug: (*id).to_string(),
                        tagline: String::new(),
                        plot: String::new(),
                        duration: 0,
                    }),
                    ..Entry::default()
                })
                .collect(),
        }]
    }

    fn franchise(&mut self, library: &str, id: &str) -> Option<Franchise> {
        if !self.franchise || library != ORDERS || id != CYCLE {
            return None;
        }
        Some(Franchise {
            library: library.to_string(),
            id: id.to_string(),
            title: "The Cycle".into(),
            entries: self.franchises_of("", "").remove(0).members,
            ..Franchise::default()
        })
    }

    fn libraries(&mut self) -> Vec<LibraryEntry> {
        Vec::new()
    }

    fn genres(&mut self) -> Vec<GenreEntry> {
        Vec::new()
    }

    fn wall(&mut self, _query: &Query) -> Answer {
        Answer::default()
    }

    fn movie(&mut self, _library: &str, _id: &str) -> Option<MovieDetails> {
        None
    }

    fn set(&mut self, _library: &str, _id: &str) -> Option<MovieSet> {
        None
    }

    fn series(&mut self, _library: &str, id: &str) -> Option<SeriesDetails> {
        // The second serial is the one a press on the franchise strip
        // opens, and it carries the same body as the first.
        if id != SERIES && !(self.franchise && id == "series:two") {
            return None;
        }
        Some(SeriesDetails {
            title: "Serial One".into(),
            released: "2004".into(),
            rating: "TV-14".into(),
            tagline: "One line of it.".into(),
            plot: "The series' own plot.".into(),
            creators: vec!["A Creator".into()],
            cast: vec![Credit {
                name: "A Player".into(),
                role: "The Part".into(),
            }],
            studios: vec!["A Studio".into()],
            ratings: vec![("imdb".into(), 8.3), ("tomatometerallcritics".into(), 95.0)],
            backdrop: "backdrop.jpg".into(),
            seasons: self.counts().len() as i64,
            ..SeriesDetails::default()
        })
    }

    fn episodes(&mut self, _library: &str, id: &str) -> Vec<Episode> {
        if id != SERIES || self.empty {
            return Vec::new();
        }
        let undated = self.undated;
        self.counts()
            .into_iter()
            .enumerate()
            .flat_map(|(index, count)| {
                let season = index as i64 + 1;
                (1..=count).map(move |episode| Episode {
                    id: format!("episode:{season}:{episode}"),
                    season,
                    episode,
                    title: format!("Segment {episode}"),
                    released: match undated {
                        true => String::new(),
                        false => format!("{}-03-0{episode}", 2003 + season),
                    },
                    duration: 2_760,
                    plot: format!("The plot of S{season} E{episode}."),
                    art: format!("s{season}e{episode}.jpg"),
                })
            })
            .collect()
    }

    fn play(&mut self, _library: &str, _selection: &Selection) -> Vec<PlayItem> {
        Vec::new()
    }

    fn credits(&mut self, _library: &str, _id: &str) -> Credits {
        if !self.credits {
            return Credits::default();
        }
        Credits {
            directors: vec![slot("A Director")],
            writers: Vec::new(),
            cast: vec![slot("A Player"), slot("Another")],
        }
    }

    fn person(&mut self, library: &str, path: &str) -> Option<Person> {
        Some(Person {
            library: library.to_string(),
            path: path.to_string(),
            name: path.rsplit('/').next()?.to_string(),
            ..Person::default()
        })
    }

    // Every episode holds one video file of its own size, so the foot
    // reads differently for every still.
    fn files(&mut self, _library: &str, item: &str) -> Vec<FileFacts> {
        let number: i64 = item
            .rsplit(':')
            .next()
            .and_then(|digits| digits.parse().ok())
            .unwrap_or(0);
        if number == 0 {
            return Vec::new();
        }
        vec![FileFacts {
            role: "primary".into(),
            kind: "video".into(),
            video_codec: "x264".into(),
            audio_codec: "AAC".into(),
            width: 1_920,
            height: 1_080,
            size_bytes: number * 1_000_000_000,
            ..FileFacts::default()
        }]
    }

    fn pool(&mut self) -> Vec<crate::catalog::pool::Candidate> {
        Vec::new()
    }

    fn changed(&mut self) -> Change {
        Change::None
    }

    fn episode_progress(
        &mut self,
        _library: &str,
        _series: &str,
        people: &[String],
    ) -> Vec<crate::catalog::Progress> {
        self.watching = people.to_vec();
        self.progress.clone()
    }

    // The same plays as whole rows, every one the audience's own, so the
    // wall opens on the episode the marks say.
    fn plays_of(
        &mut self,
        _library: &str,
        _id: &str,
        _people: &[String],
    ) -> Vec<crate::catalog::Resume> {
        self.progress
            .iter()
            .map(|progress| crate::catalog::Resume {
                progress: progress.clone(),
                exact: true,
                ..crate::catalog::Resume::default()
            })
            .collect()
    }

    fn wake_by(&mut self, _wake: Waker) {}
}

// One credit of the invented series, with an entry in the
// contributor store.
fn slot(name: &str) -> CreditSlot {
    CreditSlot {
        name: name.to_string(),
        role: String::new(),
        contributor: format!(".contributors/{name}"),
        headshot: true,
    }
}

// A page whose series credits a director and two players.
pub fn credited(serials: Serials) -> (Series, Serials) {
    page(Serials {
        credits: true,
        ..serials
    })
}

pub fn page(serials: Serials) -> (Series, Serials) {
    let mut source = serials;
    let page =
        Series::open("screening/serials", SERIES, &mut source).expect("the catalog holds it");
    (page, source)
}

// One press on a page, with the source it reads from.
pub fn pressed(page: &mut Series, source: &mut Serials, key: &str) -> Focus {
    page.key(key, source);
    page.focus
}

// The focused still's index, for a press that stays on the wall.
pub fn still(focus: Focus) -> usize {
    match focus {
        Focus::Still(index) => index,
        Focus::Rail(..) | Focus::Franchise(..) | Focus::Stripe(..) => {
            panic!("focus left the wall")
        }
    }
}
