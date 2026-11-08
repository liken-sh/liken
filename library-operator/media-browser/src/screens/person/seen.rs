// What the people at the screen have seen of each of a person's credits.
// A person has seen a film when the finished rule counts one of their
// plays as finished, which the reach of plan 86 gives as a share of 1. A
// person has seen a series when they finished every episode the library
// holds. The dots, the bars, the facts line, and the label all count by
// this one rule, so they agree.

use super::credit::{Credit, Seen};
use crate::audience::Viewer;
use crate::catalog::{Reach, Source};
use crate::screens::reached::Reached;

// The kind word a series' slot carries. A series poster draws no bar,
// and its seen state counts episodes.
const SERIES: &str = "series";

/// Read what these people have seen of every credit, and hang the bar
/// and the faces under each film's poster, the way every film poster
/// draws them. With nobody at the screen no credit is seen.
pub fn read(
    credits: &mut [Credit],
    source: &mut dyn Source,
    people: &[String],
    viewers: &[Viewer],
) {
    let reach = source.film_reach(people);
    for credit in credits.iter_mut() {
        let work = credit.work();
        let film = reach.get(&work);
        let series = credit.item.kind == SERIES;
        credit.item.reached = match series {
            true => Reached::default(),
            false => Reached::of(film, people, viewers),
        };
        let seen = match series {
            true => finished_series(source, credit, people),
            false => finished_film(film, people),
        };
        credit.seen = Seen::of(seen, people.len());
    }
}

// How many of these people finished the film.
fn finished_film(reach: Option<&Reach>, people: &[String]) -> usize {
    let Some(reach) = reach else {
        return 0;
    };
    reach
        .people
        .iter()
        .filter(|(name, share)| people.contains(name) && *share >= 1.0)
        .count()
}

// How many of these people finished every episode of the series that the
// library holds. A series that holds no episode is seen by nobody.
fn finished_series(source: &mut dyn Source, credit: &Credit, people: &[String]) -> usize {
    if credit.episodes == 0 {
        return 0;
    }
    people
        .iter()
        .filter(|person| {
            let rows = source.episode_progress(
                &credit.item.library,
                &credit.item.id,
                std::slice::from_ref(*person),
            );
            rows.iter().filter(|row| row.finished).count() >= credit.episodes
        })
        .count()
}

/// How many credits everyone at the screen has seen.
pub fn everyone(credits: &[Credit]) -> usize {
    credits
        .iter()
        .filter(|credit| credit.seen == Seen::Everyone)
        .count()
}
