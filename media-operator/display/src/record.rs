//! The display's record of what a person's presses did. A press that makes
//! the display send mpv a command earns one line: the navigation word, what
//! the command does in a person's words, the command's exact words, and
//! mpv's answer. The frame loop gives each such command a request id and
//! leaves its line in [`Notes`], and the socket reader writes the line when
//! mpv answers under that id. So one line says what triggered the command,
//! what the display sent, and what came back.
//!
//! A held key repeats the navigation word many times a second. Each repeat
//! acts, and none writes a line: the hold writes one line for all of its
//! repeats when it ends, which is the next press or the idle hide.

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};

use serde_json::Value;

use crate::ipc::{self, Command};

/// The lines that wait for mpv's answer, by the request id their command
/// carries. The frame loop writes them and the socket reader takes them, on
/// two tasks, so the map takes a lock.
pub type Notes = Arc<Mutex<BTreeMap<u64, String>>>;

/// The first request id a press's command carries. The observes take the
/// ids 1 to 16 and the gate takes 1 and 2, and those answers are the
/// display's own business, so a press counts from well above them.
pub const FIRST_ID: u64 = 1000;

/// The third word the command sidecar adds to a navigation word that a held
/// key repeated.
pub const REPEAT: &str = "repeat";

/// What one press or repeat sends: the socket lines, and the line that ends
/// the hold before it, when there is one.
#[derive(Debug, Default, PartialEq)]
pub struct Pressed {
    pub lines: Vec<String>,
    pub ended: Option<String>,
}

/// One held key: its word, how many repeats it made, and the last command a
/// repeat sent.
#[derive(Debug)]
struct Hold {
    word: String,
    repeats: u32,
    last: Option<Command>,
}

/// The ids the display gives and the hold it is inside.
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
    /// Turn one press's commands into socket lines. A press leaves a note for
    /// each command a person would name; a repeat leaves none and counts
    /// toward its hold.
    pub fn press(
        &mut self,
        word: &str,
        repeat: bool,
        commands: Vec<Command>,
        notes: &Notes,
    ) -> Pressed {
        let mut pressed = Pressed::default();
        if repeat && self.hold.as_ref().is_some_and(|hold| hold.word == word) {
            let hold = self.hold.as_mut().expect("the hold stands");
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
            notes.lock().expect("no task panics with the notes").insert(
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

    /// End the hold, and answer its line when its repeats sent mpv anything.
    /// A hold that only moved the focus or the cursor sent nothing, and the
    /// press that follows it has its own line.
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

/// The line one of mpv's answers completes, and nothing for an id no press
/// gave.
pub fn answered(notes: &Notes, id: u64, error: &str) -> Option<String> {
    let note = notes
        .lock()
        .expect("no task panics with the notes")
        .remove(&id)?;
    Some(format!("{note}, mpv answered {error}"))
}

/// The lines still waiting when the socket closes. mpv will not answer them
/// now, so each is written with that reason, in the order it was sent.
pub fn abandoned(notes: &Notes) -> Vec<String> {
    std::mem::take(&mut *notes.lock().expect("no task panics with the notes"))
        .into_values()
        .map(|note| format!("{note}, mpv closed its socket before it answered"))
        .collect()
}

/// A command's words the way mpv's socket carries them.
fn words(command: &Command) -> String {
    Value::Array(command.clone()).to_string()
}

/// What a command does, in a person's words. The two broadcasts to the
/// command sidecar, the exit and the up-next ask, answer nothing: the sidecar
/// logs those itself.
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

/// A position as H:MM:SS, floored, the way the Play's status writes one.
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
    fn each_command_reads_in_a_persons_words() {
        let cases: Vec<(Command, Option<&str>)> = vec![
            (
                vec![json!("no-osd"), json!("cycle"), json!("pause")],
                Some("pause or resume"),
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

    /// A press writes one line, when mpv answers, with every fact in it.
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

    /// mpv's refusal reaches the line word for word.
    #[test]
    fn a_refusal_is_written_in_mpvs_words() {
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

    /// An answer under an id no press gave completes nothing.
    #[test]
    fn an_answer_to_the_displays_own_command_writes_nothing() {
        assert_eq!(answered(&notes(), 0, "success"), None);
    }

    /// The broadcasts and a press that sends nothing leave no note.
    #[test]
    fn a_press_mpv_runs_nothing_for_leaves_no_note() {
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
        assert!(notes.lock().expect("the notes").is_empty());
    }

    /// A hold writes one line for all of its repeats, when the next press
    /// arrives or when the caller settles it, and its repeats leave no note.
    #[test]
    fn a_hold_writes_one_line_for_its_repeats() {
        let notes = notes();
        let mut record = Record::default();
        let nudge = |to: f64| vec![vec![json!("set_property"), json!("sub-delay"), json!(to)]];

        record.press("right", false, nudge(0.1), &notes);
        for step in 2..=4 {
            let pressed = record.press("right", true, nudge(f64::from(step) / 10.0), &notes);
            assert_eq!(pressed.ended, None);
            assert!(pressed.lines[0].ends_with("\"request_id\":0}\n"));
        }
        let pressed = record.press("select", false, Vec::new(), &notes);

        assert_eq!(
            pressed.ended.as_deref(),
            Some(
                "media-display: right held for 3 repeats, last sent [\"set_property\",\"sub-delay\",0.4] to mpv"
            )
        );
        assert_eq!(notes.lock().expect("the notes").len(), 1);
        assert_eq!(record.settle(), None);
    }

    /// A repeat whose press the display never read starts a hold of its own,
    /// and one repeat reads in the singular.
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
        assert!(notes.lock().expect("the notes").is_empty());
    }

    /// A hold that moved only the focus sent mpv nothing, so it ends with no
    /// line.
    #[test]
    fn a_hold_that_sent_nothing_ends_quietly() {
        let notes = notes();
        let mut record = Record::default();
        record.press("down", false, Vec::new(), &notes);
        record.press("down", true, Vec::new(), &notes);

        assert_eq!(record.settle(), None);
    }

    /// The lines still waiting when the socket closes are written in the
    /// order they were sent, and the notes are empty after.
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
        assert!(abandoned(&notes).is_empty());
    }
}
