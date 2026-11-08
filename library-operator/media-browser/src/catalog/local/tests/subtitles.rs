// The subtitle tracks a play item carries: each embedded subtitle stream of
// the main file by its index in the container, and each text subtitle file
// beside it that mpv loads, both with the event count the probe read.

use super::*;
use crate::catalog::{Subtitle, Track};

const FILM: &str = "The Lantern/The Lantern.mkv";

// One stream of the main file, as the walk writes it off the probe ledger.
// An absent count is a null in the table.
fn insert_stream(path: &Path, ordinal: i64, kind: &str, events: Option<i64>) {
    Connection::open(path)
        .unwrap()
        .execute(
            "INSERT INTO streams (library, path, ordinal, kind, events) \
             VALUES ('default/films', ?, ?, ?, ?)",
            (FILM, ordinal, kind, events),
        )
        .unwrap();
}

// One subtitle file in the library, as the walk writes it.
fn insert_subtitle_file(path: &Path, file: &str, events: Option<i64>) {
    Connection::open(path)
        .unwrap()
        .execute(
            "INSERT INTO files (library, path, type, role, events) \
             VALUES ('default/films', ?, 'subtitle', 'subtitle', ?)",
            (file, events),
        )
        .unwrap();
}

fn lantern(dir: &TempDir) -> PathBuf {
    let path = fixture(dir);
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:7001",
        "The Lantern",
        "lantern",
    );
    insert_main_file(&path, "default/films", FILM, "movie:tmdb:7001");
    path
}

fn stream(index: i64, events: Option<i64>) -> Subtitle {
    Subtitle {
        track: Track::Stream(index),
        events,
    }
}

fn file(name: &str, events: Option<i64>) -> Subtitle {
    Subtitle {
        track: Track::File(name.into()),
        events,
    }
}

// The index of a subtitle stream counts every stream before it, a video
// and an audio track as well, because that is the index mpv names the
// track by. A count of zero stays zero, and no count stays absent.
#[test]
fn a_movie_carries_each_subtitle_stream_by_its_index_in_the_container() {
    let dir = TempDir::new().unwrap();
    let path = lantern(&dir);
    insert_stream(&path, 0, "video", None);
    insert_stream(&path, 1, "audio", None);
    insert_stream(&path, 2, "subtitle", Some(22));
    insert_stream(&path, 3, "subtitle", Some(0));
    insert_stream(&path, 4, "subtitle", None);

    let mut source = LocalCatalog::new(&path, NO_AGENT);
    assert_eq!(
        source.play("default/films", &movie_chosen())[0]
            .presentation
            .subtitles,
        vec![stream(2, Some(22)), stream(3, Some(0)), stream(4, None)]
    );
}

// mpv's default `--sub-auto=exact` loads a file named as the video, or as
// the video and a language tag, from the video's own folder. A file whose
// name ends in a number has no language tag, so mpv loads none of them.
#[test]
fn a_movie_carries_the_text_subtitle_files_mpv_loads_beside_it() {
    let dir = TempDir::new().unwrap();
    let path = lantern(&dir);
    for (name, events) in [
        ("The Lantern/The Lantern.srt", Some(980)),
        ("The Lantern/The Lantern.en.hi.srt", Some(1054)),
        ("The Lantern/the lantern.eng.forced.srt", Some(12)),
        ("The Lantern/The Lantern.pt-BR.ass", None),
        ("The Lantern/The Lantern.eng.0.srt", Some(1000)),
        ("The Lantern/The Lantern Extended.en.srt", Some(1100)),
        ("The Lantern/The Lantern.en.sup", None),
        ("The Lantern/Subs/The Lantern.en.srt", Some(990)),
        ("The Lanterns/The Lantern.en.srt", Some(991)),
    ] {
        insert_subtitle_file(&path, name, events);
    }

    let mut source = LocalCatalog::new(&path, NO_AGENT);
    assert_eq!(
        source.play("default/films", &movie_chosen())[0]
            .presentation
            .subtitles,
        vec![
            file("The Lantern.en.hi.srt", Some(1054)),
            file("The Lantern.pt-BR.ass", None),
            file("The Lantern.srt", Some(980)),
            file("the lantern.eng.forced.srt", Some(12)),
        ]
    );
}

// An episode's item carries its own file's tracks, and not those of the
// next episode in the same folder.
#[test]
fn an_episode_carries_the_subtitle_tracks_of_its_own_file() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_season(&path);
    let connection = Connection::open(&path).unwrap();
    for (file, events) in [("Harrow/S01E2.en.srt", 411), ("Harrow/S01E3.en.srt", 412)] {
        connection
            .execute(
                "INSERT INTO files (library, path, type, role, events) \
                 VALUES ('default/shows', ?, 'subtitle', 'subtitle', ?)",
                (file, events),
            )
            .unwrap();
    }
    connection
        .execute(
            "INSERT INTO streams (library, path, ordinal, kind, events) \
             VALUES ('default/shows', 'Harrow/S01E2.mkv', 2, 'subtitle', 300)",
            (),
        )
        .unwrap();

    let mut source = LocalCatalog::new(&path, NO_AGENT);
    assert_eq!(
        source.play("default/shows", &episode_chosen(2))[0]
            .presentation
            .subtitles,
        vec![stream(2, Some(300)), file("S01E2.en.srt", Some(411))]
    );
}
