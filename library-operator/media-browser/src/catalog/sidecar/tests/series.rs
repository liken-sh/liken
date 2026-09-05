// The reads behind a series' page: the body's fields, the count of its
// seasons, the art by role, and the plot each episode carries.

use super::*;

// The body a series page reads every field out of.
const SERIES_BODY: &str = r#"{"plot":"A plot.","tagline":"One line.","contentRating":"TV-14",
    "genres":["Drama","Mystery"],"creators":["A Creator"],"studios":["A Studio"],
    "ratings":{"imdb":8.3,"tomatometerallcritics":95},
    "cast":[{"name":"A Player","role":"The Part"}]}"#;

#[test]
fn a_series_file_of_a_role_the_page_does_not_draw_is_left_out() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_series(&path, "default/shows", "series:path:one", "One", "one");
    insert_file(
        &path,
        "default/shows",
        "one/trailer.mkv",
        "series:path:one",
        "video",
        "trailer",
    );
    let mut source = SidecarSource::new(&path, NO_AGENT);

    let page = source
        .series("default/shows", "series:path:one")
        .expect("the series is there");
    assert_eq!(page.backdrop, "");
    assert_eq!(page.logo, "");
}

#[test]
fn a_series_page_reads_its_body_its_seasons_and_its_art_by_role() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_series_page(&path, "default/shows", "one", "2004", SERIES_BODY);
    for (season, episode) in [(1, 1), (1, 2), (2, 1)] {
        insert_episode_page(
            &path,
            "default/shows",
            &format!("episode:{season}:{episode}"),
            "one",
            (season, episode),
            "2004-09-22",
            "{}",
        );
    }
    insert_file(
        &path,
        "default/shows",
        "Serial one/fanart.jpg",
        "one",
        "image",
        "backdrop",
    );
    insert_file(
        &path,
        "default/shows",
        "Serial one/clearlogo.png",
        "one",
        "image",
        "logo",
    );
    insert_file(
        &path,
        "default/shows",
        "Serial one/poster.jpg",
        "one",
        "image",
        "poster",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let details = source
        .series("default/shows", "one")
        .expect("the library holds this series");
    assert_eq!(
        details,
        SeriesDetails {
            title: "Serial one".into(),
            released: "2004".into(),
            duration: 0,
            rating: "TV-14".into(),
            genres: vec!["Drama".into(), "Mystery".into()],
            tagline: "One line.".into(),
            plot: "A plot.".into(),
            creators: vec!["A Creator".into()],
            cast: vec![Credit {
                name: "A Player".into(),
                role: "The Part".into(),
            }],
            studios: vec!["A Studio".into()],
            ratings: vec![("imdb".into(), 8.3), ("tomatometerallcritics".into(), 95.0)],
            backdrop: "Serial one/fanart.jpg".into(),
            logo: "Serial one/clearlogo.png".into(),
            seasons: 2,
        }
    );
}

#[test]
fn a_series_with_an_empty_body_reads_as_a_page_of_its_columns() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_series_page(&path, "default/shows", "one", "2004", "{}");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let details = source
        .series("default/shows", "one")
        .expect("the library holds this series");
    assert_eq!(details.title, "Serial one");
    assert!(details.plot.is_empty());
    assert!(details.cast.is_empty());
    assert!(details.creators.is_empty());
    assert!(details.studios.is_empty());
    assert!(details.ratings.is_empty());
    assert!(details.backdrop.is_empty());
    assert!(details.logo.is_empty());
    assert_eq!(details.seasons, 0);
}

#[test]
fn a_series_another_library_holds_has_no_page_here() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_series_page(&path, "default/shows", "one", "2004", SERIES_BODY);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert_eq!(source.series("default/other", "one"), None);
    assert_eq!(source.series("default/shows", "gone"), None);
}

#[test]
fn every_episode_carries_its_plot_its_runtime_and_its_still() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_series_page(&path, "default/shows", "one", "2004", SERIES_BODY);
    insert_episode_page(
        &path,
        "default/shows",
        "episode:1:1",
        "one",
        (1, 1),
        "2004-09-22",
        r#"{"plot":"The episode's plot."}"#,
    );
    insert_episode_page(
        &path,
        "default/shows",
        "episode:1:2",
        "one",
        (1, 2),
        "2004-09-29",
        "{}",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let episodes = source.episodes("default/shows", "one");
    assert_eq!(episodes.len(), 2);
    assert_eq!(episodes[0].id, "episode:1:1");
    assert_eq!(episodes[0].title, "Segment 1");
    assert_eq!(episodes[0].released, "2004-09-22");
    assert_eq!(episodes[0].duration, 2760);
    assert_eq!(episodes[0].plot, "The episode's plot.");
    assert_eq!(episodes[0].art, "episode:1:1.jpg");
    assert!(episodes[1].plot.is_empty());
}
