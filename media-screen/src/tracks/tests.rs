// The roles, the file names, and the languages of the subtitle rule against
// the cases that media-operator's tests read. The file is media-operator's,
// and media-screen's package.toml names it in the Rust job's reads, so a
// change to it runs this test too. The media browser's tests read the
// cases of the choice from the same file.

use serde_json::Value;

use super::subtitle::{self, Track};
use crate::language;

const CASES: &str = include_str!("../../../media-operator/testdata/subtitle-rule.json");

fn cases() -> Value {
    serde_json::from_str(CASES).expect("the shared cases parse")
}

fn text(value: &Value, key: &str) -> String {
    value[key].as_str().unwrap_or_default().to_string()
}

fn strings(value: &Value) -> Vec<String> {
    value
        .as_array()
        .map(|items| {
            items
                .iter()
                .map(|item| item.as_str().unwrap_or_default().to_string())
                .collect()
        })
        .unwrap_or_default()
}

// One track of the cases, as a screen builds it: an external file has its
// base name, and mpv gives it no title of its own, so it takes the part of
// its name after the video's name.
fn track(value: &Value) -> Track {
    let flags = strings(&value["flags"]);
    let file = value["file"].as_str().map(str::to_string);
    let title = match &file {
        Some(name) => name
            .strip_prefix("Movie (2024).")
            .unwrap_or(name)
            .to_string(),
        None => text(value, "title"),
    };
    Track {
        lang: text(value, "lang"),
        title,
        default: flags.iter().any(|flag| flag == "default"),
        forced: flags.iter().any(|flag| flag == "forced"),
        hearing_impaired: flags.iter().any(|flag| flag == "hi"),
        file,
        events: value["events"].as_i64(),
        codec: text(value, "codec"),
    }
}

// A case's own tracks, or the survey file it names.
fn tracks(all: &Value, case: &Value) -> Vec<Track> {
    let list = match case["file"].as_str() {
        Some(name) => &all["files"][name],
        None => &case["tracks"],
    };
    list.as_array()
        .expect("a case names its tracks")
        .iter()
        .map(track)
        .collect()
}

fn each(section: &str) -> Vec<Value> {
    let all = cases();
    let list = all[section].as_array().cloned().unwrap_or_default();
    assert!(!list.is_empty(), "the shared cases have no {section}");
    list
}

#[test]
fn every_track_takes_the_role_media_operator_gives_it() {
    let all = cases();
    for case in each("classify") {
        let roles: Vec<&str> = subtitle::classify(&tracks(&all, &case))
            .iter()
            .map(|one| one.role.word())
            .collect();
        assert_eq!(roles, strings(&case["roles"]), "{}", text(&case, "name"));
    }
}

#[test]
fn a_file_name_says_what_media_operator_reads_from_it() {
    for case in each("fileNames") {
        let parsed = subtitle::file_name(&text(&case, "name"));
        assert_eq!(
            (parsed.language, parsed.forced, parsed.sdh),
            (
                text(&case, "language"),
                case["forced"].as_bool().unwrap_or_default(),
                case["sdh"].as_bool().unwrap_or_default()
            ),
            "{}",
            text(&case, "name")
        );
    }
}

#[test]
fn two_tags_name_the_same_language_where_media_operator_says_so() {
    for case in each("sameLanguage") {
        assert_eq!(
            language::same(&text(&case, "a"), &text(&case, "b")),
            case["same"].as_bool().unwrap_or_default(),
            "{} and {}",
            text(&case, "a"),
            text(&case, "b")
        );
    }
}
