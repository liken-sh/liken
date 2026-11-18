//! Log lines for the key presses that send mpv a command. Each line has
//! the key's navigation word, a description of the command, the command
//! as sent, and mpv's reply.
//!
//! [`Record::press`] sends each command with its own request id and
//! stores the start of its line in [`Notes`] under that id. The socket
//! reader completes the line and writes it when mpv replies with that id.
//!
//! A held key repeats its navigation word, and each repeat sends its
//! command to mpv. A line for each repeat would flood the log, so
//! [`Record`] counts the repeats and writes one line for the hold when it
//! ends: at the next press, at a summon, or when the display hides.

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};

use serde_json::Value;

use crate::ipc::{self, Command};

/// The log lines that wait for mpv's reply, by request id. The frame
/// loop inserts them and the socket reader removes them, so the map is
/// behind a lock.
pub type Notes = Arc<Mutex<BTreeMap<u64, String>>>;

/// The first request id of a press's command. The `observe_property`
/// registrations and the `GATE` reads in `ipc` use small ids, and their
/// replies write no log line, so press ids start above them.
pub const FIRST_ID: u64 = 1000;

/// The second word of the navigation message that the command sidecar
/// sends for each repeat of a held key.
pub const REPEAT: &str = "repeat";

/// The lines to send to mpv's socket for one press or repeat, and the
/// log line of the previous hold when this press ended it.
#[derive(Debug, Default, PartialEq)]
pub struct Pressed {
    pub lines: Vec<String>,
    pub ended: Option<String>,
}

#[derive(Debug)]
struct Hold {
    word: String,
    repeats: u32,
    last: Option<Command>,
}

#[derive(Debug)]
pub struct Record {
    next: u64,
    hold: Option<Hold>,
}

impl Default for Record {
    fn default() -> Self {
        Self {
            next: FIRST_ID,
            hold: None,
        }
    }
}

impl Record {
    /// Turn one press's commands into lines for mpv's socket. A press
    /// stores a note for each command that [`describe`] describes. A
    /// repeat of the held key stores none and counts toward the hold.
    pub fn press(
        &mut self,
        word: &str,
        repeat: bool,
        commands: Vec<Command>,
        notes: &Notes,
    ) -> Pressed {
        let mut pressed = Pressed::default();
        if repeat && self.hold.as_ref().is_some_and(|hold| hold.word == word) {
            let hold = self
                .hold
                .as_mut()
                .expect("a hold exists for this word, checked above");
            hold.repeats += 1;
            if let Some(last) = commands.last() {
                hold.last = Some(last.clone());
            }
            pressed.lines = commands.iter().map(ipc::line).collect();
            return pressed;
        }
        pressed.ended = self.settle();
        self.hold = Some(Hold {
            word: word.to_string(),
            repeats: u32::from(repeat),
            last: commands.last().filter(|_| repeat).cloned(),
        });
        for command in &commands {
            let described = describe(command).filter(|_| !repeat);
            let Some(what) = described else {
                pressed.lines.push(ipc::line(command));
                continue;
            };
            let id = self.next;
            self.next += 1;
            notes.lock().expect("the notes lock is poisoned").insert(
                id,
                format!(
                    "media-display: {word} pressed: {what}, sent {} to mpv",
                    words(command)
                ),
            );
            pressed.lines.push(ipc::command(id, command.clone()));
        }
        pressed
    }

    /// End the hold in progress. Returns its log line, or `None` when no
    /// repeat sent mpv a command, as for a hold that only moved the focus.
    pub fn settle(&mut self) -> Option<String> {
        let hold = self.hold.take()?;
        let last = hold.last?;
        Some(format!(
            "media-display: {} held for {} {}, last sent {} to mpv",
            hold.word,
            hold.repeats,
            if hold.repeats == 1 {
                "repeat"
            } else {
                "repeats"
            },
            words(&last)
        ))
    }
}

/// Complete the stored line for the id of mpv's reply. Returns `None`
/// when no press stored a line under that id.
pub fn answered(notes: &Notes, id: u64, error: &str) -> Option<String> {
    let note = notes
        .lock()
        .expect("the notes lock is poisoned")
        .remove(&id)?;
    Some(format!("{note}, mpv answered {error}"))
}

/// Complete every waiting line, in the order of their ids, when the
/// socket closes and mpv can no longer reply.
pub fn abandoned(notes: &Notes) -> Vec<String> {
    std::mem::take(&mut *notes.lock().expect("the notes lock is poisoned"))
        .into_values()
        .map(|note| format!("{note}, mpv closed its socket before it answered"))
        .collect()
}

fn words(command: &Command) -> String {
    Value::Array(command.clone()).to_string()
}

/// What a command does, for the log line. Returns `None` for a
/// `script-message`, because the command sidecar receives it and logs it.
pub fn describe(command: &Command) -> Option<String> {
    let text = |index: usize| -> String {
        match command.get(index) {
            Some(Value::String(word)) => word.clone(),
            Some(other) => other.to_string(),
            None => String::new(),
        }
    };
    let number = |index: usize| command.get(index).and_then(Value::as_f64);
    let words: Vec<String> = (0..command.len()).map(text).collect();
    let words: Vec<&str> = words.iter().map(String::as_str).collect();
    match words.as_slice() {
        ["script-message", ..] => None,
        [.., "cycle", "pause"] => Some("pause or resume".into()),
        [.., "set", "pause", "yes"] => Some("pause".into()),
        ["seek", ..] => Some(format!("seek to {}", clock(number(1).unwrap_or(0.0)))),
        ["add", "chapter", _] => Some(format!("chapter {:+}", number(2).unwrap_or(0.0))),
        ["playlist-prev"] => Some("the previous item".into()),
        ["playlist-next"] => Some("the next item".into()),
        ["set_property", "sid", "no"] => Some("subtitles off".into()),
        ["set_property", "sid", id] => Some(format!("subtitle track {id}")),
        ["set_property", "aid", id] => Some(format!("audio track {id}")),
        ["set_property", "vid", id] => Some(format!("video track {id}")),
        ["set_property", "sub-delay", _] => {
            Some(format!("subtitle offset {:+} s", number(2).unwrap_or(0.0)))
        }
        ["set_property", "audio-delay", _] => {
            Some(format!("audio offset {:+} s", number(2).unwrap_or(0.0)))
        }
        [name, ..] => Some((*name).to_string()),
        [] => None,
    }
}

/// A position in seconds as H:MM:SS, rounded down.
fn clock(seconds: f64) -> String {
    let whole = seconds.max(0.0).floor() as i64;
    format!(
        "{}:{:02}:{:02}",
        whole / 3600,
        whole % 3600 / 60,
        whole % 60
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn notes() -> Notes {
        Notes::default()
    }

    #[test]
    fn each_command_is_described_in_plain_words() {
        let cases: Vec<(Command, Option<&str>)> = vec![
            (
                vec![json!("no-osd"), json!("cycle"), json!("pause")],
                Some("pause or resume"),
            ),
            (
                vec![json!("no-osd"), json!("set"), json!("pause"), json!("yes")],
                Some("pause"),
            ),
            (
                vec![json!("seek"), json!(1205.5), json!("absolute+exact")],
                Some("seek to 0:20:05"),
            ),
            (
                vec![json!("add"), json!("chapter"), json!(-1)],
                Some("chapter -1"),
            ),
            (vec![json!("playlist-prev")], Some("the previous item")),
            (vec![json!("playlist-next")], Some("the next item")),
            (
                vec![json!("set_property"), json!("sid"), json!("no")],
                Some("subtitles off"),
            ),
            (
                vec![json!("set_property"), json!("sid"), json!("2")],
                Some("subtitle track 2"),
            ),
            (
                vec![json!("set_property"), json!("aid"), json!("2")],
                Some("audio track 2"),
            ),
            (
                vec![json!("set_property"), json!("vid"), json!(1)],
                Some("video track 1"),
            ),
            (
                vec![json!("set_property"), json!("sub-delay"), json!(0.3)],
                Some("subtitle offset +0.3 s"),
            ),
            (
                vec![json!("set_property"), json!("audio-delay"), json!(-0.05)],
                Some("audio offset -0.05 s"),
            ),
            (vec![json!("script-message"), json!("liken-exit")], None),
            (vec![json!("script-message"), json!("liken-next")], None),
            (vec![json!("frame-step")], Some("frame-step")),
            (Vec::new(), None),
        ];
        for (command, want) in cases {
            assert_eq!(describe(&command).as_deref(), want, "{command:?}");
        }
    }

    /// A press writes its line when mpv replies.
    #[test]
    fn a_press_line_waits_for_mpvs_answer() {
        let notes = notes();
        let mut record = Record::default();

        let pressed = record.press(
            "select",
            false,
            vec![vec![json!("seek"), json!(107.0), json!("absolute+exact")]],
            &notes,
        );

        assert_eq!(
            pressed.lines,
            vec![
                "{\"command\":[\"seek\",107.0,\"absolute+exact\"],\"request_id\":1000}\n"
                    .to_string()
            ]
        );
        assert_eq!(pressed.ended, None);
        assert_eq!(
            answered(&notes, 1000, "success").as_deref(),
            Some(
                "media-display: select pressed: seek to 0:01:47, sent [\"seek\",107.0,\"absolute+exact\"] to mpv, mpv answered success"
            )
        );
        assert_eq!(answered(&notes, 1000, "success"), None);
    }

    #[test]
    fn an_mpv_error_appears_in_the_line_unchanged() {
        let notes = notes();
        let mut record = Record::default();
        record.press(
            "select",
            false,
            vec![vec![json!("set_property"), json!("aid"), json!("9")]],
            &notes,
        );

        assert_eq!(
            answered(&notes, 1000, "property unavailable").as_deref(),
            Some(
                "media-display: select pressed: audio track 9, sent [\"set_property\",\"aid\",\"9\"] to mpv, mpv answered property unavailable"
            )
        );
    }

    /// A reply with an id that no press stored finishes no line.
    #[test]
    fn an_answer_to_the_displays_own_command_writes_nothing() {
        assert_eq!(answered(&notes(), 0, "success"), None);
    }

    /// A `script-message` and a press that sends no command store no note.
    #[test]
    fn a_command_with_no_description_stores_no_note() {
        let notes = notes();
        let mut record = Record::default();

        let pressed = record.press(
            "back",
            false,
            vec![vec![json!("script-message"), json!("liken-exit")]],
            &notes,
        );
        record.press("up", false, Vec::new(), &notes);

        assert_eq!(
            pressed.lines,
            vec![
                "{\"command\":[\"script-message\",\"liken-exit\"],\"request_id\":0}\n".to_string()
            ]
        );
        assert!(
            notes.lock().expect("the notes lock is poisoned").is_empty(),
            "a note was recorded, want none"
        );
    }

    /// A hold writes one line for all of its repeats, when the next
    /// press arrives or the caller settles it.
    #[test]
    fn a_hold_writes_one_line_for_its_repeats() {
        let notes = notes();
        let mut record = Record::default();
        let nudge = |to: f64| vec![vec![json!("set_property"), json!("sub-delay"), json!(to)]];

        record.press("right", false, nudge(0.1), &notes);
        for step in 2..=4 {
            let pressed = record.press("right", true, nudge(f64::from(step) / 10.0), &notes);
            assert_eq!(pressed.ended, None);
            assert!(
                pressed.lines[0].ends_with("\"request_id\":0}\n"),
                "the line is {:?}, want it to end with request_id 0",
                pressed.lines[0]
            );
        }
        let pressed = record.press("select", false, Vec::new(), &notes);

        assert_eq!(
            pressed.ended.as_deref(),
            Some(
                "media-display: right held for 3 repeats, last sent [\"set_property\",\"sub-delay\",0.4] to mpv"
            )
        );
        assert_eq!(notes.lock().expect("the notes lock is poisoned").len(), 1);
        assert_eq!(record.settle(), None);
    }

    /// A repeat with no earlier press starts its own hold, and one repeat
    /// reads in the singular.
    #[test]
    fn a_repeat_with_no_press_starts_its_own_hold() {
        let notes = notes();
        let mut record = Record::default();
        let step = vec![vec![json!("add"), json!("chapter"), json!(1)]];

        record.press("right", true, step, &notes);

        assert_eq!(
            record.settle().as_deref(),
            Some(
                "media-display: right held for 1 repeat, last sent [\"add\",\"chapter\",1] to mpv"
            )
        );
        assert!(
            notes.lock().expect("the notes lock is poisoned").is_empty(),
            "a note was recorded, want none"
        );
    }

    #[test]
    fn a_hold_that_sent_nothing_ends_quietly() {
        let notes = notes();
        let mut record = Record::default();
        record.press("down", false, Vec::new(), &notes);
        record.press("down", true, Vec::new(), &notes);

        assert_eq!(record.settle(), None);
    }

    /// The lines still waiting when the socket closes are written in the
    /// order they were sent, and `Notes` is empty after.
    #[test]
    fn a_closed_socket_writes_every_waiting_line() {
        let notes = notes();
        let mut record = Record::default();
        record.press(
            "select",
            false,
            vec![vec![json!("no-osd"), json!("cycle"), json!("pause")]],
            &notes,
        );
        record.press(
            "right",
            false,
            vec![vec![json!("add"), json!("chapter"), json!(1)]],
            &notes,
        );

        assert_eq!(
            abandoned(&notes),
            vec![
                "media-display: select pressed: pause or resume, sent [\"no-osd\",\"cycle\",\"pause\"] to mpv, mpv closed its socket before it answered",
                "media-display: right pressed: chapter +1, sent [\"add\",\"chapter\",1] to mpv, mpv closed its socket before it answered",
            ]
        );
        assert!(
            abandoned(&notes).is_empty(),
            "a note was abandoned, want none"
        );
    }
}
