// The index measured at the size of a large collection. The invented
// catalog has the shape of a real one (titles, episodes with plots,
// aliases, contributors in these proportions) and made-up words, because
// the two assertions are about the arrays' size and the read's time,
// and neither depends on what the words are.

use std::time::Instant;

use super::super::{Builder, Index, Item, Kind, Person, Where};
use crate::catalog::Slot;

// The size of the invented collection, several times the local catalog
// measured on 2026-09-05 in every table.
const MOVIES: u32 = 5_000;
const SERIALS: u32 = 500;
const EPISODES: u32 = 120;
const PEOPLE: u32 = 100_000;
const ALIASES: u32 = 80_000;

// How many words a plot holds, about the length of a real overview.
const PLOT_WORDS: usize = 50;

/// The ceiling plan 39 sets on the index.
const CEILING: usize = 20 * 1024 * 1024;

// The ceiling on one read, in milliseconds. An unoptimized build answers
// in about 10 ms at this size. The bound is loose because the coverage
// build is instrumented and slower, and it still catches a read that
// becomes quadratic.
const READ_CEILING: u128 = 200;

const LIBRARY: &str = "screening/features";
const SERIALS_LIBRARY: &str = "screening/serials";

// A counter that plays the part of a random draw. The test needs
// spread, not randomness, and a counter gives the same index on every
// run.
struct Draw(u64);

impl Draw {
    fn next(&mut self, span: usize) -> usize {
        self.0 = self
            .0
            .wrapping_mul(6_364_136_223_846_793_005)
            .wrapping_add(1);
        (self.0 >> 33) as usize % span
    }
}

// The invented vocabulary plots draw from, about the size of the word
// list a real catalog's overviews make.
fn vocabulary() -> Vec<String> {
    let heads = [
        "ba", "ce", "di", "fo", "gu", "ha", "ke", "li", "mo", "nu", "pa", "re", "si", "to", "vu",
        "wa", "ze", "bri", "cla", "dro",
    ];
    let tails = [
        "nder", "ket", "ling", "mor", "ntic", "rest", "sel", "tion", "vast", "wick",
    ];
    let ends = ["", "s", "ed", "ing", "er", "ly", "al", "ish", "ous", "y"];
    let mut words = Vec::with_capacity(heads.len() * tails.len() * ends.len());
    for head in heads {
        for tail in tails {
            for end in ends {
                words.push(format!("{head}{tail}{end}"));
            }
        }
    }
    words
}

// The invented names people carry. Real names repeat first and last
// names across many people, so two short pools give the same sharing.
fn names() -> (Vec<String>, Vec<String>) {
    let firsts = (0..300).map(|at| format!("Ashen{at:03}")).collect();
    let lasts = (0..300).map(|at| format!("Corran{at:03}")).collect();
    (firsts, lasts)
}

// One invented plot.
fn plot(draw: &mut Draw, words: &[String]) -> String {
    let mut plot = String::with_capacity(320);
    for at in 0..PLOT_WORDS {
        if at > 0 {
            plot.push(' ');
        }
        plot.push_str(&words[draw.next(words.len())]);
    }
    plot.push('.');
    plot
}

// One movie or series of the invented catalog.
fn title(library: &str, word: &str, id: String, name: String, released: String) -> Item {
    Item {
        slot: Slot {
            library: library.into(),
            kind: word.into(),
            art: format!("posters/{id}.jpg"),
            released,
            rating: "PG-13".into(),
            tagline: format!("The {name} of its kind."),
            id,
            title: name.clone(),
            ..Slot::default()
        },
        sort_key: name.to_lowercase(),
        kind: Kind::Title,
        strings: vec![(Where::Title, name)],
    }
}

// The whole invented collection, folded into one index.
fn collection() -> Index {
    let words = vocabulary();
    let (firsts, lasts) = names();
    let mut draw = Draw(1);
    let mut builder = Builder::new();

    for number in 1..=MOVIES {
        let name = format!(
            "{} {}",
            words[draw.next(words.len())],
            words[draw.next(words.len())]
        );
        let mut item = title(
            LIBRARY,
            "movies",
            format!("movie:tmdb:{number}"),
            name,
            format!("{}", 1_920 + number % 106),
        );
        item.strings.push((Where::Plot, plot(&mut draw, &words)));
        builder.add(item);
    }

    // The one title the read test searches for.
    builder.add(title(
        LIBRARY,
        "movies",
        "movie:tmdb:268".into(),
        "Batman".into(),
        "1989".into(),
    ));

    for number in 1..=SERIALS {
        let name = format!(
            "{} {}",
            words[draw.next(words.len())],
            words[draw.next(words.len())]
        );
        let mut item = title(
            SERIALS_LIBRARY,
            "series",
            format!("series:tvdb:{number}"),
            name,
            format!("{}", 1_960 + number % 66),
        );
        item.strings.push((Where::Plot, plot(&mut draw, &words)));
        let place = builder.add(item);
        for episode in 1..=EPISODES {
            builder.fold(
                place,
                Where::EpisodeTitle,
                &format!("{} {episode}", words[draw.next(words.len())]),
            );
            let told = plot(&mut draw, &words);
            builder.fold(place, Where::EpisodePlot, &told);
        }
    }

    for number in 0..PEOPLE {
        let name = format!(
            "{} {}",
            firsts[draw.next(firsts.len())],
            lasts[draw.next(lasts.len())]
        );
        builder.person(Person {
            library: LIBRARY.into(),
            path: format!(".contributors/{name} {number}"),
            name,
            headshot: true,
        });
    }

    for number in 0..ALIASES {
        let place = super::super::Place(draw.next(MOVIES as usize) as u32);
        builder.fold(place, Where::Alias, &format!("movie:imdb:tt{number:07}"));
    }

    builder.finish()
}

#[test]
fn a_large_collection_folds_under_the_ceiling_and_answers_a_keystroke() {
    let index = collection();
    let size = index.size();

    let started = Instant::now();
    let hits = index.find("batman");
    let took = started.elapsed().as_millis();

    assert_eq!(hits.first().map(|hit| hit.title.as_str()), Some("Batman"));
    assert!(
        size.bytes < CEILING,
        "the index holds {} bytes over {} items, {} words and {} entries",
        size.bytes,
        size.items,
        size.words,
        size.entries
    );
    assert!(
        took < READ_CEILING,
        "one read took {took} ms over {} items",
        size.items
    );
}
