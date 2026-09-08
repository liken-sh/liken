// The work that follows the one a page starts, from the leaves the
// continue-watching row already walks, and the three lines that row spells
// for a card.

use super::home::resume::containers::{self, Container, Episodes};
use super::home::resume::{self, Reason};
use super::{InFranchise, Item, Spelling};
use crate::catalog::progress::thread;
use crate::catalog::{Selection, Slot, Source};

/// The offer a `Play` carries: the three lines of the card, the art beside
/// them, and the block the browser gets back when a person takes the offer.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Next {
    pub reason: String,
    pub title: String,
    pub detail: String,
    pub art: String,
    pub request: Request,
}

/// What the browser needs to start the next work: the library, the choice,
/// and the franchise the run follows, where there is one.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Request {
    pub library: String,
    pub selection: Selection,
    pub via: Option<InFranchise>,
}

/// Where a play of the work one request names begins, and what follows that
/// work. The browser reads both when a person takes the offer on the film
/// that is playing, so the chain reads the store the way the page that
/// wrote the first request read it.
pub fn again(
    source: &mut dyn Source,
    request: &Request,
    people: &[String],
) -> (Option<i64>, Option<Next>) {
    let library = &request.library;
    match &request.selection {
        Selection::Episode {
            series,
            season,
            episode,
        } => {
            let numbers = (*season, *episode);
            let title = source
                .series(library, series)
                .map(|details| details.title)
                .unwrap_or_default();
            (
                episode_start(source, library, series, numbers, people),
                after_episode(
                    source,
                    library,
                    series,
                    &title,
                    numbers,
                    request.via.as_ref(),
                ),
            )
        }
        Selection::Movie { id } | Selection::Trailer { id } => {
            let set = source
                .movie(library, id)
                .map(|details| details.set_id)
                .unwrap_or_default();
            (
                film_start(source, library, id, people),
                after_film(
                    source,
                    library,
                    id,
                    (!set.is_empty()).then_some(set.as_str()),
                    request.via.as_ref(),
                ),
            )
        }
    }
}

// The second an episode starts at: where these people stopped in an episode
// they are in the middle of, and the beginning of every other one.
fn episode_start(
    source: &mut dyn Source,
    library: &str,
    series: &str,
    numbers: (i64, i64),
    people: &[String],
) -> Option<i64> {
    source
        .episode_progress(library, series, people)
        .into_iter()
        .find(|row| (row.season, row.episode) == numbers)
        .filter(|row| !row.finished)
        .map(|row| row.position)
}

// The second a film starts at, by the thread rule over its plays, which is
// the rule the film's own page draws its Resume button by.
fn film_start(source: &mut dyn Source, library: &str, id: &str, people: &[String]) -> Option<i64> {
    let plays: Vec<thread::Play> = source
        .plays_of(library, id, people)
        .into_iter()
        .map(|play| thread::Play {
            leaf: 0,
            progress: play.progress,
            exact: play.exact,
        })
        .collect();
    thread::walk(1, &plays)
        .map(|offer| offer.standing)
        .filter(|standing| !standing.finished)
        .map(|standing| standing.position)
}

/// What follows one episode: the next leaf of the franchise the page was
/// reached through, or the next episode in aired order where the page was
/// reached any other way.
pub fn after_episode(
    source: &mut dyn Source,
    library: &str,
    series: &str,
    title: &str,
    numbers: (i64, i64),
    via: Option<&InFranchise>,
) -> Option<Next> {
    let container = match via {
        Some(place) => through(source, library, series, place)?,
        None => Container::Series {
            library: library.to_string(),
            id: series.to_string(),
            title: title.to_string(),
        },
    };
    offered(
        source,
        &container,
        &Here::Episode {
            library,
            series,
            numbers,
        },
    )
}

/// What follows one film: the next leaf of the franchise the page was
/// reached through, or the next member of the film's set where the page was
/// reached any other way, and nothing where the film is in neither.
pub fn after_film(
    source: &mut dyn Source,
    library: &str,
    id: &str,
    set: Option<&str>,
    via: Option<&InFranchise>,
) -> Option<Next> {
    let container = match via {
        Some(place) => through(source, library, id, place)?,
        None => Container::Set {
            library: library.to_string(),
            id: set?.to_string(),
        },
    };
    offered(source, &container, &Here::Film { library, id })
}

// The franchise a page was reached through, as the container of the work on
// that page, or nothing where the work is in no such order.
fn through(
    source: &mut dyn Source,
    library: &str,
    id: &str,
    place: &InFranchise,
) -> Option<Container> {
    source
        .franchises_of(library, id)
        .into_iter()
        .find(|held| held.library == place.library && held.id == place.id)
        .map(Container::Franchise)
}

// The work a page is on, as the leaf of a container that matches it.
enum Here<'a> {
    Film {
        library: &'a str,
        id: &'a str,
    },
    Episode {
        library: &'a str,
        series: &'a str,
        numbers: (i64, i64),
    },
}

impl Here<'_> {
    fn names(&self, slot: &Slot) -> bool {
        match self {
            Self::Film { library, id } => {
                slot.library == *library && slot.id == *id && slot.episode.is_none()
            }
            Self::Episode {
                library,
                series,
                numbers,
            } => {
                slot.library == *library
                    && slot.episode.as_ref().is_some_and(|place| {
                        place.series == *series && (place.season, place.episode) == *numbers
                    })
            }
        }
    }
}

// The leaf after the one the page is on, spelled the way the row spells a
// card, or nothing where the container ends on that leaf.
fn offered(source: &mut dyn Source, container: &Container, here: &Here) -> Option<Next> {
    let leaves = containers::leaves(source, &mut Episodes::default(), container);
    let at = leaves.iter().position(|leaf| here.names(&leaf.slot))?;
    let leaf = leaves.get(at + 1)?;
    let title = containers::title(source, container)?;
    let reason = resume::next_in(container, leaf.member, title);
    Some(spelled(&leaf.slot, &reason))
}

// One leaf as the offer, with every line built by the speller that builds
// the continue-watching row's card.
fn spelled(slot: &Slot, reason: &Reason) -> Next {
    Next {
        reason: resume::under(slot, std::slice::from_ref(reason)),
        // The browser's own card leads a film with its tagline, because the
        // poster under it carries the title. The offer card draws no
        // poster, so the title is the line. An episode keeps the number and
        // the name the row spells.
        title: match slot.episode.is_some() {
            true => Item::resumed(slot.clone(), String::new(), None).caption,
            false => slot.title.clone(),
        },
        detail: Item::spelled(slot.clone(), Spelling::Facts).under,
        art: slot.art.clone(),
        request: Request {
            library: slot.library.clone(),
            selection: chosen(slot),
            via: resume::in_franchise(reason),
        },
    }
}

// The choice one leaf resolves to: an episode by its aired numbers, and a
// film by its id.
fn chosen(slot: &Slot) -> Selection {
    match &slot.episode {
        Some(place) => Selection::Episode {
            series: place.series.clone(),
            season: place.season,
            episode: place.episode,
        },
        None => Selection::Movie {
            id: slot.id.clone(),
        },
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::InSeries;

    // One film leaf whose sidecar wrote a tagline, which the browser's own
    // card would lead with.
    fn film() -> Slot {
        Slot {
            library: "screening/films".into(),
            kind: "movies".into(),
            id: "movies:2".into(),
            title: "Entry 2".into(),
            released: "1980".into(),
            art: "movies:2.jpg".into(),
            duration: 5_400,
            rating: "PG".into(),
            tagline: "One line of it.".into(),
            ..Slot::default()
        }
    }

    // One episode leaf of the same shape the row's walk builds.
    fn episode() -> Slot {
        Slot {
            library: "screening/serials".into(),
            kind: "episodes".into(),
            id: "episode:1:3".into(),
            title: "Segment 3".into(),
            released: "1980".into(),
            art: "s1e3.jpg".into(),
            duration: 2_760,
            episode: Some(InSeries {
                series: "series:1".into(),
                name: "The Serial".into(),
                season: 1,
                episode: 3,
            }),
            ..Slot::default()
        }
    }

    #[test]
    fn a_films_line_is_its_title_and_never_its_tagline() {
        let next = spelled(&film(), &Reason::Set("The Entries".into()));

        assert_eq!(next.title, "Entry 2");
        assert_eq!(next.reason, "Next in The Entries");
        assert_eq!(next.detail, "1980 · 1h 30m · PG");
        assert_eq!(next.art, "movies:2.jpg");
    }

    #[test]
    fn an_episodes_line_is_its_number_and_its_name() {
        let next = spelled(&episode(), &Reason::Series("The Serial".into()));

        assert_eq!(next.title, "E03 · Segment 3");
        assert_eq!(next.reason, "Next in The Serial · S01");
        assert_eq!(next.detail, "The Serial · S01 · E03 · 46m");
    }
}
