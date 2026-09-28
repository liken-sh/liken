// The search index over the invented catalog. `Catalog` is a unit
// struct that every caller constructs fresh, so it can hold no index;
// the index is built once, on the first search, and shared.

use std::collections::HashSet;
use std::sync::OnceLock;

use super::{
    Catalog, FEATURES, IN_SETS, MOVIES, PER_SET, PLOT, SERIALS, SERIALS_LIBRARY, movie, orders,
    serial, serial_slot,
};
use crate::catalog::search::{Builder, Index, Item, Kind, Person, Where};
use crate::catalog::{Slot, Source};

/// The index over the invented catalog, built on first use.
pub fn index() -> &'static Index {
    static INDEX: OnceLock<Index> = OnceLock::new();
    INDEX.get_or_init(build)
}

fn build() -> Index {
    let mut builder = Builder::new();
    let mut catalog = Catalog;

    for number in 1..=MOVIES {
        let slot = Slot::of(FEATURES, "movies", movie(number));
        builder.add(found(slot, Kind::Title, PLOT));
    }

    for number in 1..=SERIALS {
        let slot = serial_slot(SERIALS_LIBRARY, number);
        let id = slot.id.clone();
        let place = builder.add(found(slot, Kind::Title, PLOT));
        for episode in catalog.episodes(SERIALS_LIBRARY, &id) {
            builder.fold(place, Where::EpisodeTitle, &episode.title);
            builder.fold(place, Where::EpisodePlot, &episode.plot);
        }
    }

    let sets: Vec<(String, String)> = (1..=IN_SETS / PER_SET)
        .map(|number| format!("set:sample:{number:02}"))
        .filter_map(|id| catalog.set(FEATURES, &id).map(|set| (id, set.title)))
        .collect();
    for (id, title) in sets {
        let slot = Slot {
            library: FEATURES.into(),
            kind: "sets".into(),
            id,
            title,
            ..Slot::default()
        };
        builder.add(named(slot, Kind::Collection, Where::Title));
    }

    for entry in orders::entries() {
        let slot = Slot {
            library: entry.library,
            kind: "franchise".into(),
            id: entry.id,
            title: entry.title,
            art: entry.art,
            ..Slot::default()
        };
        builder.add(named(slot, Kind::Collection, Where::Title));
    }

    for (path, name) in contributors() {
        builder.person(Person {
            library: FEATURES.into(),
            path,
            name,
            headshot: true,
        });
    }

    builder.finish()
}

// One invented title with its plot, as the index holds it.
fn found(slot: Slot, kind: Kind, plot: &str) -> Item {
    let mut item = named(slot, kind, Where::Title);
    item.strings.push((Where::Plot, plot.to_string()));
    item
}

// One item found by its title alone.
fn named(slot: Slot, kind: Kind, rung: Where) -> Item {
    Item {
        sort_key: slot.title.to_lowercase(),
        strings: vec![(rung, slot.title.clone())],
        slot,
        kind,
    }
}

// Every invented person as path and name, each once.
fn contributors() -> Vec<(String, String)> {
    let mut catalog = Catalog;
    let mut seen: HashSet<String> = HashSet::new();
    let mut found: Vec<(String, String)> = Vec::new();
    let titles = (1..=MOVIES)
        .map(|number| movie(number).id)
        .chain((1..=SERIALS).map(|number| serial(number).id));
    for id in titles {
        let credits = catalog.credits(FEATURES, &id);
        let stripes = credits
            .directors
            .into_iter()
            .chain(credits.writers)
            .chain(credits.cast);
        for credit in stripes {
            if seen.insert(credit.contributor.clone()) {
                found.push((credit.contributor, credit.name));
            }
        }
    }
    found
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_invented_catalog_searches_by_title() {
        let hits = index().find("Specimen 0007");
        assert_eq!(
            hits.first().map(|hit| hit.title.as_str()),
            Some("Specimen 0007")
        );
        assert_eq!(hits[0].kind, "movies");
        assert_eq!(hits[0].library, FEATURES);
    }

    #[test]
    fn the_invented_catalog_searches_by_person_and_by_serial() {
        let hits = index().find("Serial 03");
        assert_eq!(
            hits.first().map(|hit| hit.id.as_str()),
            Some("series:sample:03")
        );

        let people = index().find("Player 0001-1");
        assert_eq!(people.first().map(|hit| hit.kind.as_str()), Some("people"));
        assert_eq!(people[0].id, ".contributors/Player 0001-1");
    }

    #[test]
    fn the_invented_index_reports_a_size() {
        let size = index().size();
        assert!(size.items > MOVIES as usize);
        assert!(size.entries > size.items);
        assert!(size.bytes > 0);
    }
}
