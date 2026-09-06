// The headings each query draws, the order each wall cycles through,
// and what "see all" opens from each.

use super::*;

#[test]
fn a_library_heading_carries_the_name_half_and_the_count() {
    let query = Query::Library {
        library: "screening/features".into(),
        sort: Sort::default(),
    };
    assert_eq!(query.heading("features", counts(42, 0, 0)), "features · 42");
}

// The counts of one wall, without walking a whole answer for them.
fn counts(items: usize, movies: usize, series: usize) -> Counts {
    Counts {
        items,
        movies,
        series,
    }
}

#[test]
fn a_franchise_is_headed_by_its_name_alone() {
    let query = Query::Franchise {
        library: "screening/franchises".into(),
        id: "franchise:name:the-cycle".into(),
    };
    assert_eq!(query.name("The Cycle"), "The Cycle");
    assert_eq!(query.heading("The Cycle", counts(9, 9, 0)), "The Cycle");
    assert_eq!(query.all_titles(), query);
}

#[test]
fn a_person_and_a_set_are_headed_by_their_name_alone() {
    let person = Query::Person {
        library: "screening/features".into(),
        path: ".contributors/A Player".into(),
    };
    assert_eq!(person.heading("A Player", counts(3, 3, 0)), "A Player");
    let set = Query::Set {
        library: "screening/features".into(),
        id: "set:1".into(),
    };
    assert_eq!(set.heading("The Cycle", counts(3, 3, 0)), "The Cycle");
}

#[test]
fn the_recency_queries_are_headed_by_their_words_and_the_count() {
    let released = Query::Released { fold: Fold::Airing };
    assert_eq!(released.name(""), "Recently released");
    assert_eq!(
        released.heading("", counts(12, 4, 0)),
        "Recently released · 12"
    );
    let added = Query::Added { fold: Fold::Titles };
    assert_eq!(added.name("ignored"), "Recently added");
    assert_eq!(
        added.heading("ignored", counts(3, 3, 0)),
        "Recently added · 3"
    );
}

#[test]
fn a_genre_is_headed_by_its_name_and_the_counts_of_both_kinds() {
    let query = Query::Genre {
        name: "Science Fiction".into(),
        order: Order::Released,
        sort: GenreSort::default(),
    };
    assert_eq!(query.name("ignored"), "Science Fiction");
    assert_eq!(
        query.heading("ignored", counts(499, 429, 70)),
        "Science Fiction · 429 movies, 70 series"
    );
    assert_eq!(query.all_titles(), query);
}

#[test]
fn a_genre_of_one_kind_names_that_kind_alone() {
    let query = Query::Genre {
        name: "Science Fiction".into(),
        order: Order::Released,
        sort: GenreSort::default(),
    };
    let cases = [
        (counts(429, 429, 0), "Science Fiction · 429 movies"),
        (counts(70, 0, 70), "Science Fiction · 70 series"),
        (counts(1, 1, 0), "Science Fiction · 1 movie"),
        (counts(1, 0, 1), "Science Fiction · 1 series"),
        (counts(0, 0, 0), "Science Fiction · 0"),
    ];
    for (counts, want) in cases {
        assert_eq!(query.heading("ignored", counts), want, "{counts:?}");
    }
}

#[test]
fn a_search_is_headed_by_the_text_and_the_count() {
    let query = Query::Search {
        text: "batman".into(),
    };
    assert_eq!(query.name("ignored"), "batman");
    assert_eq!(query.heading("ignored", counts(4, 4, 0)), "batman · 4");
    assert_eq!(query.all_titles(), query);
}

#[test]
fn a_wall_counts_its_items_by_kind() {
    let counted = Counts::of(["movies", "series", "movies", "episodes"]);
    assert_eq!(counted, counts(4, 2, 1));
    assert_eq!(Counts::of([]), Counts::default());
}

#[test]
fn a_library_cycles_title_newest_oldest_and_back() {
    let mut query = Query::Library {
        library: "screening/features".into(),
        sort: Sort::default(),
    };
    let mut words = Vec::new();
    for _ in 0..4 {
        words.push(query.sort_word().expect("a library draws a button"));
        query = query.resorted();
    }
    assert_eq!(words, ["Title", "Newest", "Oldest", "Title"]);
    assert_eq!(
        query,
        Query::Library {
            library: "screening/features".into(),
            sort: Sort::Newest,
        }
    );
}

#[test]
fn a_genre_cycles_leads_newest_oldest_title_and_back() {
    let mut query = Query::Genre {
        name: "Western".into(),
        order: Order::Released,
        sort: GenreSort::default(),
    };
    let mut words = Vec::new();
    for _ in 0..5 {
        words.push(query.sort_word().expect("a genre draws a button"));
        query = query.resorted();
    }
    assert_eq!(words, ["Genre", "Newest", "Oldest", "Title", "Genre"]);
    assert_eq!(
        query,
        Query::Genre {
            name: "Western".into(),
            order: Order::Released,
            sort: GenreSort::By(Sort::Newest),
        }
    );
}

#[test]
fn every_other_query_draws_no_button_and_never_resorts() {
    for query in [
        Query::Person {
            library: "screening/features".into(),
            path: ".contributors/A Player".into(),
        },
        Query::Set {
            library: "screening/features".into(),
            id: "set:1".into(),
        },
        Query::Franchise {
            library: "screening/franchises".into(),
            id: "franchise:name:the-cycle".into(),
        },
        Query::Released { fold: Fold::Titles },
        Query::Added { fold: Fold::Titles },
        Query::Search {
            text: "batman".into(),
        },
    ] {
        assert_eq!(query.sort_word(), None, "{query:?}");
        assert_eq!(query.resorted(), query, "{query:?}");
    }
}

#[test]
fn a_library_names_itself_without_the_count() {
    let query = Query::Library {
        library: "screening/features".into(),
        sort: Sort::default(),
    };
    assert_eq!(query.name(""), "features");
}

#[test]
fn see_all_opens_a_recency_query_with_every_episode_folded() {
    assert_eq!(
        Query::Released { fold: Fold::Airing }.all_titles(),
        Query::Released { fold: Fold::Titles }
    );
    assert_eq!(
        Query::Added {
            fold: Fold::Episodes,
        }
        .all_titles(),
        Query::Added { fold: Fold::Titles }
    );
    let library = Query::Library {
        library: "screening/features".into(),
        sort: Sort::default(),
    };
    assert_eq!(library.all_titles(), library);
}

#[test]
fn an_episode_slot_is_a_still_and_every_other_slot_a_poster() {
    let episode = Slot {
        episode: Some(InSeries {
            series: "series:1".into(),
            name: "The Serial".into(),
            season: 3,
            episode: 4,
        }),
        ..Slot::default()
    };
    assert!(episode.still());
    assert!(!Slot::default().still());
}

#[test]
fn a_slot_whose_id_is_its_series_is_the_whole_show_folded() {
    let mut slot = Slot {
        id: "episode:1".into(),
        episode: Some(InSeries {
            series: "series:1".into(),
            name: "The Serial".into(),
            season: 3,
            episode: 4,
        }),
        ..Slot::default()
    };
    assert!(!slot.folded());
    slot.id = "series:1".into();
    assert!(slot.folded());
    assert!(!Slot::default().folded());
}

#[test]
fn a_slot_of_a_title_carries_its_library_and_kind_and_no_parts() {
    let slot = Slot::of(
        "screening/features",
        "movies",
        Title {
            id: "movie:1".into(),
            title: "Specimen 0001".into(),
            released: "1987".into(),
            art: "1.jpg".into(),
            duration: 5_820,
            rating: "PG-13".into(),
            tagline: "One of a kind.".into(),
        },
    );
    assert_eq!(slot.library, "screening/features");
    assert_eq!(slot.kind, "movies");
    assert_eq!(slot.id, "movie:1");
    assert_eq!(slot.title, "Specimen 0001");
    assert_eq!(slot.duration, 5_820);
    assert_eq!(slot.rating, "PG-13");
    assert_eq!(slot.parts, "");
    assert_eq!(slot.new, 0);
    assert_eq!(slot.seasons, 0);
    assert!(!slot.still());
}
