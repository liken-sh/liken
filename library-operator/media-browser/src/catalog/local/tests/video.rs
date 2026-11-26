// The video streams a play item carries: each video stream of the main file
// by its index in the container, with its frame and the active area the
// probe measured.

use super::*;
use crate::catalog::{ActiveArea, Video};

const FILM: &str = "The Lantern/The Lantern.mkv";

// One video stream of the main file, as the walk writes it off the probe
// ledger.
fn insert_video(path: &Path, stream: &Video) {
    let area = stream.active_area;
    Connection::open(path)
        .unwrap()
        .execute(
            "INSERT INTO streams (library, path, ordinal, kind, codec, width, height, \
                                  active_left, active_top, active_width, active_height) \
             VALUES ('default/films', ?, ?, 'video', 'hevc', ?, ?, ?, ?, ?, ?)",
            (
                FILM,
                stream.stream,
                stream.width,
                stream.height,
                area.map(|area| area.left),
                area.map(|area| area.top),
                area.map(|area| area.width),
                area.map(|area| area.height),
            ),
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

// A stream with an area carries it, and a stream whose samples did not agree
// carries its frame alone. An audio stream carries no entry.
#[test]
fn a_movie_carries_the_active_area_of_each_video_stream_by_its_index_in_the_container() {
    let dir = TempDir::new().unwrap();
    let path = lantern(&dir);
    let scope = Video {
        stream: 0,
        width: 1920,
        height: 1080,
        active_area: Some(ActiveArea {
            left: 0,
            top: 131,
            width: 1920,
            height: 817,
        }),
    };
    let unagreed = Video {
        stream: 2,
        width: 1920,
        height: 1080,
        active_area: None,
    };
    insert_video(&path, &scope);
    Connection::open(&path)
        .unwrap()
        .execute(
            "INSERT INTO streams (library, path, ordinal, kind, codec) \
             VALUES ('default/films', ?, 1, 'audio', 'eac3')",
            [FILM],
        )
        .unwrap();
    insert_video(&path, &unagreed);

    let mut source = LocalCatalog::new(&path, NO_AGENT);
    assert_eq!(
        source.play("default/films", &movie_chosen())[0]
            .presentation
            .video,
        vec![scope, unagreed]
    );
}

// A file the probe has not read has no stream rows, so its item carries no
// video entry.
#[test]
fn a_file_the_probe_has_not_read_carries_no_video_streams() {
    let dir = TempDir::new().unwrap();
    let path = lantern(&dir);

    let mut source = LocalCatalog::new(&path, NO_AGENT);
    assert_eq!(
        source.play("default/films", &movie_chosen())[0]
            .presentation
            .video,
        Vec::<Video>::new()
    );
}
