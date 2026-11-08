// The subtitle rule against the cases media-operator's tests read. The file
// is media-operator's, and library-operator's package.toml names it in the
// Rust job's reads, so a change to it runs this test too.

use serde_json::Value;

use super::preferences::Preferences;
use super::subtitle::{self, Choice, Track};

const CASES: &str = include_str!("../../../../media-operator/testdata/subtitle-rule.json");

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

// One track of the cases, as the browser builds it from the catalog: an
// external file carries its base name, and mpv gives it no title of its
// own, so it takes the part of its name after the video's name.
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

fn preferences(value: &Value) -> Preferences {
    Preferences::parse(&value.to_string())
}

// The mpv id of the chosen track, 0 for none, and -1 where mpv's own
// choice stands, as the cases write it.
fn id(choice: Choice) -> i64 {
    match choice {
        Choice::Track(index) => index as i64 + 1,
        Choice::None => 0,
        Choice::Keep => -1,
    }
}

fn each(section: &str) -> Vec<Value> {
    let all = cases();
    let list = all[section].as_array().cloned().unwrap_or_default();
    assert!(!list.is_empty(), "the shared cases hold no {section}");
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
            crate::language::same(&text(&case, "a"), &text(&case, "b")),
            case["same"].as_bool().unwrap_or_default(),
            "{} and {}",
            text(&case, "a"),
            text(&case, "b")
        );
    }
}

#[test]
fn the_rule_chooses_the_track_media_operator_chooses() {
    let all = cases();
    for case in each("choose") {
        let classified = subtitle::classify(&tracks(&all, &case));
        let choice = subtitle::choose(
            &classified,
            &text(&case, "audio"),
            &preferences(&case["prefs"]),
        );
        assert_eq!(
            id(choice),
            case["want"].as_i64().unwrap_or_default(),
            "{}",
            text(&case, "name")
        );
    }
}
