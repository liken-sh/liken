use super::*;

fn library(name: &str) -> Query {
    Query::Library {
        library: name.into(),
    }
}

#[test]
fn the_counts_match_the_entries() {
    let mut catalog = Catalog;
    let libraries = catalog.libraries();
    let movies = catalog.wall(&library("sample/features"));
    let serials = catalog.wall(&library("sample/serials"));
    assert_eq!(movies.slots.len() as u64, libraries[0].items);
    assert_eq!(serials.slots.len() as u64, libraries[1].items);
    assert_eq!(movies.name, "features");
    assert_eq!(serials.name, "serials");
}

#[test]
fn every_slot_of_a_library_wall_names_its_library_and_kind() {
    let mut catalog = Catalog;
    let movies = catalog.wall(&library("sample/features")).slots;
    assert!(
        movies
            .iter()
            .all(|slot| slot.library == "sample/features" && slot.kind == "movies")
    );
    let serials = catalog.wall(&library("sample/serials")).slots;
    assert!(
        serials
            .iter()
            .all(|slot| slot.library == "sample/serials" && slot.kind == "series")
    );
}

#[test]
fn a_persons_wall_is_headed_by_their_name() {
    let mut catalog = Catalog;
    let answer = catalog.wall(&Query::Person {
        library: "sample/features".into(),
        path: ".contributors/Player 0001-1".into(),
    });
    assert_eq!(answer.name, "Player 0001-1");
    assert_eq!(answer.slots.len(), 3);
    assert_eq!(
        catalog.wall(&Query::Person {
            library: "sample/features".into(),
            path: "nonsense".into(),
        }),
        Answer::default()
    );
}

#[test]
fn a_sets_wall_is_headed_by_its_title_and_holds_its_members() {
    let mut catalog = Catalog;
    let answer = catalog.wall(&Query::Set {
        library: "sample/features".into(),
        id: "set:sample:01".into(),
    });
    assert!(answer.name.starts_with("The Specimen Cycle"));
    assert_eq!(answer.slots.len() as i64, PER_SET);
    assert_eq!(answer.slots[0].id, "movie:sample:0001");
    assert_eq!(answer.slots[0].kind, "movies");
    assert_eq!(
        catalog.wall(&Query::Set {
            library: "sample/features".into(),
            id: "set:sample:77".into(),
        }),
        Answer::default()
    );
}

#[test]
fn the_catalog_is_deterministic() {
    let mut catalog = Catalog;
    assert_eq!(
        catalog.wall(&library("sample/features")),
        catalog.wall(&library("sample/features"))
    );
    assert_eq!(
        catalog.episodes("sample/serials", "series:sample:03"),
        catalog.episodes("sample/serials", "series:sample:03")
    );
}

#[test]
fn every_name_is_invented() {
    let mut catalog = Catalog;
    let movies = catalog.wall(&library("sample/features")).slots;
    assert!(
        movies
            .iter()
            .all(|slot| slot.title.starts_with("Specimen "))
    );
    let serials = catalog.wall(&library("sample/serials")).slots;
    assert!(serials.iter().all(|slot| slot.title.starts_with("Serial ")));
}

#[test]
fn every_serial_has_seasons_with_episodes() {
    let mut catalog = Catalog;
    let details = catalog
        .series("sample/serials", "series:sample:07")
        .expect("the sample holds this serial");
    assert_eq!(details.seasons, 3);
    assert!(!details.backdrop.is_empty());
    assert_eq!(details.cast.len(), 6);

    let episodes = catalog.episodes("sample/serials", "series:sample:07");
    assert_eq!(episodes.len(), 9 + 10 + 6);
    assert_eq!(episodes[0].season, 1);
    assert_eq!(episodes[0].episode, 1);
    assert_eq!(episodes[9].season, 2);
    assert!(!episodes[0].art.is_empty());
    assert!(!episodes[0].plot.is_empty());
    assert_eq!(episodes[0].released, "2000-02-02");
}

#[test]
fn the_home_strips_show_a_standalone_episode_a_folded_series_and_titles() {
    let mut catalog = Catalog;
    for query in [
        Query::Released {
            fold: crate::catalog::Fold::Airing,
        },
        Query::Added {
            fold: crate::catalog::Fold::Airing,
        },
    ] {
        let slots = catalog.wall(&query).slots;
        assert!(slots.len() <= recency::CANDIDATES * recency::PAGES);
        assert!(slots.iter().any(|slot| slot.still()));
        assert!(slots.iter().any(|slot| slot.kind == "series"));
        assert!(slots.iter().any(|slot| slot.kind == "movies"));
        assert_eq!(slots[0].kind, "episodes");
        assert_eq!(slots[0].library, SERIALS_LIBRARY);
        let stills = slots.iter().filter(|slot| slot.still()).count();
        assert_eq!(stills as i64, AIRING_EPISODES);
        assert_eq!(slots[AIRING_EPISODES as usize].kind, "series");
    }
}

// A page-by-page reference fixes the exact candidate prefix that enters
// the fold, independent of how a source fetches the bounded rows.
fn paged_reference(fold: crate::catalog::Fold, candidates: &[Candidate]) -> Vec<Slot> {
    let mut read = Vec::new();
    for page in 0..recency::PAGES {
        let candidates: Vec<Candidate> = candidates
            .iter()
            .skip(page * recency::CANDIDATES)
            .take(recency::CANDIDATES)
            .cloned()
            .collect();
        let short = candidates.len() < recency::CANDIDATES;
        read.extend(candidates);
        if short || recency::fold(read.clone(), fold).len() >= recency::FILL {
            break;
        }
    }
    recency::fold(read, fold)
}

#[test]
fn added_and_released_match_the_paged_reference_under_every_fold() {
    let today = recency::date_seconds("2026-09-03").expect("a full date");
    for fold in [
        crate::catalog::Fold::Titles,
        crate::catalog::Fold::Episodes,
        crate::catalog::Fold::Airing,
        crate::catalog::Fold::Shows { today },
    ] {
        let expected = paged_reference(
            fold,
            &recent(|candidate| released_of(candidate).to_string()),
        );
        let actual = Catalog.wall(&Query::Released { fold }).slots;
        assert_eq!(actual, expected, "Released with {fold:?}");

        let expected = paged_reference(fold, &recent(added_of));
        let actual = Catalog.wall(&Query::Added { fold }).slots;
        assert_eq!(actual, expected, "Added with {fold:?}");
    }
}

#[test]
fn released_comes_newest_first_and_added_by_arrival() {
    let mut catalog = Catalog;
    let released = catalog
        .wall(&Query::Released {
            fold: crate::catalog::Fold::Episodes,
        })
        .slots;
    assert!(
        released
            .windows(2)
            .all(|pair| pair[0].released >= pair[1].released)
    );
    let added = catalog
        .wall(&Query::Added {
            fold: crate::catalog::Fold::Titles,
        })
        .slots;
    assert!(added.iter().all(|slot| !slot.still()));
    assert_ne!(
        released[0].id,
        added.last().expect("the strip holds slots").id
    );
}

#[test]
fn every_library_draws_as_a_mosaic_of_its_newest_added_posters() {
    let mut catalog = Catalog;
    let libraries = catalog.libraries();
    assert!(libraries.iter().all(|entry| entry.art.len() == TILES));
    let added = catalog.wall(&Query::Added {
        fold: crate::catalog::Fold::Titles,
    });
    let newest = added
        .slots
        .iter()
        .find(|slot| slot.library == FEATURES)
        .expect("the added strip holds a movie");
    assert_eq!(libraries[0].art[0], newest.art);
    assert_eq!(libraries[1].art[0], serial(SERIALS).art);
}

#[test]
fn a_serial_the_sample_never_invented_has_no_page() {
    let mut catalog = Catalog;
    assert_eq!(catalog.series("sample/serials", "series:sample:99"), None);
    assert!(catalog.episodes("sample/serials", "nonsense").is_empty());
}

#[test]
fn the_sample_reports_no_changes_and_answers_no_reader() {
    assert!(!Catalog.changed());
    assert!(Catalog.reader().is_none());
}

#[test]
fn a_select_on_the_sample_resolves_no_file() {
    let mut catalog = Catalog;
    assert!(
        catalog
            .play(
                "sample/features",
                &Selection::Movie {
                    id: "movie:sample:0001".into()
                }
            )
            .is_empty()
    );
}

#[test]
fn a_movie_carries_a_page_with_a_backdrop_and_a_trailer() {
    let mut catalog = Catalog;
    let details = catalog
        .movie("sample/features", "movie:sample:0001")
        .expect("the sample holds this movie");
    assert_eq!(details.title, "Specimen 0001");
    assert!(!details.backdrop.is_empty());
    assert!(!details.trailer.is_empty());
    assert_eq!(details.cast.len(), 6);
    assert!(details.duration > 0);
}

#[test]
fn a_movie_the_sample_never_invented_has_no_page() {
    let mut catalog = Catalog;
    assert_eq!(catalog.movie("sample/features", "movie:sample:9999"), None);
    assert_eq!(catalog.movie("sample/features", "nonsense"), None);
}

#[test]
fn the_first_movies_fall_into_sets_and_the_rest_into_none() {
    let mut catalog = Catalog;
    let first = catalog
        .movie("sample/features", "movie:sample:0001")
        .expect("the sample holds this movie");
    assert_eq!(first.set_id, "set:sample:01");
    let later = catalog
        .movie(
            "sample/features",
            &format!("movie:sample:{:04}", IN_SETS + 1),
        )
        .expect("the sample holds this movie");
    assert!(later.set_id.is_empty());
}

#[test]
fn a_set_holds_its_own_members_in_order() {
    let mut catalog = Catalog;
    let set = catalog
        .set("sample/features", "set:sample:01")
        .expect("the sample holds this set");
    let ids: Vec<&str> = set
        .members
        .iter()
        .map(|member| member.id.as_str())
        .collect();
    assert_eq!(
        ids,
        [
            "movie:sample:0001",
            "movie:sample:0002",
            "movie:sample:0003"
        ]
    );
    assert_eq!(set.members.len() as i64, PER_SET);
    assert!(set.title.starts_with("The Specimen Cycle"));
}

#[test]
fn a_set_the_sample_never_invented_is_empty() {
    assert_eq!(Catalog.set("sample/features", "set:sample:77"), None);
}

#[test]
fn no_art_answers_none() {
    assert_eq!(
        NoArt.poster("sample/features", "posters/x.jpg", 10, 15),
        None
    );
}
