// The audio streams a play item carries: each audio stream of the main file
// by its index in the container, with the format facts the probe read.

use super::*;
use crate::catalog::Audio;

const FILM: &str = "The Lantern/The Lantern.mkv";

// One audio stream of the main file, as the walk writes it off the probe
// ledger.
fn insert_audio(path: &Path, stream: &Audio) {
    Connection::open(path)
        .unwrap()
        .execute(
            "INSERT INTO streams (library, path, ordinal, kind, codec, profile, channels, layout) \
             VALUES ('default/films', ?, ?, 'audio', ?, ?, ?, ?)",
            (
                FILM,
                stream.stream,
                &stream.codec,
                &stream.profile,
                stream.channels,
                &stream.layout,
            ),
        )
        .unwrap();
}

// One stream of another kind, with only its index and its codec.
fn insert_other(path: &Path, ordinal: i64, kind: &str, codec: &str) {
    Connection::open(path)
        .unwrap()
        .execute(
            "INSERT INTO streams (library, path, ordinal, kind, codec) \
             VALUES ('default/films', ?, ?, ?, ?)",
            (FILM, ordinal, kind, codec),
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

// The index of an audio stream counts every stream before it, the video as
// well, because that is the index mpv names the track by. A stream of any
// other kind carries no entry.
#[test]
fn a_movie_carries_the_format_of_each_audio_stream_by_its_index_in_the_container() {
    let dir = TempDir::new().unwrap();
    let path = lantern(&dir);
    let atmos = Audio {
        stream: 1,
        codec: "eac3".into(),
        profile: "Dolby Digital Plus + Dolby Atmos".into(),
        channels: 6,
        layout: "5.1(side)".into(),
    };
    let stereo = Audio {
        stream: 3,
        codec: "aac".into(),
        profile: "LC".into(),
        channels: 2,
        layout: "stereo".into(),
    };
    insert_other(&path, 0, "video", "hevc");
    insert_audio(&path, &atmos);
    insert_other(&path, 2, "subtitle", "subrip");
    insert_audio(&path, &stereo);

    let mut source = LocalCatalog::new(&path, NO_AGENT);
    assert_eq!(
        source.play("default/films", &movie_chosen())[0]
            .presentation
            .audio,
        vec![atmos, stereo]
    );
}

// The probe writes the stream rows of a file when it reads the file, so a
// file it has not read has none, and its item carries no audio entry.
#[test]
fn a_file_the_probe_has_not_read_carries_no_audio_streams() {
    let dir = TempDir::new().unwrap();
    let path = lantern(&dir);

    let mut source = LocalCatalog::new(&path, NO_AGENT);
    assert_eq!(
        source.play("default/films", &movie_chosen())[0]
            .presentation
            .audio,
        Vec::<Audio>::new()
    );
}
