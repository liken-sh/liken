// The play lists a choice resolves to: a movie's main file, a movie's
// trailer, and an episode with the rest of its season.

use super::*;

#[test]
fn a_movie_plays_its_primary_video_file() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );
    insert_main_file(
        &path,
        "default/films",
        "The Matrix/The Matrix.mkv",
        "movie:tmdb:603",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert_eq!(
        source.play("default/films", &movie_chosen()),
        vec![PlayItem {
            path: "The Matrix/The Matrix.mkv".into(),
            slug: "matrix-1999".into(),
            presentation: Presentation {
                kind: "video".into(),
                hint: "movie".into(),
                title: "The Matrix".into(),
                year: 1999,
                art: "movie:tmdb:603.jpg".into(),
                trickplay: "The Matrix/The Matrix.mkv.trickplay".into(),
                ..Presentation::default()
            },
        }]
    );
}

#[test]
fn a_movie_plays_neither_its_art_nor_its_extras() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );
    insert_file(
        &path,
        "default/films",
        "The Matrix/poster.jpg",
        "movie:tmdb:603",
        "image",
        "primary",
    );
    insert_file(
        &path,
        "default/films",
        "The Matrix/behind.mkv",
        "movie:tmdb:603",
        "video",
        "extra",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(source.play("default/films", &movie_chosen()).is_empty());
}

#[test]
fn a_movie_with_two_encodings_plays_one_of_them() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );
    insert_main_file(&path, "default/films", "The Matrix/b.mkv", "movie:tmdb:603");
    insert_main_file(&path, "default/films", "The Matrix/a.mkv", "movie:tmdb:603");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let items = source.play("default/films", &movie_chosen());
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].path, "The Matrix/a.mkv");
    assert_eq!(
        items[0].presentation.trickplay,
        "The Matrix/a.mkv.trickplay"
    );
}

#[test]
fn an_episode_plays_itself_and_the_rest_of_its_season() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_season(&path);
    insert_episode(&path, "default/shows", "episode:tvdb:next", SERIES, 2, 1);
    insert_main_file(
        &path,
        "default/shows",
        "Lost/S02E1.mkv",
        "episode:tvdb:next",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let items = source.play("default/shows", &episode_chosen(2));
    let paths: Vec<&str> = items.iter().map(|item| item.path.as_str()).collect();
    assert_eq!(paths, ["Lost/S01E2.mkv", "Lost/S01E3.mkv"]);
}

// The operator names a Play after the chosen item, so every item
// carries the slug its own row holds.
#[test]
fn every_item_carries_the_slug_its_row_holds() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_season(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let items = source.play("default/shows", &episode_chosen(2));
    let slugs: Vec<&str> = items.iter().map(|item| item.slug.as_str()).collect();
    assert_eq!(slugs, ["s01e02", "s01e03"]);
}

#[test]
fn an_episodes_presentation_names_its_series_and_its_numbers() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_season(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let items = source.play("default/shows", &episode_chosen(3));
    assert_eq!(
        items[0].presentation,
        Presentation {
            kind: "video".into(),
            hint: "series".into(),
            series: "Lost".into(),
            season: 1,
            episode: 3,
            episode_title: "Episode 3".into(),
            art: "episode:tvdb:3.jpg".into(),
            trickplay: "Lost/S01E3.mkv.trickplay".into(),
            ..Presentation::default()
        }
    );
}

#[test]
fn an_episode_the_catalog_dates_carries_the_date_and_not_the_year() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_released_episode(&path, "default/shows", "e1", SERIES, 1, 1, "2004-09-22");
    insert_main_file(&path, "default/shows", "Lost/S01E1.mkv", "e1");
    insert_released_episode(&path, "default/shows", "e2", SERIES, 1, 2, "2004");
    insert_main_file(&path, "default/shows", "Lost/S01E2.mkv", "e2");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let items = source.play("default/shows", &episode_chosen(1));
    assert_eq!(items[0].presentation.date, "2004-09-22");
    assert_eq!(items[0].presentation.year, 0);
    assert_eq!(items[1].presentation.date, "");
    assert_eq!(items[1].presentation.year, 2004);
}

#[test]
fn an_episode_with_no_still_presents_the_art_of_its_series() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_season(&path);
    set_series_art(
        &path,
        "default/shows",
        SERIES,
        "Lost/poster.jpg",
        &["Lost/poster.jpg", "Lost/fanart.jpg"],
    );
    clear_episode_art(&path, "default/shows", "episode:tvdb:1");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let items = source.play("default/shows", &episode_chosen(1));
    assert_eq!(items[0].presentation.art, "Lost/fanart.jpg");
    assert_eq!(items[1].presentation.art, "episode:tvdb:2.jpg");
}

#[test]
fn an_episode_under_no_series_row_names_no_series() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_episode(&path, "default/shows", "e1", SERIES, 1, 1);
    insert_main_file(&path, "default/shows", "Lost/S01E1.mkv", "e1");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let items = source.play("default/shows", &episode_chosen(1));
    assert_eq!(items[0].presentation.series, "");
    assert_eq!(items[0].presentation.year, 0);
}

#[test]
fn an_episode_with_no_file_of_its_own_plays_nothing() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_series(&path, "default/shows", SERIES, "Lost", "lost");
    insert_episode(&path, "default/shows", "e1", SERIES, 1, 1);
    insert_episode(&path, "default/shows", "e2", SERIES, 1, 2);
    insert_main_file(&path, "default/shows", "Lost/S01E2.mkv", "e2");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(source.play("default/shows", &episode_chosen(1)).is_empty());
}

#[test]
fn a_choice_in_another_library_plays_nothing() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_season(&path);
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );
    insert_main_file(
        &path,
        "default/films",
        "The Matrix/The Matrix.mkv",
        "movie:tmdb:603",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(source.play("default/other", &movie_chosen()).is_empty());
    assert!(source.play("default/other", &episode_chosen(1)).is_empty());
}

#[test]
fn a_trailer_plays_the_trailer_file_and_carries_no_trickplay() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_page(&path, "default/films", "one", "1994", "", BODY);
    insert_main_file(&path, "default/films", "Film one/Film one.mkv", "one");
    insert_file(
        &path,
        "default/films",
        "Film one/trailer.mkv",
        "one",
        "video",
        "trailer",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert_eq!(
        source.play("default/films", &Selection::Trailer { id: "one".into() }),
        vec![PlayItem {
            path: "Film one/trailer.mkv".into(),
            slug: "film-one".into(),
            presentation: Presentation {
                kind: "video".into(),
                hint: "movie".into(),
                title: "Film one".into(),
                year: 1994,
                art: "one.jpg".into(),
                ..Presentation::default()
            },
        }]
    );
}

#[test]
fn a_movie_with_no_trailer_file_plays_nothing() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_page(&path, "default/films", "one", "1994", "", BODY);
    insert_main_file(&path, "default/films", "Film one/Film one.mkv", "one");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(
        source
            .play("default/films", &Selection::Trailer { id: "one".into() })
            .is_empty()
    );
}
