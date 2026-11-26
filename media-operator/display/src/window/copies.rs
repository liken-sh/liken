//! Gives mpv the display's copy of the selected text subtitle track, and
//! keeps mpv drawing it. The copy gives each cue of two or more lines a
//! margin of its own, so libass places the cue on its first frame.
//! `crate::subtitles::copy` builds the copy, and `media-operator` plan 49
//! has the design.
//!
//! The copy goes to mpv as a file in the directory of mpv's socket, the
//! pod's `/ipc` `emptyDir`, which the player container mounts too and
//! which is deleted with the pod. mpv reports a track's file as its
//! `external-filename` in every `track-list`, and `track-list` goes to the
//! display and to the command sidecar each time a track changes. A script
//! given as `memory://` would be its own file name, so each `track-list`
//! would carry every cue of the film. The media is mounted read-only, and
//! the display never writes it.
//!
//! The read of the media, the write of the copy, and the removal of an old
//! copy run off the drawing thread. Until the copy is ready, mpv draws the
//! original with the placement of plan 46. Each read has a number, and a
//! read stops once another read is current, so a read for a track or a
//! file that is no longer selected changes nothing.

use std::path::{Path, PathBuf};
use std::sync::Arc;

use iced::Task;
use serde_json::{Value, json};

use super::{Display, Message};
use crate::film::{Screen, Track};
use crate::subtitles::copy::at::Current;
use crate::subtitles::copy::source::{self, Ask, Why};
use crate::subtitles::copy::{self, Copy};

/// How many `track-list` messages can arrive after a `sub-add` without the
/// copy before the display adds it again. mpv can send one `track-list`
/// that it wrote before it read the `sub-add`.
const LISTS_WITHOUT_COPY: u64 = 2;

/// How many times the display adds one copy before it gives up on it.
const MOST_ADDS: u32 = 3;

/// The file, its place in the playlist, and the original track that a copy
/// is made from. The same file twice in a playlist is two originals,
/// because mpv drops the copy when it opens the file again.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Original {
    path: Option<String>,
    playlist_pos: Option<i64>,
    identity: String,
}

/// The result of one read, with the number of the read.
#[derive(Debug, Clone)]
pub struct Copied {
    pub read: u64,
    pub result: Result<Arc<Copy>, Why>,
}

/// The result of the write of copy `number` to `file`.
#[derive(Debug, Clone)]
pub struct Written {
    pub number: u64,
    pub file: PathBuf,
    pub result: Result<(), String>,
}

/// A copy that is being written, with what its `sub-add` needs.
#[derive(Debug, Clone)]
struct Writing {
    number: u64,
    script: Arc<String>,
    title: String,
    lang: String,
}

/// The copy that mpv was given.
#[derive(Debug, Clone)]
struct Sent {
    script: Arc<String>,
    file: String,
    title: String,
    lang: String,
    /// The `track-list` count when the display last added it.
    added_at: u64,
    adds: u32,
}

#[derive(Debug)]
pub(super) struct Copies {
    /// The directory that the copies are written in.
    pub(super) dir: PathBuf,
    /// Runs each write and removal on the calling thread, so a test sees
    /// the files at once.
    pub(super) inline: bool,
    current: Current,
    pub(super) reads: u64,
    /// The number of the last copy written, which names its file.
    written: u64,
    /// The files that the display wrote and has not removed.
    files: Vec<PathBuf>,
    /// The original that the latest read is for, while it runs.
    reading: Option<Original>,
    ready: Option<(Original, Arc<Copy>)>,
    /// The original whose read or write failed, so it is not tried again.
    failed: Option<Original>,
    /// The script of the ready copy for the screen and the encoded bar it
    /// was built for, so a tick of a fade does not build it again.
    pub(super) script: Option<(Bars, Arc<String>)>,
    writing: Option<Writing>,
    sent: Option<Sent>,
    /// The `track-list` count when the display last selected the copy.
    asked: Option<u64>,
    /// The copies that the display asked mpv to remove.
    removed: Vec<i64>,
}

impl Copies {
    /// The copies of a display whose mpv serves its socket at `socket`
    /// go in the socket's directory.
    pub(super) fn beside(socket: &Path) -> Self {
        Self {
            dir: socket
                .parent()
                .map_or_else(|| PathBuf::from("/ipc"), Path::to_path_buf),
            inline: false,
            current: Current::default(),
            reads: 0,
            written: 0,
            files: Vec::new(),
            reading: None,
            ready: None,
            failed: None,
            script: None,
            writing: None,
            sent: None,
            asked: None,
            removed: Vec::new(),
        }
    }

    fn sent_file(&self) -> Option<&str> {
        self.sent.as_ref().map(|sent| sent.file.as_str())
    }

    /// Takes the files that the display wrote out of its list. `keep` stays.
    fn take_files(&mut self, keep: Option<&str>) -> Vec<PathBuf> {
        let (kept, taken) = std::mem::take(&mut self.files)
            .into_iter()
            .partition(|file| Some(file.as_os_str()) == keep.map(std::ffi::OsStr::new));
        self.files = kept;
        taken
    }

    /// Removes every file that the display wrote, on this thread. The
    /// display calls it as it ends.
    pub(super) fn remove_files(&mut self) {
        for file in self.take_files(None) {
            let _ = std::fs::remove_file(file);
        }
    }
}

/// The lower bar that a copy's margins are built for: mpv's
/// `osd-dimensions`, and the share of the selected video track's frame that
/// is black below its active area, from the item's `presentation.video`.
#[derive(Debug, Clone, Copy, PartialEq)]
pub(super) struct Bars {
    screen: Screen,
    encoded: f64,
}

fn command(words: Vec<Value>) -> String {
    crate::ipc::line(&words)
}

impl Display {
    /// Starts the read of the selected track, gives mpv the copy once it is
    /// ready, and selects the copy when mpv draws the original. It waits
    /// for `osd-dimensions`, which the margins need, and for
    /// `demuxer-start-time`, which times the cues of an embedded track.
    pub(super) fn follow_copies(&mut self) -> Task<Message> {
        if self.detached {
            return Task::none();
        }
        let mut tasks = vec![self.remove_stale_copies()];
        let Some(track) = self.film.selected_subtitle().cloned() else {
            return Task::batch(tasks);
        };
        let (Some(screen), Some(start)) = (self.film.screen, self.film.start_time) else {
            return Task::batch(tasks);
        };
        let original = Original {
            path: self.film.path.clone(),
            playlist_pos: self.film.playlist_pos,
            identity: source::identity(&track),
        };
        if let Some((ready, copy)) = &self.copies.ready
            && *ready == original
        {
            let copy = Arc::clone(copy);
            let bars = Bars {
                screen,
                encoded: self.encoded_bar(),
            };
            tasks.push(self.give_copy(&track, &original, &copy, bars));
            return Task::batch(tasks);
        }
        let copies = &mut self.copies;
        if copies.reading.as_ref() == Some(&original) || copies.failed.as_ref() == Some(&original) {
            return Task::batch(tasks);
        }
        copies.reads += 1;
        copies.current.set(copies.reads);
        let stale = copies.take_files(None);
        tasks.push(self.remove_later(stale));
        let copies = &mut self.copies;
        copies.reading = Some(original.clone());
        copies.ready = None;
        copies.script = None;
        copies.writing = None;
        copies.sent = None;
        copies.asked = None;
        copies.removed.clear();
        let read = copies.reads;
        let current = copies.current.clone();
        let Some(path) = original.path.clone() else {
            return Task::batch(tasks);
        };
        let fps = self.film.fps();
        let events = self.presentation.streams(&self.film).events(&track);
        tasks.push(Task::future(async move {
            let result = tokio::task::spawn_blocking(move || {
                let ask = Ask {
                    fps,
                    start,
                    events,
                    current: &current,
                    read,
                };
                source::read(&path, &track, &ask)
            })
            .await
            .unwrap_or_else(|error| Err(Why::Broken(error.to_string())));
            Message::Copied(Copied {
                read,
                result: result.map(Arc::new),
            })
        }));
        Task::batch(tasks)
    }

    /// Keeps the result of the latest read, and logs once whether the track
    /// has a copy.
    pub(super) fn on_copied(&mut self, copied: Copied) {
        let copies = &mut self.copies;
        if copied.read != copies.reads {
            return;
        }
        let Some(original) = copies.reading.take() else {
            return;
        };
        let line = match copied.result {
            Ok(copy) => {
                let line = format!(
                    "media-display: subtitle track {} is copied with {} cues, so a cue of two or more lines has its own margin",
                    original.identity,
                    copy.events.len()
                );
                copies.ready = Some((original, copy));
                Some(line)
            }
            Err(Why::Codec(_) | Why::Superseded) => {
                copies.failed = Some(original);
                None
            }
            Err(why) => {
                let line = format!(
                    "media-display: subtitle track {} keeps the placement of every cue on the house row, because {why}",
                    original.identity
                );
                copies.failed = Some(original);
                Some(line)
            }
        };
        self.say(line);
    }

    /// The share of the selected video track's frame that is black below
    /// its active area, or 0 when the item's block has no area for it.
    fn encoded_bar(&self) -> f64 {
        let streams = self.presentation.streams(&self.film);
        self.film
            .tracks_of("video")
            .find(|track| track.selected)
            .map_or(0.0, |track| streams.encoded_bar(track))
    }

    /// Writes the script of the copy for `bars`, gives mpv the file once
    /// it is written, and selects the copy while mpv draws the original.
    fn give_copy(
        &mut self,
        track: &Track,
        original: &Original,
        copy: &Copy,
        bars: Bars,
    ) -> Task<Message> {
        let copies = &mut self.copies;
        let script = match &copies.script {
            Some((built, script)) if *built == bars => Arc::clone(script),
            _ => {
                let script = Arc::new(copy.script(Some(&bars.screen), bars.encoded));
                copies.script = Some((bars, Arc::clone(&script)));
                script
            }
        };
        let given = copies
            .sent
            .as_ref()
            .is_some_and(|sent| sent.script == script);
        let pending = copies
            .writing
            .as_ref()
            .is_some_and(|writing| writing.script == script);
        if !given && !pending {
            copies.written += 1;
            let number = copies.written;
            let file = copies.dir.join(copy::file_name(&original.identity, number));
            copies.writing = Some(Writing {
                number,
                script: Arc::clone(&script),
                title: track.title.clone().unwrap_or_default(),
                lang: track.lang.clone().unwrap_or_default(),
            });
            return self.write_later(number, file, script);
        }
        if !given {
            return Task::none();
        }
        let lists = self.film.track_lists;
        // mpv can list the copy twice. The one that mpv draws counts.
        let mut listed: Vec<(i64, bool)> = self
            .film
            .copies
            .iter()
            .filter(|(_, listed)| listed.external_filename.as_deref() == copies.sent_file())
            .map(|(_, listed)| (listed.id, listed.selected))
            .collect();
        listed.sort_by_key(|(_, selected)| !*selected);
        let listed = listed.first().copied();
        let Some(sent) = copies.sent.as_mut() else {
            return Task::none();
        };
        match listed {
            None if lists >= sent.added_at + LISTS_WITHOUT_COPY && sent.adds < MOST_ADDS => {
                sent.adds += 1;
                sent.added_at = lists;
                let add = add_command(sent);
                let _ = self.wire.commands.send(add);
            }
            None => {}
            Some((id, false)) if copies.asked != Some(lists) => {
                copies.asked = Some(lists);
                let select = command(vec![json!("set_property"), json!("sid"), json!(id)]);
                let _ = self.wire.commands.send(select);
            }
            Some(_) => {}
        }
        Task::none()
    }

    /// Gives mpv the copy once its file is written, or keeps the original
    /// when the write failed.
    pub(super) fn on_written(&mut self, written: Written) {
        let copies = &mut self.copies;
        let Some(writing) = copies
            .writing
            .take_if(|writing| writing.number == written.number)
        else {
            // A newer copy replaced this one while it was written.
            if written.result.is_ok() {
                copies.files.push(written.file);
            }
            return;
        };
        if let Err(error) = written.result {
            let original = copies.ready.take().map(|(original, _)| original);
            let identity = original
                .as_ref()
                .map_or_else(String::new, |original| original.identity.clone());
            copies.failed = original;
            let line = format!(
                "media-display: subtitle track {identity} keeps the placement of every cue on the house row, because its copy could not be written to {}: {error}",
                written.file.display()
            );
            self.say(Some(line));
            return;
        }
        let sent = Sent {
            script: writing.script,
            file: written.file.display().to_string(),
            title: writing.title,
            lang: writing.lang,
            added_at: self.film.track_lists,
            adds: 1,
        };
        copies.files.push(written.file);
        let add = add_command(&sent);
        copies.sent = Some(sent);
        copies.asked = Some(self.film.track_lists);
        let _ = self.wire.commands.send(add);
    }

    /// Writes `script` to `file` off the drawing thread, and reports the
    /// result as `Message::Written`.
    fn write_later(&mut self, number: u64, file: PathBuf, script: Arc<String>) -> Task<Message> {
        let target = file.clone();
        let write = move || {
            let result =
                std::fs::write(&file, script.as_bytes()).map_err(|error| error.to_string());
            Written {
                number,
                file,
                result,
            }
        };
        if self.copies.inline {
            self.on_written(write());
            return Task::none();
        }
        Task::future(async move {
            let written = tokio::task::spawn_blocking(write)
                .await
                .unwrap_or_else(|error| Written {
                    number,
                    file: target,
                    result: Err(error.to_string()),
                });
            Message::Written(written)
        })
    }

    /// Removes `files` off the drawing thread. A file that is already gone
    /// is not an error.
    fn remove_later(&self, files: Vec<PathBuf>) -> Task<Message> {
        if files.is_empty() {
            return Task::none();
        }
        let remove = move || {
            for file in files {
                let _ = std::fs::remove_file(file);
            }
        };
        if self.copies.inline {
            remove();
            return Task::none();
        }
        Task::future(async move {
            let _ = tokio::task::spawn_blocking(remove).await;
        })
        .discard()
    }

    /// Asks mpv to remove each copy that is not the one last given and that
    /// mpv does not draw, such as the copy for an earlier screen, and
    /// removes its file. mpv read the whole file when it added the track.
    fn remove_stale_copies(&mut self) -> Task<Message> {
        let copies = &mut self.copies;
        // mpv can list the current copy twice, after the display added it
        // again. The one that mpv draws stays, or else the first.
        let current: Vec<(i64, bool)> = self
            .film
            .copies
            .iter()
            .filter(|(_, listed)| {
                listed.external_filename.is_some()
                    && listed.external_filename.as_deref() == copies.sent_file()
            })
            .map(|(_, listed)| (listed.id, listed.selected))
            .collect();
        let keep = current
            .iter()
            .find(|(_, selected)| *selected)
            .or(current.first())
            .map(|(id, _)| *id);
        let mut stale = Vec::new();
        for (_, listed) in &self.film.copies {
            let file = listed.external_filename.as_deref();
            let duplicate = current.iter().any(|(id, _)| *id == listed.id);
            if Some(listed.id) == keep
                || (listed.selected && !duplicate)
                || copies.removed.contains(&listed.id)
            {
                continue;
            }
            copies.removed.push(listed.id);
            let remove = command(vec![json!("sub-remove"), json!(listed.id)]);
            let _ = self.wire.commands.send(remove);
            if duplicate {
                continue;
            }
            if let Some(file) = file
                && let Some(at) = copies
                    .files
                    .iter()
                    .position(|written| written.as_os_str() == file)
            {
                stale.push(copies.files.remove(at));
            }
        }
        self.remove_later(stale)
    }
}

/// The `sub-add` of a copy, selected, with the original's title and
/// language.
fn add_command(sent: &Sent) -> String {
    command(vec![
        json!("sub-add"),
        json!(sent.file),
        json!("select"),
        json!(sent.title),
        json!(sent.lang),
    ])
}

#[cfg(test)]
mod tests;
