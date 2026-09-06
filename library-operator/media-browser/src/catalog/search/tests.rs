mod scale;

use super::*;
use crate::catalog::Slot;

// The invented library every ranking test builds in.
const LIBRARY: &str = "screening/features";

// One title of the fixture, with the aliases the catalog's aliases table
// would hold for it.
fn titled(
    builder: &mut Builder,
    word: &str,
    id: &str,
    title: &str,
    released: &str,
    aliases: &[&str],
) -> Place {
    let mut strings = vec![(Where::Title, title.to_string())];
    strings.extend(
        aliases
            .iter()
            .map(|alias| (Where::Alias, (*alias).to_string())),
    );
    builder.add(Item {
        slot: Slot {
            library: LIBRARY.into(),
            kind: word.into(),
            id: id.into(),
            title: title.into(),
            released: released.into(),
            ..Slot::default()
        },
        sort_key: title.to_lowercase(),
        kind: Kind::Title,
        strings,
    })
}

fn movie(builder: &mut Builder, id: &str, title: &str, released: &str, aliases: &[&str]) -> Place {
    titled(builder, "movies", id, title, released, aliases)
}

fn series(builder: &mut Builder, id: &str, title: &str, released: &str, aliases: &[&str]) -> Place {
    titled(builder, "series", id, title, released, aliases)
}

// The titles the hits carry, in the read's order.
fn titles(hits: &[Slot]) -> Vec<&str> {
    hits.iter().map(|hit| hit.title.as_str()).collect()
}

// The five items plan 39's proof names: a movie titled with the word, a
// movie that starts with it, a movie with it as a later word, a series
// with it as an alias, and a series whose episode's plot names it.
fn caped() -> Index {
    let mut builder = Builder::new();
    movie(&mut builder, "movie:1", "Batman", "1989", &["batman-1989"]);
    movie(&mut builder, "movie:2", "Batman Begins", "2005", &[]);
    movie(&mut builder, "movie:3", "The Batman", "2022", &[]);
    series(
        &mut builder,
        "series:1",
        "The Caped Crusader",
        "1992",
        &["Batman"],
    );
    let nightly = series(&mut builder, "series:2", "Nightly", "1996", &[]);
    builder.fold(nightly, Where::EpisodeTitle, "The Long Fall");
    builder.fold(
        nightly,
        Where::EpisodePlot,
        "A reporter follows Batman across the rooftops and files nothing.",
    );
    builder.finish()
}

#[test]
fn a_word_ranks_a_title_over_an_alias_over_a_plot() {
    let hits = caped().find("batman");
    assert_eq!(
        titles(&hits),
        [
            "Batman",
            "Batman Begins",
            "The Batman",
            "The Caped Crusader",
            "Nightly",
        ]
    );
}

#[test]
fn every_word_of_a_query_must_match_the_same_item() {
    let hits = caped().find("batman 1989");
    assert_eq!(titles(&hits), ["Batman"]);
}

#[test]
fn a_query_of_nothing_answers_nothing() {
    let index = caped();
    assert!(index.find("").is_empty());
    assert!(index.find("   ").is_empty());
    assert!(index.find("zzz").is_empty());
}

#[test]
fn a_diacritic_folds_away_on_both_sides() {
    let mut builder = Builder::new();
    movie(&mut builder, "movie:4", "Amélie", "2001", &[]);
    let index = builder.finish();
    assert_eq!(titles(&index.find("amelie")), ["Amélie"]);
    assert_eq!(titles(&index.find("AMÉLIE")), ["Amélie"]);
}

#[test]
fn a_substring_inside_a_word_is_the_last_kind_of_match() {
    let mut builder = Builder::new();
    movie(&mut builder, "movie:5", "Man of Steel", "2013", &[]);
    movie(&mut builder, "movie:6", "Batman", "1989", &[]);
    let index = builder.finish();
    assert_eq!(titles(&index.find("man")), ["Man of Steel", "Batman"]);
}

#[test]
fn a_person_answers_as_the_slot_their_page_opens_from() {
    let mut builder = Builder::new();
    builder.person(Person {
        library: LIBRARY.into(),
        path: ".contributors/A Player".into(),
        name: "A Player".into(),
        headshot: true,
    });
    builder.person(Person {
        library: LIBRARY.into(),
        path: ".contributors/A Player Unseen".into(),
        name: "A Player Unseen".into(),
        headshot: false,
    });
    let hits = builder.finish().find("player");
    assert_eq!(hits.len(), 2);
    assert_eq!(hits[0].kind, "people");
    assert_eq!(hits[0].library, LIBRARY);
    assert_eq!(hits[0].id, ".contributors/A Player");
    assert_eq!(hits[0].art, ".contributors/A Player/headshot.jpg");
    assert_eq!(hits[1].id, ".contributors/A Player Unseen");
    assert_eq!(hits[1].art, "");
}

#[test]
fn an_episode_answers_as_its_series_once_however_many_of_them_match() {
    let mut builder = Builder::new();
    let place = series(&mut builder, "series:3", "The Serial", "1970", &[]);
    builder.fold(place, Where::EpisodeTitle, "The Coppice");
    builder.fold(place, Where::EpisodePlot, "The coppice at dusk.");
    let hits = builder.finish().find("coppice");
    assert_eq!(titles(&hits), ["The Serial"]);
    assert_eq!(hits[0].id, "series:3");
}

// One set of the fixture, with a sort key that differs from its title.
fn collection(builder: &mut Builder, id: &str, released: &str, sort_key: &str) {
    builder.add(Item {
        slot: Slot {
            library: LIBRARY.into(),
            kind: "sets".into(),
            id: id.into(),
            title: "Drift".into(),
            released: released.into(),
            ..Slot::default()
        },
        sort_key: sort_key.into(),
        kind: Kind::Collection,
        strings: vec![(Where::Title, "Drift".into())],
    });
}

#[test]
fn a_tie_breaks_by_kind_then_by_the_newest_release_then_by_the_sort_key() {
    let mut builder = Builder::new();
    collection(&mut builder, "set:2", "2020", "second drift");
    movie(&mut builder, "movie:7", "Drift", "1999", &[]);
    collection(&mut builder, "set:1", "2020", "first drift");
    movie(&mut builder, "movie:8", "Drift", "2011", &[]);
    let hits = builder.finish().find("drift");
    let named: Vec<&str> = hits.iter().map(|hit| hit.id.as_str()).collect();
    assert_eq!(named, ["movie:8", "movie:7", "set:1", "set:2"]);
}

#[test]
fn an_index_reports_what_it_holds() {
    let size = caped().size();
    assert_eq!(size.items, 5);
    assert_eq!(size.entries, 9);
    assert!(size.words >= 12, "the vocabulary holds every folded word");
    assert!(size.bytes > 0);
}
