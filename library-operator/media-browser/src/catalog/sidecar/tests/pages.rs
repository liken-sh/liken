// The reads behind a movie's page: the body's fields, the art by role,
// and the set the movie belongs to.

use super::*;

#[test]
fn a_title_carries_the_duration_and_the_rating_its_row_holds() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_page(&path, "default/films", "one", "1994", "", BODY);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let slots = source.wall(&library("default/films")).slots;
    assert_eq!(slots[0].duration, 6720);
    assert_eq!(slots[0].rating, "PG");
}

#[test]
fn a_title_whose_body_names_no_rating_carries_none() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_movie(&path, "default/films", "one", "Film one", "film one");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let slots = source.wall(&library("default/films")).slots;
    assert_eq!(slots[0].rating, "");
    assert_eq!(slots[0].duration, 0);
}

#[test]
fn a_movie_page_reads_its_body_and_its_art_by_role() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_page(&path, "default/films", "one", "1994-05-02", "set:one", BODY);
    insert_file(
        &path,
        "default/films",
        "Film one/fanart.jpg",
        "one",
        "image",
        "backdrop",
    );
    insert_file(
        &path,
        "default/films",
        "Film one/clearlogo.png",
        "one",
        "image",
        "logo",
    );
    insert_file(
        &path,
        "default/films",
        "Film one/trailer.mkv",
        "one",
        "video",
        "trailer",
    );
    insert_file(
        &path,
        "default/films",
        "Film one/poster.jpg",
        "one",
        "image",
        "poster",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let details = source
        .movie("default/films", "one")
        .expect("the library holds this movie");
    assert_eq!(
        details,
        MovieDetails {
            title: "Film one".into(),
            released: "1994-05-02".into(),
            duration: 6720,
            rating: "PG".into(),
            genres: vec!["Drama".into(), "Mystery".into()],
            tagline: "One line.".into(),
            plot: "A plot.".into(),
            directors: vec!["A Director".into()],
            writers: vec!["A Writer".into(), "Another".into()],
            cast: vec![Credit {
                name: "A Player".into(),
                role: "The Part".into(),
            }],
            studios: vec!["A Studio".into()],
            ratings: vec![
                ("imdb".into(), 6.5),
                ("metacritic".into(), 80.0),
                ("themoviedb".into(), 7.1),
                ("tomatometerallcritics".into(), 83.0),
            ],
            set_id: "set:one".into(),
            backdrop: "Film one/fanart.jpg".into(),
            logo: "Film one/clearlogo.png".into(),
            trailer: "Film one/trailer.mkv".into(),
        }
    );
}

#[test]
fn a_movie_with_an_empty_body_reads_as_a_page_of_its_columns() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_page(&path, "default/films", "one", "1994", "", "{}");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let details = source
        .movie("default/films", "one")
        .expect("the library holds this movie");
    assert_eq!(details.title, "Film one");
    assert!(details.plot.is_empty());
    assert!(details.genres.is_empty());
    assert!(details.cast.is_empty());
    assert!(details.studios.is_empty());
    assert!(details.ratings.is_empty());
    assert!(details.backdrop.is_empty());
    assert!(details.logo.is_empty());
    assert!(details.trailer.is_empty());
}

#[test]
fn a_title_reads_the_files_it_holds_with_their_technical_columns() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_page(&path, "default/films", "one", "1994", "", BODY);
    insert_video_file(&path, "default/films", "Film one/film.mkv", "one", "");
    insert_file(
        &path,
        "default/films",
        "Film one/film.en.srt",
        "one",
        "subtitle",
        "subtitle",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let files = source.files("default/films", "one");
    assert_eq!(
        files,
        [
            FileFacts {
                role: "subtitle".into(),
                kind: "subtitle".into(),
                ..FileFacts::default()
            },
            FileFacts {
                role: "primary".into(),
                kind: "video".into(),
                container: "mkv".into(),
                video_codec: "x265".into(),
                audio_codec: "AC3".into(),
                width: 1_920,
                height: 804,
                size_bytes: 4_200_000_000,
                language: String::new(),
            },
        ]
    );
}

#[test]
fn a_title_another_library_holds_reads_none_of_its_files_here() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_page(&path, "default/films", "one", "1994", "", BODY);
    insert_video_file(&path, "default/films", "Film one/film.mkv", "one", "");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(source.files("default/other", "one").is_empty());
    assert!(source.files("default/films", "gone").is_empty());
}

#[test]
fn a_movie_another_library_holds_has_no_page_here() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_page(&path, "default/films", "one", "1994", "", BODY);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert_eq!(source.movie("default/other", "one"), None);
    assert_eq!(source.movie("default/films", "gone"), None);
}

#[test]
fn a_set_comes_back_named_with_its_members_in_release_order() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_set(&path, "default/films", "set:one", "The Set");
    insert_page(&path, "default/films", "later", "1999", "set:one", BODY);
    insert_page(&path, "default/films", "first", "1994", "set:one", BODY);
    insert_page(&path, "default/films", "apart", "1990", "", BODY);
    insert_page(&path, "default/other", "elsewhere", "1991", "set:one", BODY);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let set = source
        .set("default/films", "set:one")
        .expect("the library holds this set");
    assert_eq!(set.title, "The Set");
    let ids: Vec<&str> = set
        .members
        .iter()
        .map(|member| member.id.as_str())
        .collect();
    assert_eq!(ids, ["first", "later"]);
    assert_eq!(set.members[0].rating, "PG");
}

#[test]
fn a_set_no_row_names_is_nothing() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_page(&path, "default/films", "one", "1994", "set:gone", BODY);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert_eq!(source.set("default/films", "set:gone"), None);
}

#[test]
fn a_set_with_no_member_left_comes_back_empty() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_set(&path, "default/films", "set:one", "The Set");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let set = source
        .set("default/films", "set:one")
        .expect("the library holds this set");
    assert!(set.members.is_empty());
}
