use serde_json::json;

use super::*;

/// The film of the capture rig: four audio tracks, four embedded
/// subtitle tracks, and an SDH file beside the video. The block has the
/// facts of the first audio track and the event counts of the English
/// subtitle tracks.
fn film() -> Film {
    let mut film = Film::default();
    film.apply(
        "track-list",
        &json!([
            { "id": 1, "type": "video", "selected": true },
            { "id": 1, "type": "audio", "lang": "eng", "codec": "eac3", "ff-index": 1,
              "demux-channel-count": 6, "title": "[English / DD+ 5.1 Atmos]", "selected": true },
            { "id": 2, "type": "audio", "lang": "eng", "codec": "aac", "ff-index": 2,
              "demux-channel-count": 2, "title": "Commentary with the director" },
            { "id": 3, "type": "audio", "lang": "fre", "codec": "flac", "ff-index": 3,
              "demux-channel-count": 8 },
            { "id": 4, "type": "audio", "lang": "eng", "codec": "ac3", "ff-index": 4,
              "demux-channel-count": 1 },
            { "id": 1, "type": "sub", "lang": "eng", "codec": "subrip", "ff-index": 5,
              "forced": true, "selected": true },
            { "id": 2, "type": "sub", "lang": "eng", "codec": "subrip", "ff-index": 6 },
            { "id": 3, "type": "sub", "lang": "eng", "codec": "subrip", "ff-index": 7,
              "hearing-impaired": true },
            { "id": 4, "type": "sub", "lang": "spa", "codec": "subrip", "ff-index": 8,
              "title": "Español (Latinoamérica)" },
            { "id": 5, "type": "sub", "lang": "en", "codec": "subrip", "ff-index": 0,
              "external": true, "external-filename": "/media/a-film.en.hi.srt",
              "title": "en.hi.srt", "hearing-impaired": true },
        ]),
    );
    film
}

fn streams() -> Streams {
    Streams::parse(
        Some(&json!([
            { "stream": 5, "events": 14 },
            { "stream": 6, "events": 620 },
            { "stream": 7, "events": 700 },
        ])),
        Some(&json!([
            { "stream": 1, "codec": "eac3", "profile": "Dolby Digital Plus + Dolby Atmos",
              "channels": 6, "layout": "5.1(side)" },
        ])),
    )
}

/// Each row as its id and the four parts the panel draws.
fn read(rows: &[Row]) -> Vec<(Option<i64>, &str, &str, &str, &str)> {
    rows.iter()
        .map(|row| {
            (
                row.id,
                row.language.as_str(),
                row.role,
                row.format.as_str(),
                row.title.as_str(),
            )
        })
        .collect()
}

/// The audio that plays is English, so the English tracks come first and
/// the commentary last among them. A title that repeats the language and
/// the format is left out, and a title with a word of its own is kept,
/// without the role word that it starts with. Only the first track has an entry in the block, so the others take
/// their format from mpv's codec and channel count.
#[test]
fn the_audio_rows_list_the_language_that_plays_first() {
    assert_eq!(
        read(&rows(Track::Audio, &film(), &streams())),
        vec![
            (Some(1), "English", "", "E-AC3 Atmos 5.1", ""),
            (Some(4), "English", "", "AC3 1.0", ""),
            (Some(2), "English", "commentary", "AAC 2.0", "The director"),
            (Some(3), "French", "", "FLAC 7.1", ""),
        ]
    );
}

/// The subtitle list starts with Off. The English tracks come first,
/// because the forced English track plays, and within them the full track,
/// the SDH tracks in the file's order, and the forced track. The forced
/// track has 14 events against 620, and the file beside the video is SDH
/// from its name.
#[test]
fn the_subtitle_rows_list_the_language_that_shows_first_by_role() {
    assert_eq!(
        read(&rows(Track::Subtitles, &film(), &streams())),
        vec![
            (None, "Off", "", "", ""),
            (Some(2), "English", "", "SRT", ""),
            (Some(3), "English", "SDH", "SRT", ""),
            (Some(5), "English", "SDH", "SRT file", ""),
            (Some(1), "English", "forced", "SRT", ""),
            (Some(4), "Spanish", "", "SRT", "Español (Latinoamérica)"),
        ]
    );
}

/// With the subtitles off, the tracks in the language of the audio come
/// first, and the other languages follow by their English names.
#[test]
fn with_subtitles_off_the_language_of_the_audio_comes_first() {
    let mut film = Film::default();
    film.apply(
        "track-list",
        &json!([
            { "id": 1, "type": "audio", "lang": "ger", "selected": true },
            { "id": 1, "type": "sub", "lang": "spa" },
            { "id": 2, "type": "sub", "lang": "fre" },
            { "id": 3, "type": "sub" },
            { "id": 4, "type": "sub", "lang": "deu" },
        ]),
    );
    let rows = rows(Track::Subtitles, &film, &Streams::default());
    let languages: Vec<&str> = rows.iter().map(|row| row.language.as_str()).collect();
    assert_eq!(
        languages,
        vec!["Off", "German", "French", "Spanish", "Track 3"]
    );
    assert!(rows[0].playing, "the Off row does not play, want it to");
}

/// A track with no events shows nothing, so it is left out unless it
/// plays.
#[test]
fn an_empty_track_is_listed_only_while_it_plays() {
    let block = Streams::parse(
        Some(&json!([{ "stream": 1, "events": 0 }, { "stream": 2, "events": 0 }])),
        None,
    );
    let mut film = Film::default();
    film.apply(
        "track-list",
        &json!([
            { "id": 1, "type": "sub", "lang": "eng", "ff-index": 1 },
            { "id": 2, "type": "sub", "lang": "eng", "ff-index": 2, "selected": true },
        ]),
    );
    let ids: Vec<Option<i64>> = rows(Track::Subtitles, &film, &block)
        .iter()
        .map(|row| row.id)
        .collect();
    assert_eq!(ids, vec![None, Some(2)]);
}

/// A track with no language shows its title, and with no title, its
/// number. A described track takes its role from the flag or the title.
#[test]
fn a_track_with_no_language_shows_its_title_or_its_number() {
    let mut film = Film::default();
    film.apply(
        "track-list",
        &json!([
            { "id": 1, "type": "audio", "title": "Director's cut mix", "selected": true },
            { "id": 2, "type": "audio" },
            { "id": 3, "type": "audio", "lang": "eng", "visual-impaired": true },
            { "id": 4, "type": "audio", "lang": "eng", "title": "Audio Description" },
        ]),
    );
    assert_eq!(
        read(&rows(Track::Audio, &film, &Streams::default())),
        vec![
            (Some(3), "English", "described", "", ""),
            (Some(4), "English", "described", "", ""),
            (Some(1), "Director's cut mix", "", "", ""),
            (Some(2), "Track 2", "", "", ""),
        ]
    );
}

/// When two rows read the same, each shows its track number.
#[test]
fn two_rows_that_read_the_same_show_their_numbers() {
    let mut film = Film::default();
    film.apply(
        "track-list",
        &json!([
            { "id": 1, "type": "sub", "lang": "eng", "codec": "subrip" },
            { "id": 2, "type": "sub", "lang": "eng", "codec": "subrip" },
            { "id": 3, "type": "sub", "lang": "eng", "codec": "ass" },
        ]),
    );
    let numbers: Vec<Option<i64>> = rows(Track::Subtitles, &film, &Streams::default())
        .iter()
        .map(|row| row.number)
        .collect();
    assert_eq!(numbers, vec![None, Some(1), Some(2), None]);
}

/// A title is kept when it has a word that the language, the role, and
/// the format do not say.
#[test]
fn a_title_shows_only_when_it_adds_a_word() {
    let said = ["English", "eng", "en", "SDH", "E-AC3 Atmos 5.1"];
    let cases = [
        ("[English / DD+ 5.1 Atmos]", false),
        ("English (SDH) Full Subtitles Track", false),
        ("English", false),
        ("Commentary with the director", true),
        ("Canadien", true),
        ("Theatrical", true),
    ];
    for (title, adds) in cases {
        assert_eq!(adds_a_word(title, &said), adds, "{title:?}");
    }
}

/// The strip takes the first of these texts that fits.
#[test]
fn the_strip_values_run_from_the_longest_to_the_code() {
    let audio = rows(Track::Audio, &film(), &streams());
    let subtitles = rows(Track::Subtitles, &film(), &streams());
    let cases = [
        (
            Track::Audio,
            &audio[0],
            vec!["English E-AC3 Atmos 5.1", "English 5.1", "English", "ENG"],
        ),
        (
            Track::Audio,
            &audio[2],
            vec![
                "English commentary AAC 2.0",
                "English commentary",
                "English",
                "ENG",
            ],
        ),
        (
            Track::Subtitles,
            &subtitles[2],
            vec!["English SDH", "English", "ENG"],
        ),
        (Track::Subtitles, &subtitles[1], vec!["English", "ENG"]),
        (Track::Subtitles, &subtitles[0], vec!["Off"]),
    ];
    for (kind, row, values) in cases {
        let texts: Vec<String> = row.values(kind).iter().map(Value::text).collect();
        assert_eq!(texts, values, "{:?}", row.id);
    }
}

#[test]
fn a_video_row_is_its_title_or_its_number() {
    let mut film = Film::default();
    film.apply(
        "track-list",
        &json!([
            { "id": 1, "type": "video", "title": "Main", "selected": true },
            { "id": 2, "type": "video" },
        ]),
    );
    let rows = rows(Track::Video, &film, &Streams::default());
    assert_eq!(rows[0].values(Track::Video)[0].text(), "Main");
    assert_eq!(rows[1].values(Track::Video)[0].text(), "Track 2");
    assert_eq!(playing(&rows).and_then(|row| row.id), Some(1));
}

/// The role is a part of its own, so the strip draws it in italics, and a
/// detail is the format or the channels in smaller type.
#[test]
fn a_strip_value_keeps_its_role_and_its_detail_apart() {
    let audio = rows(Track::Audio, &film(), &streams());
    let subtitles = rows(Track::Subtitles, &film(), &streams());
    assert_eq!(
        audio[2].values(Track::Audio)[0],
        Value {
            name: "English".to_string(),
            role: "commentary",
            detail: "AAC 2.0".to_string(),
        }
    );
    assert_eq!(
        subtitles[2].values(Track::Subtitles)[0],
        Value {
            name: "English".to_string(),
            role: "SDH",
            detail: String::new(),
        }
    );
}

/// The languages above or below a window of rows, each once, with the one
/// that plays marked.
#[test]
fn the_hidden_rows_read_as_their_languages() {
    let rows = rows(Track::Subtitles, &film(), &streams());
    assert_eq!(
        languages(&rows),
        vec![
            ("Off".to_string(), false),
            ("English".to_string(), true),
            ("Spanish".to_string(), false),
        ]
    );
    assert_eq!(
        languages(&rows[3..]),
        vec![
            ("English".to_string(), true),
            ("Spanish".to_string(), false),
        ]
    );
}

/// A language is written on the first row of its group, and again on the
/// first row that shows when the list scrolls into the middle of a group.
/// The Off row names no language.
#[test]
fn a_language_is_written_once_for_its_group() {
    let rows = rows(Track::Subtitles, &film(), &streams());
    let named = |first: usize| -> Vec<bool> {
        (first..rows.len())
            .map(|at| names_its_language(&rows, at, first))
            .collect()
    };
    assert_eq!(named(0), vec![false, true, false, false, false, true]);
    assert_eq!(named(3), vec![true, false, true]);
}

/// The format is written once for a group only when every track of the
/// language has it. A track with no language is not a group.
#[test]
fn a_group_writes_its_format_once_only_when_every_track_shares_it() {
    let subtitles = rows(Track::Subtitles, &film(), &streams());
    let shared: Vec<bool> = (0..subtitles.len())
        .map(|at| one_format(&subtitles, at))
        .collect();
    assert_eq!(shared, vec![false, false, false, false, false, true]);

    let mut film = Film::default();
    film.apply(
        "track-list",
        &json!([
            { "id": 1, "type": "sub", "lang": "fre", "codec": "hdmv_pgs_subtitle", "selected": true },
            { "id": 2, "type": "sub", "lang": "fre", "codec": "hdmv_pgs_subtitle", "forced": true },
            { "id": 3, "type": "sub", "title": "Signs" },
        ]),
    );
    let rows = rows(Track::Subtitles, &film, &Streams::default());
    let shared: Vec<bool> = (0..rows.len()).map(|at| one_format(&rows, at)).collect();
    assert_eq!(shared, vec![false, true, true, false]);
    assert!(
        !rows[3].has_language,
        "a track with only a title has a language, want none"
    );
}

/// A title that starts with the role word says the role twice beside the
/// italic role, so the row drops the word, and a "by" or "with" after it,
/// and starts what remains with a capital letter.
#[test]
fn a_title_drops_the_role_word_it_starts_with() {
    let cases = [
        (
            "Commentary by Ann Example and Bob Sample",
            "Ann Example and Bob Sample",
        ),
        ("Commentary by novelist Ann Example", "Novelist Ann Example"),
        ("Commentary with the director", "The director"),
        ("Commentary: Ann Example", "Ann Example"),
        ("Commentary - Ann Example", "Ann Example"),
        ("Commentary Ann Example", "Ann Example"),
        ("Commentary", ""),
        ("Director's commentary", "Director's commentary"),
    ];
    for (title, want) in cases {
        let mut film = Film::default();
        film.apply(
            "track-list",
            &json!([{ "id": 1, "type": "audio", "lang": "eng", "title": title, "selected": true }]),
        );
        let rows = rows(Track::Audio, &film, &Streams::default());
        assert_eq!(rows[0].role, "commentary", "{title:?}");
        assert_eq!(rows[0].title, want, "{title:?}");
    }
}

/// The codes for several languages, for no language, and for a language
/// with no code name no one language, so a track tagged with one reads as
/// a track with no language: its title, or its number.
#[test]
fn a_track_tagged_with_no_one_language_reads_as_having_none() {
    let mut film = Film::default();
    film.apply(
        "track-list",
        &json!([
            { "id": 1, "type": "audio", "lang": "eng", "selected": true },
            { "id": 2, "type": "audio", "lang": "zxx", "title": "Score only" },
            { "id": 3, "type": "audio", "lang": "mul" },
            { "id": 4, "type": "audio", "lang": "fil" },
        ]),
    );
    let rows = rows(Track::Audio, &film, &Streams::default());
    let read: Vec<(&str, bool)> = rows
        .iter()
        .map(|row| (row.language.as_str(), row.has_language))
        .collect();
    assert_eq!(
        read,
        vec![
            ("English", true),
            ("Filipino", true),
            ("Score only", false),
            ("Track 3", false)
        ]
    );
}
