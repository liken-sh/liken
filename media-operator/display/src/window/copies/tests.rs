use std::path::{Path, PathBuf};
use std::sync::Arc;

use serde_json::{Value, json};
use tokio::sync::broadcast;

use super::super::tests::{display, property, sent};
use super::super::{Display, Message};
use super::Copied;
use crate::subtitles::copy::source::Why;
use crate::subtitles::copy::{Copy, Dialogue};

const PATH: &str = "/media/0/a-film.mkv";

/// A directory of its own for one test's copies, removed when the test
/// ends.
struct Scratch(PathBuf);

impl Drop for Scratch {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}

/// The display of the shared fixture, which writes its copies in a
/// directory named for the test.
fn display_in(test: &str) -> (Display, broadcast::Receiver<String>, Scratch) {
    let dir = std::env::temp_dir().join(format!("media-display-{}-{test}", std::process::id()));
    std::fs::create_dir_all(&dir).expect("creating the directory of the copies");
    let (mut display, receiver) = display();
    display.copies.dir = dir.clone();
    display.copies.inline = true;
    (display, receiver, Scratch(dir))
}

/// The files in the directory of the copies.
fn files(dir: &Path) -> Vec<String> {
    let mut names: Vec<String> = std::fs::read_dir(dir)
        .map(|entries| {
            entries
                .filter_map(|entry| Some(entry.ok()?.file_name().to_string_lossy().into_owned()))
                .collect()
        })
        .unwrap_or_default();
    names.sort();
    names
}

/// A 1920x800 film on a 1920x1080 screen, with two SubRip tracks.
fn film(display: &mut Display, selected: i64) {
    let _ = display.update(property("path", json!(PATH)));
    let _ = display.update(property("demuxer-start-time", json!(0.0)));
    let _ = display.update(property(
        "osd-dimensions",
        json!({ "w": 1920, "h": 1080, "mt": 140, "mb": 140, "ml": 0, "mr": 0 }),
    ));
    tracks(display, selected, &[]);
}

/// Sends `track-list` with the two SubRip tracks and `copies`, each a copy's
/// id, script, and whether mpv draws it. `selected` is the id of the
/// original that mpv draws, or 0.
fn tracks(display: &mut Display, selected: i64, copies: &[(i64, &str, bool)]) {
    let mut list = vec![
        json!({ "id": 1, "type": "sub", "codec": "subrip", "ff-index": 2, "lang": "eng", "title": "English", "selected": selected == 1 }),
        json!({ "id": 2, "type": "sub", "codec": "subrip", "ff-index": 3, "lang": "fra", "selected": selected == 2 }),
    ];
    for (id, script, drawn) in copies {
        list.push(
            json!({ "id": id, "type": "sub", "codec": "ass", "ff-index": 0, "external": true,
            "external-filename": script, "selected": drawn }),
        );
    }
    let _ = display.update(property("track-list", Value::Array(list)));
}

fn copy() -> Copy {
    Copy::converted(vec![
        Dialogue::text(500, 2000, "One line".to_string()),
        Dialogue::text(2500, 4000, "The first\\Nand the second".to_string()),
    ])
}

fn copied(display: &mut Display, read: u64, result: Result<Copy, Why>) {
    let _ = display.update(Message::Copied(Copied {
        read,
        result: result.map(Arc::new),
    }));
}

/// The commands that the display sent to add, select, or remove a track,
/// as their words. The placing properties and the observation of the
/// script's header are tested in `window::subtitles`.
fn commands(lines: Vec<String>) -> Vec<Value> {
    lines
        .iter()
        .filter_map(|line| serde_json::from_str::<Value>(line).ok())
        .map(|sent| sent["command"].clone())
        .filter(|words| words[0] == "sub-add" || words[0] == "sub-remove" || words[1] == "sid")
        .collect()
}

/// The file of the one `sub-add` in `sent`.
fn added(sent: &[Value]) -> String {
    let adds: Vec<&Value> = sent.iter().filter(|words| words[0] == "sub-add").collect();
    assert_eq!(
        adds.len(),
        1,
        "the commands {sent:?} have no single sub-add"
    );
    adds[0][1].as_str().unwrap_or_default().to_string()
}

fn script(file: &str) -> String {
    std::fs::read_to_string(file).expect("reading the copy")
}

/// The copy goes to mpv as a file beside mpv's socket, selected, with the
/// original's title and language. The file's name names the original. Its
/// cue of two lines has a margin of its own, and its cue of one line keeps
/// the style's margin.
#[test]
fn a_ready_copy_is_added_and_selected() {
    let (mut display, mut receiver, dir) = display_in("added");
    film(&mut display, 1);
    sent(&mut receiver);
    copied(&mut display, 1, Ok(copy()));
    let sent = commands(sent(&mut receiver));
    let file = added(&sent);
    assert_eq!(
        file,
        dir.0
            .join("media-display-copy.ff-index-2.1.ass")
            .display()
            .to_string()
    );
    let script = script(&file);
    let add = sent
        .iter()
        .find(|words| words[0] == "sub-add")
        .cloned()
        .unwrap_or_default();
    assert_eq!(
        [&add[2], &add[3], &add[4]],
        [&json!("select"), &json!("English"), &json!("eng")]
    );
    assert!(script.starts_with("[Script Info]\n"));
    assert!(script.contains("Dialogue: 0,0:00:00.50,0:00:02.00,Default,,0,0,0,,One line\n"));
    assert!(script.contains(",Default,,0,0,6,,The first\\Nand the second\n"));
}

/// While mpv draws the copy, the track panel shows the original once, as
/// selected, and the display sends nothing more. When a person selects the
/// original again, the display selects the copy.
#[test]
fn the_copy_stands_in_for_its_original() {
    let (mut display, mut receiver, _dir) = display_in("stands-in");
    film(&mut display, 1);
    copied(&mut display, 1, Ok(copy()));
    let script = added(&commands(sent(&mut receiver)));

    tracks(&mut display, 0, &[(3, &script, true)]);
    assert_eq!(commands(sent(&mut receiver)), Vec::<Value>::new());
    let listed: Vec<(i64, bool)> = display
        .film
        .tracks_of("sub")
        .map(|track| (track.id, track.selected))
        .collect();
    assert_eq!(listed, [(1, true), (2, false)]);

    tracks(&mut display, 1, &[(3, &script, false)]);
    assert_eq!(
        commands(sent(&mut receiver)),
        [json!(["set_property", "sid", 3])]
    );
}

/// mpv draws the copy of a SubRip track as it draws the original, at
/// `sub-pos` 100, with the house style forced on the copy's header.
#[test]
fn a_converted_copy_is_drawn_in_the_house_style_at_the_original_place() {
    let (mut display, mut receiver, _dir) = display_in("converted");
    film(&mut display, 1);
    copied(&mut display, 1, Ok(copy()));
    let script = added(&commands(sent(&mut receiver)));
    tracks(&mut display, 0, &[(3, &script, true)]);
    let placing: Vec<Value> = sent(&mut receiver)
        .iter()
        .filter_map(|line| serde_json::from_str::<Value>(line).ok())
        .map(|sent| sent["command"].clone())
        .filter(|words| words[0] == "set_property" && words[1] != "sid")
        .collect();
    assert_eq!(
        placing,
        [json!(["set_property", "sub-ass-override", "force"])]
    );
}

/// A new screen gives the cues new margins, so the display adds a new copy.
/// Once mpv draws the new one, the display removes the old one and its
/// file.
#[test]
fn a_new_screen_replaces_the_copy() {
    let (mut display, mut receiver, dir) = display_in("screen");
    film(&mut display, 1);
    copied(&mut display, 1, Ok(copy()));
    let old = added(&commands(sent(&mut receiver)));
    tracks(&mut display, 0, &[(3, &old, true)]);

    let _ = display.update(property(
        "osd-dimensions",
        json!({ "w": 1920, "h": 1080, "mt": 104, "mb": 104, "ml": 0, "mr": 0 }),
    ));
    let new = added(&commands(sent(&mut receiver)));
    assert!(script(&new).contains(",Default,,0,0,37,,The first\\Nand the second\n"));
    assert_eq!(
        files(&dir.0),
        [
            "media-display-copy.ff-index-2.1.ass",
            "media-display-copy.ff-index-2.2.ass"
        ]
    );

    tracks(&mut display, 0, &[(3, &old, false), (4, &new, true)]);
    assert_eq!(commands(sent(&mut receiver)), [json!(["sub-remove", 3])]);
    assert_eq!(files(&dir.0), ["media-display-copy.ff-index-2.2.ass"]);
}

/// The copies are removed when another track is selected and when mpv
/// exits.
#[test]
fn the_files_go_when_the_track_changes_and_when_mpv_exits() {
    let (mut display, mut receiver, dir) = display_in("files");
    film(&mut display, 1);
    copied(&mut display, 1, Ok(copy()));
    let first = added(&commands(sent(&mut receiver)));
    tracks(&mut display, 0, &[(3, &first, true)]);
    tracks(&mut display, 2, &[(3, &first, false)]);
    assert_eq!(files(&dir.0), Vec::<String>::new());

    copied(&mut display, 2, Ok(copy()));
    assert_eq!(files(&dir.0), ["media-display-copy.ff-index-3.2.ass"]);
    let _ = display.update(Message::Mpv(crate::ipc::Event::Detached));
    assert_eq!(files(&dir.0), Vec::<String>::new());
}

/// A copy that cannot be written leaves the original with its placement.
#[test]
fn a_copy_that_cannot_be_written_keeps_the_original() {
    let (mut display, mut receiver, dir) = display_in("unwritable");
    display.copies.dir = dir.0.join("missing");
    film(&mut display, 1);
    copied(&mut display, 1, Ok(copy()));
    assert_eq!(commands(sent(&mut receiver)), Vec::<Value>::new());
    assert!(
        display
            .logged
            .iter()
            .any(|line| line.contains("its copy could not be written"))
    );
}

/// Selecting another track starts a read for it. The result of the read for
/// the earlier track arrives late and changes nothing.
#[test]
fn a_read_for_a_track_no_longer_selected_is_dropped() {
    let (mut display, mut receiver, _dir) = display_in("dropped");
    film(&mut display, 1);
    tracks(&mut display, 2, &[]);
    sent(&mut receiver);
    copied(&mut display, 1, Ok(copy()));
    assert_eq!(commands(sent(&mut receiver)), Vec::<Value>::new());
    copied(&mut display, 2, Ok(copy()));
    assert!(added(&commands(sent(&mut receiver))).ends_with("media-display-copy.ff-index-3.1.ass"));
}

/// A track that cannot be copied keeps the placement of every cue, the log
/// says why once, and the file is not read again.
#[test]
fn a_track_with_no_index_keeps_its_placement() {
    let (mut display, mut receiver, _dir) = display_in("no-index");
    film(&mut display, 1);
    copied(&mut display, 1, Err(Why::NoIndex));
    tracks(&mut display, 1, &[]);
    assert!(
        commands(sent(&mut receiver))
            .iter()
            .all(|words| words[0] != "sub-add")
    );
    let lines: Vec<&String> = display
        .logged
        .iter()
        .filter(|line| line.contains("keeps the placement"))
        .collect();
    assert_eq!(
        lines,
        [
            "media-display: subtitle track ff-index-2 keeps the placement of every cue on the house row, because the file's index lists no cue of the track"
        ]
    );
    assert_eq!(display.copies.reads, 1);
}

/// mpv can drop the copy, for example when it opens the file again. When
/// `track-list` goes on without the copy, the display adds it again.
#[test]
fn a_copy_that_mpv_dropped_is_added_again() {
    let (mut display, mut receiver, _dir) = display_in("dropped-by-mpv");
    film(&mut display, 1);
    copied(&mut display, 1, Ok(copy()));
    let file = added(&commands(sent(&mut receiver)));
    tracks(&mut display, 1, &[]);
    tracks(&mut display, 1, &[]);
    assert_eq!(added(&commands(sent(&mut receiver))), file);
}

/// A file that starts late gets a copy too. The read moves the cues of an
/// embedded track back by the start time.
#[test]
fn a_file_that_starts_late_gets_a_copy() {
    let (mut display, mut receiver, _dir) = display_in("late");
    let _ = display.update(property("demuxer-start-time", json!(1.4)));
    let _ = display.update(property("path", json!(PATH)));
    let _ = display.update(property(
        "osd-dimensions",
        json!({ "w": 1920, "h": 1080, "mt": 140, "mb": 140, "ml": 0, "mr": 0 }),
    ));
    tracks(&mut display, 1, &[]);
    copied(&mut display, 1, Ok(copy()));
    added(&commands(sent(&mut receiver)));
}

/// mpv can list one copy twice, after the display added it again. The
/// display keeps the copy that mpv draws and removes the other.
#[test]
fn a_copy_listed_twice_is_removed_once() {
    let (mut display, mut receiver, _dir) = display_in("twice");
    film(&mut display, 1);
    copied(&mut display, 1, Ok(copy()));
    let file = added(&commands(sent(&mut receiver)));
    tracks(&mut display, 0, &[(3, &file, false), (4, &file, true)]);
    assert_eq!(commands(sent(&mut receiver)), [json!(["sub-remove", 3])]);
}

/// The margins need the screen, so no read starts before
/// `osd-dimensions` arrives.
#[test]
fn no_copy_is_read_before_the_screen_is_known() {
    let (mut display, _receiver, _dir) = display_in("no-screen");
    let _ = display.update(property("demuxer-start-time", json!(0.0)));
    let _ = display.update(property("path", json!(PATH)));
    tracks(&mut display, 1, &[]);
    assert_eq!(display.copies.reads, 0);
}

/// A tick of a fade does not build the script or write the file again
/// while the screen is the same.
#[test]
fn the_script_is_built_once_for_each_screen() {
    let (mut display, mut receiver, dir) = display_in("cached");
    film(&mut display, 1);
    copied(&mut display, 1, Ok(copy()));
    let file = added(&commands(sent(&mut receiver)));
    tracks(&mut display, 0, &[(3, &file, true)]);
    let built = display
        .copies
        .script
        .as_ref()
        .map(|(_, script)| Arc::as_ptr(script));
    for _ in 0..5 {
        let _ = display.update(Message::Tick);
    }
    assert_eq!(
        display
            .copies
            .script
            .as_ref()
            .map(|(_, script)| Arc::as_ptr(script)),
        built
    );
    assert_eq!(files(&dir.0), ["media-display-copy.ff-index-2.1.ass"]);
}

/// A 1920x1080 frame of a 2.39:1 film on a 1920x1080 screen. mpv reports no
/// bar, and the item's block gives the frame's active area, so a cue of two
/// lines gets the margin of the black rows below the area. Before the
/// block arrives, the cue keeps the style's margin.
#[test]
fn a_cue_of_two_lines_takes_the_margin_of_the_black_rows_of_the_frame() {
    let (mut display, mut receiver, _dir) = display_in("encoded");
    let _ = display.update(property("path", json!(PATH)));
    let _ = display.update(property("demuxer-start-time", json!(0.0)));
    let _ = display.update(property(
        "osd-dimensions",
        json!({ "w": 1920, "h": 1080, "mt": 0, "mb": 0, "ml": 0, "mr": 0 }),
    ));
    let _ = display.update(property(
        "track-list",
        json!([
            { "id": 1, "type": "video", "ff-index": 0, "selected": true },
            { "id": 1, "type": "sub", "codec": "subrip", "ff-index": 2, "lang": "eng", "selected": true },
        ]),
    ));
    copied(&mut display, 1, Ok(copy()));
    let first = script(&added(&commands(sent(&mut receiver))));
    assert!(first.contains(",Default,,0,0,0,,The first\\Nand the second\n"));

    let _ = display.update(Message::Mpv(crate::ipc::Event::Message(vec![
        "presentation".to_string(),
        r#"{"video":[{"stream":0,"width":1920,"height":1080,
            "activeArea":{"left":0,"top":138,"width":1920,"height":804}}]}"#
            .to_string(),
    ])));
    let second = script(&added(&commands(sent(&mut receiver))));
    assert!(second.contains(",Default,,0,0,4,,The first\\Nand the second\n"));
    assert!(second.contains("Dialogue: 0,0:00:00.50,0:00:02.00,Default,,0,0,0,,One line\n"));
}
