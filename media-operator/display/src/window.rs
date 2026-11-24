//! The display's Wayland window, the frame loop that drives it, and the
//! drawing of the on-screen display (OSD) over mpv's video.
//!
//! The window opens at 1920 by 1080 logical pixels, undecorated and
//! transparent. Where the display draws nothing, the film shows through
//! every pixel.

use std::hash::{Hash, Hasher};
use std::path::Path;
use std::sync::Arc;
use std::time::{Duration, Instant};

use iced::widget::canvas;
use iced::{Color, Element, Length, Rectangle, Renderer, Size, Subscription, Task, Theme};
use jiff::ZonedDateTime;
use tokio::sync::{broadcast, watch};

mod arranging;
mod detaching;
mod subtitles;

use crate::arranger::link::{self, Link};
use crate::arranger::{self as arrangement, Arranger};
use crate::art::{self, Answer, Art, Job};
use crate::canvas::{Brush, Canvas, Scrims};
use crate::cast::{Cast, Spans};
use crate::fade::Hide;
use crate::film::Film;
use crate::focus::{Focus, Parts, Stop};
use crate::ipc::{self, Ipc};
use crate::presentation::Presentation;
use crate::record::{self, Notes, Record};
use crate::scrubber::Scrubber;
use crate::skip::Skip;
use crate::strip::Strip;
use crate::upnext::{self, UpNext};
use crate::volume::Volume;
use crate::{clock, header, images, theme};

/// How long `main` waits for mpv to report a position. When mpv reports
/// none in that time, the display exits with an error and the kubelet
/// restarts the container.
pub const PLAYER_GRACE: Duration = Duration::from_secs(60);

/// The capacity of each channel between mpv's socket and the frame loop,
/// in both directions.
const EVENT_QUEUE: usize = 64;

#[derive(Debug, Clone)]
pub enum Message {
    /// One event from mpv's socket: a script message, a property
    /// change, or the socket closing.
    Mpv(ipc::Event),
    Tick,
    /// The wall-clock minute changed.
    Minute,
    Resized(Size),
    /// The window's scale factor: how many output pixels one logical
    /// pixel covers.
    Rescaled(f32),
    /// The read of an item's spans file finished. The number identifies
    /// the item, so `Cast::on_read` ignores the result for an item that
    /// was replaced.
    Cast(u64, Option<Spans>),
    /// The display's hide timer fired. The number identifies the timer,
    /// so a timer that a later summon replaced hides nothing.
    Hide(u64),
    HideCard(u64),
    HideRow(u64),
    Art(Answer),
    Attached(Result<Link, String>),
    Arranger(arrangement::Event),
    Close,
}

/// A timer that hides one element after `wait`. A fired timer cannot be
/// recalled, so each arm gets a new number and its message carries that
/// number. A timer that a later arm or a cancel replaced no longer
/// matches `armed`, so it hides nothing.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct HideTimer {
    wait: Duration,
    count: u64,
    armed: Option<u64>,
}

impl HideTimer {
    fn new(wait: Duration) -> Self {
        Self {
            wait,
            count: 0,
            armed: None,
        }
    }

    fn arm(&mut self, hide: Hide, wrap: fn(u64) -> Message) -> Task<Message> {
        match hide {
            Hide::Arm => {
                self.count += 1;
                let at = self.count;
                self.armed = Some(at);
                Task::perform(tokio::time::sleep(self.wait), move |()| wrap(at))
            }
            Hide::Cancel => {
                self.armed = None;
                Task::none()
            }
            Hide::Keep => Task::none(),
        }
    }

    /// Forgets the current timer. It still fires, but `expired` returns
    /// false for it.
    fn cancel(&mut self) {
        self.armed = None;
    }

    /// Whether the timer numbered `at` is the current one. A true result
    /// also clears it, so a timer takes effect once.
    fn expired(&mut self, at: u64) -> bool {
        if self.armed != Some(at) {
            return false;
        }
        self.armed = None;
        true
    }
}

/// The state of the display: what mpv reports, the current item's
/// `presentation` block, which control has focus, and each fade.
pub struct Display {
    wire: Wire,
    /// The request ids of the commands that presses send, and the held
    /// key whose repeats go into one log line.
    record: Record,
    /// The log lines written, for tests to read.
    #[cfg(test)]
    logged: Vec<String>,
    focus: Focus,
    film: Film,
    presentation: Presentation,
    scrubber: Scrubber,
    strip: Strip,
    upnext: UpNext,
    skip: Skip,
    volume: Volume,
    cast: Cast,
    art: Art,
    subtitles: crate::subtitles::Subtitles,
    /// `link` is the Wayland binding that sends each arrangement to the
    /// compositor.
    arranger: Arranger,
    link: Option<Link>,
    /// Whether mpv's socket closed. After that the display draws
    /// nothing until it exits. The `detaching` module gives the reason.
    detached: bool,
    #[cfg(test)]
    committed: Vec<arrangement::Commit>,
    /// The hide timers of the whole display, the up-next card, and the
    /// volume row. They run independently.
    idle: HideTimer,
    card: HideTimer,
    row: HideTimer,
    /// When the display started, on a monotonic clock. The scrubber
    /// times its scans from it, and a wall-clock jump would break them.
    started: Instant,
    /// How many frame ticks have run. The track panel's glides read this
    /// clock, so a glide moves only on frames that the display draws, and
    /// a test moves it by sending ticks.
    tick: u64,
    /// The `Signature` of the last position that was drawn.
    signature: Option<Signature>,
    /// The layout space of the surface that the compositor configured.
    canvas: Canvas,
    density: f32,
    /// The scrims for `canvas` and `density`.
    scrims: Scrims,
    /// Tells the socket reader task what is on screen, so `Pace` can
    /// drop positions that change nothing on screen.
    watching: watch::Sender<Watching>,
    /// The cached drawing of each layer. mpv sends a position on every
    /// video frame, so a layer is drawn again only after a `redraw`
    /// clears its cache.
    below: canvas::Cache,
    above: canvas::Cache,
}

/// The parts of a position that show on screen: the output pixel of the
/// playhead and the whole second on the time label.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct Signature {
    pixel: Option<i32>,
    second: i64,
}

impl Display {
    pub fn new(ipc: Ipc) -> Self {
        let (commands, _) = broadcast::channel(EVENT_QUEUE);
        let canvas = Canvas::default();
        let (watching, watched) = watch::channel(Watching {
            canvas,
            visible: false,
        });
        Self {
            wire: Wire {
                path: Arc::from(ipc.path()),
                commands,
                notes: Notes::default(),
                watching: watched,
            },
            record: Record::default(),
            #[cfg(test)]
            logged: Vec::new(),
            focus: Focus::new(),
            film: Film::default(),
            presentation: Presentation::default(),
            scrubber: Scrubber::default(),
            strip: Strip::default(),
            upnext: UpNext::default(),
            skip: Skip::default(),
            volume: Volume::default(),
            cast: Cast::default(),
            art: Art::default(),
            subtitles: crate::subtitles::Subtitles::default(),
            arranger: Arranger::default(),
            link: None,
            detached: false,
            #[cfg(test)]
            committed: Vec::new(),
            idle: HideTimer::new(theme::IDLE_HIDE),
            card: HideTimer::new(theme::CARD_HIDE),
            row: HideTimer::new(theme::IDLE_HIDE),
            started: Instant::now(),
            tick: 0,
            signature: None,
            canvas,
            density: 1.0,
            scrims: Scrims::for_canvas(&canvas, 1.0),
            watching,
            below: canvas::Cache::new(),
            above: canvas::Cache::new(),
        }
    }

    /// Handles one message. Then it updates the arrangement, the
    /// subtitle position, and the image decodes for the new state, and
    /// tells the socket reader what is on screen.
    pub fn update(&mut self, message: Message) -> Task<Message> {
        let task = self.step(message);
        self.follow_offer();
        self.follow_subtitles();
        let jobs = self.sync_art();
        let _ = self.watching.send(Watching {
            canvas: self.canvas,
            visible: self.focus.visible(),
        });
        Task::batch([task, decode(jobs)])
    }

    fn step(&mut self, message: Message) -> Task<Message> {
        match message {
            Message::Mpv(ipc::Event::Message(words)) => return self.on_message(&words),
            Message::Mpv(ipc::Event::Property { name, value }) => {
                return self.on_property(&name, &value);
            }
            Message::Mpv(ipc::Event::Detached) => self.on_detached(),
            Message::Tick => {
                self.tick += 1;
                self.focus.fade_mut().step();
                self.upnext.fade_mut().step();
                self.upnext.column_mut().step();
                self.skip.fade_mut().step();
                self.volume.fade_mut().step();
                self.redraw();
            }
            // The clock and the film's end time are in the lower layer.
            // The up-next card shows an end time too, and while the OSD
            // is down the card is in the upper layer.
            Message::Minute => {
                self.redraw_below();
                if self.upnext.draws_outside(self.focus.visible()) {
                    self.redraw_above();
                }
            }
            Message::Resized(size) => self.on_resize(size),
            Message::Rescaled(density) => self.on_rescale(density),
            Message::Hide(at) if self.idle.expired(at) => {
                // Hiding the display ends a held key's hold, so its log
                // line is written now.
                let ended = self.record.settle();
                self.say(ended);
                let mut focus = self.focus;
                focus.dismiss(&mut self.parts());
                self.focus = focus;
                self.redraw();
                return self.arm_hide_timers();
            }
            Message::HideCard(at) => {
                if self.card.expired(at) {
                    self.upnext.hide();
                    self.redraw_above();
                }
            }
            Message::HideRow(at) => {
                if self.row.expired(at) {
                    self.volume.hide();
                    self.redraw_above();
                }
            }
            Message::Hide(_) => {}
            Message::Cast(item, spans) => {
                if self.cast.on_read(item, spans) {
                    self.redraw();
                }
            }
            Message::Art(answer) => {
                let (changed, jobs) = self.art.on_answer(answer);
                if changed {
                    self.redraw();
                }
                return decode(jobs);
            }
            Message::Attached(link) => self.on_attached(link),
            Message::Arranger(event) => return self.on_arranger(event),
            Message::Close => return self.on_close(),
        }
        Task::none()
    }

    /// Rebuilds the scrims for the new surface size. Later image
    /// decodes use the new canvas.
    fn on_resize(&mut self, size: Size) {
        let canvas = Canvas::for_output(size);
        if canvas == self.canvas {
            return;
        }
        self.canvas = canvas;
        self.scrims = Scrims::for_canvas(&canvas, self.density);
        self.redraw();
    }

    /// Rebuilds the scrims for a new scale factor, so that each scrim
    /// row still covers one output row. The factor changes when the
    /// window moves to another output or reports its first factor. A
    /// factor that is NaN or not positive is ignored.
    fn on_rescale(&mut self, density: f32) {
        if density.is_nan() || density <= 0.0 || density == self.density {
            return;
        }
        self.density = density;
        self.scrims = Scrims::for_canvas(&self.canvas, density);
        self.redraw();
    }

    /// Clears both layer caches, so the next frame draws them again.
    /// Call it after any change to what the display draws.
    fn redraw(&self) {
        self.redraw_below();
        self.redraw_above();
    }

    /// Clears the lower layer, which holds the OSD and the music cover.
    /// A position that moves only the playhead or the time label clears
    /// only this layer.
    fn redraw_below(&self) {
        self.below.clear();
    }

    /// Clears the upper layer, which holds an open track panel, the volume
    /// row, and the up-next card and the skip control while they draw
    /// over the bare video. The card's and the row's hide timers clear
    /// only this layer.
    fn redraw_above(&self) {
        self.above.clear();
    }

    /// Borrows the parts of the display that a press can change, for
    /// the focus router to use during one call.
    fn parts(&mut self) -> Parts<'_> {
        let now = self.now();
        Parts {
            presentation: &self.presentation,
            film: &self.film,
            scrubber: &mut self.scrubber,
            strip: &mut self.strip,
            now,
            tick: self.tick,
            upnext: &mut self.upnext,
            skip: &mut self.skip,
            cast: &mut self.cast,
        }
    }

    /// Handles a script message from the command sidecar: a navigation
    /// word (`up`, `down`, `left`, `right`, `select`, or `back`),
    /// `summon`, `presentation`, `next`, or `volume-changed`. Returns the
    /// tasks that arm the hide timers and read a new item's spans file.
    fn on_message(&mut self, words: &[String]) -> Task<Message> {
        let Some(word) = words.first() else {
            return Task::none();
        };
        let mut focus = self.focus;
        if let Some(action) = crate::focus::Action::from_word(word) {
            let repeat = words.get(1).is_some_and(|second| second == record::REPEAT);
            let (stepped, waited) = (self.upnext.stepped_back(), self.upnext.waiting());
            let commands = focus.nav(action, &mut self.parts());
            self.after_press(action == crate::focus::Action::Back, stepped, waited);
            let pressed = self.record.press(word, repeat, commands, &self.wire.notes);
            self.say(pressed.ended);
            for line in pressed.lines {
                let _ = self.wire.commands.send(line);
            }
        } else if word == ipc::SUMMON {
            // A summon ends the current hold, so the hold's log line is
            // written now.
            let ended = self.record.settle();
            self.say(ended);
            focus.summon(&self.parts());
        } else if word == ipc::PRESENTATION {
            self.presentation
                .receive(words.get(1).map_or("", String::as_str));
            self.presentation.arrived_at(self.film.playlist_pos);
            // A `presentation` block starts a new item, so drop the
            // previous item's pictures and the cached text measurements.
            crate::canvas::forget_measurements();
            self.art.on_item(&self.presentation, &self.canvas);
            // The new item's marks can show or hide the skip control.
            self.follow_marks();
            let read = self.cast.on_item(
                self.presentation.appearances(),
                self.presentation.contributors(),
            );
            if let Some((item, path)) = read {
                self.focus = focus;
                self.redraw();
                return Task::batch([self.arm_hide_timers(), read_spans(item, path)]);
            }
        } else if word == ipc::NEXT {
            self.upnext.receive(words.get(1).map_or("", String::as_str));
        } else if word == ipc::VOLUME_CHANGED {
            let Some((level, muted, draw)) = crate::volume::changed(words) else {
                return Task::none();
            };
            if draw {
                self.volume.show(level, muted);
            } else {
                self.volume.record(level, muted);
            }
        } else {
            return Task::none();
        }
        self.focus = focus;
        self.redraw();
        self.arm_hide_timers()
    }

    /// Applies a property change from mpv. mpv sends `time-pos` on
    /// every video frame, and a redraw draws a whole layer again. So a
    /// position redraws the lower layer only when its `Signature`
    /// changes.
    ///
    /// A position also raises the up-next card when the playhead reaches
    /// `rise`, and shows or hides the skip control at the edges of an
    /// intro or a recap. Both happen while the display is down too.
    fn on_property(&mut self, name: &str, value: &serde_json::Value) -> Task<Message> {
        // The scale's mark glides from the delay it showed to the new one.
        let before = match name {
            "audio-delay" => Some(self.film.audio_delay),
            "sub-delay" => Some(self.film.sub_delay),
            _ => None,
        };
        self.film.apply(name, value);
        if let Some(before) = before {
            self.strip
                .on_delay(name, before, &self.presentation, self.tick);
        }
        if name == "time-pos" {
            let rose = self
                .upnext
                .on_percent(self.percent(), &self.film, self.rise());
            if rose {
                self.on_rise();
                self.redraw();
            }
            self.follow_marks();
            // The cast row disappears at a time where the spans file
            // names nobody, so the focus moves off it to the bar.
            if self.focus.focused_stop() == Some(Stop::Cast) && !self.cast.available(&self.film) {
                let mut focus = self.focus;
                focus.refocus(&self.parts());
                self.focus = focus;
            }
            // While the display is down, nothing drawn depends on the
            // position.
            if self.focus.fade().value() > 0.0 {
                let signature = self.signature(value.as_f64());
                if self.signature != Some(signature) {
                    self.signature = Some(signature);
                    self.redraw_below();
                }
            }
            return match rose {
                true => self.arm_hide_timers(),
                false => Task::none(),
            };
        }
        self.redraw();
        // The next file can have no track of the open panel's kind. The
        // panel then closes without a press, so the hide timer that it held
        // off starts again.
        if name == "track-list" && self.strip.on_tracks(&self.presentation, &self.film) {
            let mut focus = self.focus;
            focus.summon(&self.parts());
            self.focus = focus;
            return self.arm_hide_timers();
        }
        if name == "pause" {
            let mut focus = self.focus;
            focus.on_pause(value.as_bool().unwrap_or(false), &mut self.parts());
            self.focus = focus;
            return self.arm_hide_timers();
        }
        // The up-next offer belongs to the item that the `Play` started
        // on, so a move to another playlist entry drops it.
        if name == "playlist-pos" {
            self.presentation.on_playlist_pos(value.as_i64());
            self.upnext.on_playlist_pos(value.as_i64());
        }
        Task::none()
    }

    /// Shows the skip control while the playhead is inside an intro or
    /// a recap, and hides it when the playhead leaves. When the control
    /// disappears while it has focus, the focus moves to the bar.
    fn follow_marks(&mut self) {
        let inside = self
            .presentation
            .marks()
            .skippable(self.film.position, self.film.duration);
        if !self.skip.on_position(self.film.position, inside) {
            return;
        }
        let mut focus = self.focus;
        focus.refocus(&self.parts());
        self.focus = focus;
        self.redraw();
    }

    /// The playhead's position as a percentage of the duration,
    /// computed from `time-pos` and `duration`. It is `None` while
    /// either is unknown or the duration is 0.
    fn percent(&self) -> Option<f64> {
        let duration = self.film.duration.filter(|duration| *duration > 0.0)?;
        Some(self.film.position? / duration * 100.0)
    }

    /// Updates `art` for the current state and returns the decodes to
    /// start.
    fn sync_art(&mut self) -> Vec<Job> {
        let (canvas, preview) = (self.canvas, self.previewing());
        self.art.frame_next(self.upnext.art_box());
        let portraits = match self.cast_showing() {
            true => self.cast.portraits(&self.film),
            false => Vec::new(),
        };
        self.art.sync(
            &self.presentation,
            &self.film,
            &canvas,
            preview,
            self.upnext.art(),
            &portraits,
        )
    }

    fn bridge(&self, canvas: &Canvas) -> Bridge<'_> {
        Bridge {
            art: &self.art,
            canvas: *canvas,
        }
    }

    /// Seconds since the display started, on a monotonic clock.
    fn now(&self) -> f64 {
        self.started.elapsed().as_secs_f64()
    }

    /// Whether the pictures of the OSD are drawn: the logo, the scan
    /// thumbnail, and the cast row. They hide with the OSD and while a
    /// track panel is open.
    fn showing(&self) -> bool {
        self.focus.visible() && self.strip.capturing().is_none()
    }

    /// The time that the scan thumbnail shows. It is `None` unless the
    /// OSD pictures show, the fine axis has focus, a scan is running, and
    /// the item has a `trickplay` directory. With no scan running, the
    /// video itself shows the frame at the cursor.
    fn previewing(&self) -> Option<f64> {
        (self.showing()
            && self.focus.focused_stop() == Some(Stop::Fine)
            && self.scrubber.scanning()
            && self.presentation.trickplay().is_some())
        .then(|| self.scrubber.cursor_time(&self.film))
        .flatten()
    }

    /// The playback second at which the up-next card appears: the start
    /// of the credits when the item's marks include them, otherwise
    /// `series_rise` for an episode and `time_rise` for anything else.
    /// `Marks::credits` also caps the wait for a scene after the credits
    /// at that same rule.
    fn rise(&self) -> Option<f64> {
        let latest = match self.presentation.series() {
            Some(_) => upnext::series_rise,
            None => upnext::time_rise,
        };
        let duration = self.film.duration.filter(|duration| *duration > 0.0)?;
        let credits = self.presentation.marks().credits(Some(duration), latest);
        Some(credits.unwrap_or_else(|| latest(duration)))
    }

    /// Whether the cast row is drawn: the OSD pictures show and the
    /// playhead is in a span that names someone. The scan thumbnail
    /// covers the same area and shows another time, so the row hides
    /// during a scan.
    fn cast_showing(&self) -> bool {
        self.showing() && self.previewing().is_none() && self.cast.available(&self.film)
    }

    fn signature(&self, at: Option<f64>) -> Signature {
        Signature {
            pixel: crate::scrubber::position_pixel(&self.canvas, &self.film, at),
            second: (at.unwrap_or(0.0) + 0.5).floor() as i64,
        }
    }

    /// Arms or cancels each hide timer that the last change requested.
    /// The returned task completes when a timer fires.
    fn arm_hide_timers(&mut self) -> Task<Message> {
        let idle = self.idle.arm(self.focus.take_hide(), Message::Hide);
        let card = self.card.arm(self.upnext.take_hide(), Message::HideCard);
        let row = self.row.arm(self.volume.take_hide(), Message::HideRow);
        Task::batch([idle, card, row])
    }

    /// Writes the log line to stderr, if there is one.
    fn say(&mut self, line: Option<String>) {
        let Some(line) = line else {
            return;
        };
        eprintln!("{line}");
        #[cfg(test)]
        self.logged.push(line);
    }

    /// Draws the lower layer. The scrubber has two focus stops but
    /// draws one bar, for the focused axis.
    fn paint_below(&self, brush: &mut Brush<'_>, now: &ZonedDateTime) {
        let canvas = brush.canvas();
        // The cover draws at full strength whatever the OSD fade is,
        // because a music screen with the OSD down would otherwise be a
        // black frame. Every other picture fades with the layer.
        if let Some(cover) = self.art.cover() {
            brush.at_fade(1.0, |brush| {
                cover.draw(brush, art::cover_at(&canvas, cover));
            });
        }
        if brush.fade() <= 0.0 {
            return;
        }

        // Each scrim draws before the text it darkens, so the text is
        // on top.
        let logo = self.art.logo();
        let head = header::lines(
            &self.presentation,
            &self.film,
            logo.map(|logo| logo.canvas_height(&canvas)),
        );
        let reading = clock::lines(&canvas, &self.film, now);
        if let Some(logo) = logo.filter(|_| self.showing()) {
            logo.draw(brush, art::logo_at(&canvas));
        }
        if !head.is_empty() || !reading.is_empty() {
            if let Some(top) = &self.scrims.top {
                brush.scrim(top);
            }
            for line in head.into_iter().chain(reading) {
                brush.text(line);
            }
        }

        let bar = (!self.presentation.is_image())
            .then(|| self.scrubber.bar(&canvas, &self.film, self.focus.axis()))
            .flatten();
        let counter = if images::available(&self.presentation) {
            images::lines(&canvas, &self.film)
        } else {
            Vec::new()
        };
        let on_strip = self.focus.focused_stop() == Some(Stop::Strip);
        let strip = Strip::available(&self.presentation, &self.film);
        if bar.is_some() || !counter.is_empty() || strip {
            // While the cast row shows, the taller cast scrim replaces
            // the bottom scrim, so the row is darkened as well.
            let scrim = match self.cast_showing() {
                true => &self.scrims.cast,
                false => &self.scrims.bottom,
            };
            if let Some(bottom) = scrim {
                brush.scrim(bottom);
            }
            // The track panel's darkening is in this layer, so it is under
            // the scrubber's and the strip's words.
            self.strip.draw_shade(brush, &self.presentation, &self.film);
            if let Some(bar) = bar.as_ref() {
                self.scrubber.draw(brush, bar);
            }
            for line in counter {
                brush.text(line);
            }
            self.strip
                .draw(brush, &self.presentation, &self.film, on_strip);
        }
        // While the up-next card shows, the cast row stops
        // `CARD_CLEAR` short of the card.
        if self.cast_showing() {
            let limit = self
                .upnext
                .showing_card()
                .then(|| self.upnext.left(&canvas) - crate::cast::CARD_CLEAR);
            self.cast.draw(
                brush,
                &self.film,
                &self.art,
                self.focus.focused_stop() == Some(Stop::Cast),
                limit,
                now.date(),
            );
        }
        // The skip control draws before the thumbnail, so a scan near
        // the start of the film shows its frame over the control.
        self.skip
            .draw(brush, self.focus.focused_stop() == Some(Stop::Skip));
        if let Some(tile) = self.art.tile()
            && self.previewing().is_some()
            && let Some(cursor) = self.scrubber.cursor_x(&canvas, &self.film)
        {
            tile.draw(brush, art::tile_at(&canvas, tile, cursor));
        }
        self.upnext.draw(
            brush,
            self.focus.focused_stop() == Some(Stop::Next),
            &self.bridge(&canvas),
            now,
        );
    }

    /// Draws the upper layer.
    ///
    /// This layer has the parts that fade on their own timers, the up-next
    /// column, the skip control, and the volume row, and the track panel.
    /// The panel's darkening is in the lower layer, under the scrubber's
    /// and the strip's words, and the panel does not dim the film, because
    /// the viewer judges a delay by watching the film.
    fn paint_above(&self, brush: &mut Brush<'_>, now: &ZonedDateTime) {
        let canvas = brush.canvas();
        self.strip
            .draw_panel(brush, &self.presentation, &self.film, self.tick);
        // The up-next card appears at `rise` with the OSD down, and a
        // selected card fades out while the next work starts, with the
        // OSD up or down. So it has its own fade outside the OSD. While
        // the compositor has shrunk the film, the offer draws as a
        // column beside it in this layer, and the column draws as
        // focused when the OSD's focus is on the offer.
        let focused = self.focus.card_takes_select(&self.upnext, &self.skip)
            || (self.upnext.stepped_back() && self.focus.focused_stop() == Some(Stop::Next));
        self.upnext.draw_outside(
            brush,
            self.focus.visible(),
            focused,
            &self.bridge(&canvas),
            now,
        );
        // The skip control has its own fade too, so it stays on the
        // bare video for the whole intro or recap.
        self.skip.draw_outside(brush, self.focus.visible());
        // A volume change shows only the volume row, so the row has
        // its own fade and hide timer. It draws last, on top of the
        // track panel.
        self.volume.draw(brush);
    }

    pub fn view(&self) -> Element<'_, Message> {
        let layer = |part| {
            canvas(Layer {
                display: self,
                part,
            })
            .width(Length::Fill)
            .height(Length::Fill)
        };
        iced::widget::stack![layer(Part::Below), layer(Part::Above)].into()
    }

    /// The frame tick runs only while a fade is moving, so a display at
    /// rest runs no frame timer.
    pub fn subscription(&self) -> Subscription<Message> {
        let mpv = Subscription::run_with(self.wire.clone(), |wire| {
            let ipc = Ipc::at(&wire.path);
            let commands = wire.commands.clone();
            let notes = wire.notes.clone();
            let mut pace = Pace::new(wire.watching.clone());
            iced::stream::channel(EVENT_QUEUE, async move |mut output| {
                use iced::futures::SinkExt;

                let (sender, mut events) = tokio::sync::mpsc::channel(EVENT_QUEUE);
                tokio::spawn(async move { ipc.serve(sender, &commands, &notes).await });
                while let Some(event) = events.recv().await {
                    if !pace.forwards(&event) {
                        continue;
                    }
                    if output.send(Message::Mpv(event)).await.is_err() {
                        return;
                    }
                }
            })
        });
        let mut running = vec![
            mpv,
            iced::window::resize_events().map(|(_, size)| Message::Resized(size)),
            iced::event::listen_with(|event, _, _| match event {
                iced::Event::Window(iced::window::Event::Rescaled(density)) => {
                    Some(Message::Rescaled(density))
                }
                _ => None,
            }),
            iced::window::close_requests().map(|_| Message::Close),
        ];
        if self.fading() {
            running.push(iced::time::every(theme::FADE_TICK).map(|_| Message::Tick));
        }
        // A paused film sends no position, and the display stays up for
        // the whole pause, so the clock and the end times need their own
        // timer while the OSD or the up-next card shows.
        if (self.focus.visible() || self.upnext.draws_outside(false)) && !self.detached {
            running.push(Subscription::run(minutes));
        }
        Subscription::batch(running)
    }

    /// Whether a layer has anything to draw. With the OSD down, the
    /// lower layer draws only the music cover, and the upper layer draws
    /// the elements that have their own fades. After mpv's socket closes
    /// neither layer draws.
    fn draws(&self, part: Part) -> bool {
        if self.detached {
            return false;
        }
        let fade = self.focus.fade().value();
        let visible = self.focus.visible();
        match part {
            Part::Below => fade > 0.0 || self.art.cover().is_some(),
            Part::Above => {
                fade > 0.0
                    || self.upnext.draws_outside(visible)
                    || self.skip.draws_outside(visible)
                    || self.volume.showing()
            }
        }
    }

    /// Whether any fade is moving. After mpv's socket closes nothing
    /// draws, so nothing ticks.
    fn fading(&self) -> bool {
        !self.detached
            && (self.focus.fade().running()
                || self.upnext.fade().running()
                || self.upnext.column().running()
                || self.skip.fade().running()
                || self.volume.fade().running()
                || self.strip.moving(self.tick))
    }
}

/// Reads the spans file on tokio's blocking pool, so a slow read never
/// delays a frame. A read that panics produces a `Cast` message with no
/// spans.
fn read_spans(item: u64, path: String) -> Task<Message> {
    Task::future(async move { tokio::task::spawn_blocking(move || Spans::read(&path)).await })
        .then(move |spans| Task::done(Message::Cast(item, spans.ok().flatten())))
}

/// Runs each job on tokio's blocking pool, because a file read, an
/// HTTPS fetch, and a sprite-sheet decode block, and no frame may wait
/// for one. When a job panics, the frame loop still receives an empty
/// answer for it, so `Art` stops waiting for that picture, and a log
/// line names the reference that failed.
fn decode(jobs: Vec<Job>) -> Task<Message> {
    Task::batch(jobs.into_iter().map(|job| {
        let failed = job.clone();
        Task::future(async move { tokio::task::spawn_blocking(move || job.run()).await }).then(
            move |answer| {
                Task::done(Message::Art(match answer {
                    Ok(answer) => answer,
                    Err(_) => {
                        eprintln!(
                            "media-display: decoding {} panicked, so it draws no picture",
                            failed.reference()
                        );
                        failed.clone().empty()
                    }
                }))
            },
        )
    }))
}

/// What the mpv socket subscription needs. iced identifies a
/// subscription by its hash, and only `path` is hashed, so the
/// subscription keeps its socket across updates instead of connecting
/// again.
#[derive(Debug, Clone)]
pub struct Wire {
    path: Arc<Path>,
    commands: broadcast::Sender<String>,
    notes: Notes,
    watching: watch::Receiver<Watching>,
}

impl Hash for Wire {
    fn hash<H: Hasher>(&self, state: &mut H) {
        self.path.hash(state);
    }
}

/// What `Pace` reads to decide whether a position changes the screen.
#[derive(Debug, Clone, Copy, PartialEq)]
struct Watching {
    canvas: Canvas,
    visible: bool,
}

/// Drops the `time-pos` changes that change nothing on screen, before
/// they reach the frame loop.
///
/// mpv sends `time-pos` on every video frame, and iced presents a frame
/// for every batch of messages. A position that moves no pixel would
/// still cost a surface commit and a recomposite of the whole screen.
/// So `Pace` forwards a position only when the whole second on the time
/// label changes or the playhead moves an output pixel. While the OSD is
/// down only the second counts, which still updates the film's position
/// once a second for the next summon.
struct Pace {
    watching: watch::Receiver<Watching>,
    duration: Option<f64>,
    last: Option<Signature>,
}

impl Pace {
    fn new(watching: watch::Receiver<Watching>) -> Self {
        Self {
            watching,
            duration: None,
            last: None,
        }
    }

    /// Whether to forward the event to the frame loop.
    fn forwards(&mut self, event: &ipc::Event) -> bool {
        let ipc::Event::Property { name, value } = event else {
            return true;
        };
        if name == "duration" {
            self.duration = value.as_f64();
        }
        if name != "time-pos" {
            return true;
        }
        let watching = *self.watching.borrow();
        let signature = Signature {
            pixel: self
                .duration
                .filter(|duration| *duration > 0.0)
                .filter(|_| watching.visible)
                .zip(value.as_f64())
                .map(|(duration, at)| crate::scrubber::pixel_at(&watching.canvas, duration, at)),
            second: (value.as_f64().unwrap_or(0.0) + 0.5).floor() as i64,
        };
        if self.last == Some(signature) {
            return false;
        }
        self.last = Some(signature);
        true
    }
}

/// Gives `upnext` the offer's picture in canvas units. `upnext` lays out
/// the card in canvas units, and `Art` stores pictures in output pixels.
struct Bridge<'a> {
    art: &'a Art,
    canvas: Canvas,
}

impl upnext::Art for Bridge<'_> {
    fn next(&self) -> Option<Size> {
        let next = self.art.next()?;
        Some(Size::new(
            self.canvas.to_canvas(next.width as f32),
            self.canvas.to_canvas(next.height as f32),
        ))
    }

    fn draw(&self, brush: &mut Brush<'_>, bounds: Rectangle) {
        let Some(next) = self.art.next() else {
            return;
        };
        next.draw(brush, bounds.position());
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Part {
    Below,
    Above,
}

/// One layer of the display as an iced canvas program.
struct Layer<'a> {
    display: &'a Display,
    part: Part,
}

impl canvas::Program<Message> for Layer<'_> {
    type State = ();

    fn draw(
        &self,
        _state: &(),
        renderer: &Renderer,
        _theme: &Theme,
        bounds: Rectangle,
        _cursor: iced::mouse::Cursor,
    ) -> Vec<canvas::Geometry> {
        // The canvas comes from the surface size that the window
        // reports, not from a constant, because the compositor sets the
        // region that a `Layout` gives this pod. `run` reads the size
        // before the first frame, so the first decode of an item already
        // uses the real size.
        let canvas = self.display.canvas;

        if !self.display.draws(self.part) {
            return Vec::new();
        }
        let fade = self.display.focus.fade().value();

        let now = clock::now();
        let cache = match self.part {
            Part::Below => &self.display.below,
            Part::Above => &self.display.above,
        };
        vec![cache.draw(renderer, bounds.size(), |frame| {
            frame.scale(canvas.scale());
            let mut brush = Brush::new(frame, canvas, fade, self.display.density);
            match self.part {
                Part::Below => self.display.paint_below(&mut brush, &now),
                Part::Above => self.display.paint_above(&mut brush, &now),
            }
            brush.finish();
        })]
    }
}

/// A stream that yields `Minute` each time the wall-clock minute
/// changes. Each wait runs to the next minute from the current time,
/// because a fixed 60-second interval would lag the clock by up to a
/// minute, and a slow frame would shift every later tick.
fn minutes() -> impl iced::futures::Stream<Item = Message> {
    iced::stream::channel(1, async |mut output| {
        use iced::futures::SinkExt;

        loop {
            tokio::time::sleep(clock::until_next_minute(&clock::now())).await;
            if output.send(Message::Minute).await.is_err() {
                return;
            }
        }
    })
}

/// Opens the window and runs the frame loop until the window closes.
/// `main` first waits for mpv to report a position, so this window is
/// created after mpv's.
pub fn run(ipc: Ipc) -> iced::Result {
    iced::application(
        // At startup the display reads the window's size and scale
        // factor once, so it has the real surface before its first
        // frame.
        //
        // The display handles no pointer input, so mouse passthrough
        // gives its surface an empty input region, and clicks reach
        // mpv's surface under it. The compositor's shell gives the
        // keyboard to the surface that receives a click. Without
        // passthrough the display's window would take every click, and
        // mpv would never receive a key.
        //
        // The arranger binds on the window's own Wayland connection, on
        // winit's thread, and sends its events to the frame loop through
        // a channel.
        move || {
            let (sender, events) = tokio::sync::mpsc::unbounded_channel();
            let events = iced::futures::stream::unfold(events, |mut events| async move {
                events.recv().await.map(|event| (event, events))
            });
            (
                Display::new(ipc.clone()),
                Task::batch([
                    iced::window::oldest().and_then(|id| {
                        iced::window::enable_mouse_passthrough(id)
                            .chain(iced::window::size(id).map(Message::Resized))
                            .chain(iced::window::scale_factor(id).map(Message::Rescaled))
                    }),
                    iced::window::oldest().and_then(move |id| {
                        let sender = sender.clone();
                        iced::window::run(id, move |window| link::attach(window, sender))
                            .map(Message::Attached)
                    }),
                    Task::run(events, Message::Arranger),
                ]),
            )
        },
        Display::update,
        Display::view,
    )
    .subscription(Display::subscription)
    .style(|_display, _theme| iced::theme::Style {
        background_color: Color::TRANSPARENT,
        text_color: theme::color::text(),
    })
    .window(settings())
    // A close request arrives as `Message::Close`. The handler closes
    // the arranger's link first and then exits, so the link's thread
    // never reads a connection that winit already closed.
    .exit_on_close_request(false)
    // The fonts are embedded in the binary, so every `liken` display
    // draws the same typeface whatever fonts the image has installed.
    .font(liken_iced::font::REGULAR_OTF)
    .font(liken_iced::font::ITALIC_OTF)
    .default_font(liken_iced::font::REGULAR)
    .title("liken display")
    // `Brush` rasterizes every shape into a picture with antialiased
    // edges, so the frame has no mesh to multisample. With the
    // toolkit's antialiasing on, iced renders each layer that has a
    // mesh into a multisampled target as large as the window, which
    // here is the whole screen.
    .antialiasing(false)
    .run()
}

/// The compositor reads `application_id` as the Wayland app_id, so an
/// arrangement can place the display's surface apart from mpv's.
fn settings() -> iced::window::Settings {
    iced::window::Settings {
        size: Size::new(theme::CANVAS_WIDTH, theme::CANVAS_HEIGHT),
        decorations: false,
        transparent: true,
        resizable: false,
        platform_specific: iced::window::settings::PlatformSpecific {
            application_id: arrangement::OVERLAY.to_string(),
            ..Default::default()
        },
        ..Default::default()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn the_window_has_the_overlay_app_id() {
        assert_eq!(
            settings().platform_specific.application_id,
            "liken-media-overlay"
        );
    }

    pub(super) fn message(word: &str) -> Message {
        Message::Mpv(ipc::Event::Message(vec![word.to_string()]))
    }

    pub(super) fn property(name: &str, value: serde_json::Value) -> Message {
        Message::Mpv(ipc::Event::Property {
            name: name.to_string(),
            value,
        })
    }

    fn pause(paused: bool) -> Message {
        property("pause", json!(paused))
    }

    /// Creates a display for a 6000-second film at 1200 seconds, with
    /// chapters and two audio tracks. The receiver reads the commands
    /// that the display sends to mpv.
    pub(super) fn display() -> (Display, broadcast::Receiver<String>) {
        let mut display = Display::new(Ipc::default());
        let commands = display.wire.commands.subscribe();
        for (name, value) in [
            ("duration", json!(6000.0)),
            ("time-pos", json!(1200.0)),
            ("chapter", json!(1)),
            (
                "chapter-list",
                json!([{ "time": 0.0 }, { "time": 1500.0 }, { "time": 4500.0 }]),
            ),
            (
                "track-list",
                json!([
                    { "id": 1, "type": "audio", "lang": "eng", "selected": true },
                    { "id": 2, "type": "audio", "lang": "fra" },
                ]),
            ),
        ] {
            let _ = display.update(property(name, value));
        }
        (display, commands)
    }

    /// Ticks until every fade has finished moving.
    pub(super) fn settle(display: &mut Display) {
        while display.fading() {
            let _ = display.update(Message::Tick);
        }
    }

    pub(super) fn sent(commands: &mut broadcast::Receiver<String>) -> Vec<String> {
        let mut lines = Vec::new();
        while let Ok(line) = commands.try_recv() {
            lines.push(line.trim_end().to_string());
        }
        lines
    }

    #[test]
    fn the_display_starts_clear() {
        let display = Display::new(Ipc::default());
        assert_eq!(display.focus.fade().value(), 0.0);
        assert!(!display.focus.visible());
    }

    #[test]
    fn the_up_next_card_stays_longer_than_the_display() {
        let display = Display::new(Ipc::default());
        assert_eq!(display.idle.wait, theme::IDLE_HIDE);
        assert_eq!(display.row.wait, theme::IDLE_HIDE);
        assert_eq!(display.card.wait, theme::CARD_HIDE);
    }

    #[tokio::test]
    async fn a_summon_or_a_direction_raises_the_display() {
        for word in ["summon", "up", "down", "left", "right"] {
            let (mut display, _) = display();
            let _ = display.update(message(word));
            settle(&mut display);
            assert!(display.focus.visible(), "{word} raises the display");
            assert_eq!(display.focus.fade().value(), 1.0);
        }
    }

    #[tokio::test]
    async fn an_unknown_or_empty_message_raises_nothing() {
        let (mut display, _) = display();
        let _ = display.update(message("liken-art"));
        let _ = display.update(Message::Mpv(ipc::Event::Message(Vec::new())));
        assert!(!display.focus.visible());
        assert_eq!(display.focus.fade().value(), 0.0);
    }

    #[tokio::test]
    async fn the_idle_timer_hides_the_display() {
        let (mut display, _) = display();
        let _ = display.update(message("summon"));
        settle(&mut display);
        let armed = display.idle.armed.expect("a summon arms an idle window");

        let _ = display.update(Message::Hide(armed));
        settle(&mut display);
        assert!(!display.focus.visible());
        assert_eq!(display.focus.fade().value(), 0.0);
        assert_eq!(display.idle.armed, None);
    }

    /// The timer that a summon armed fires while the audio panel is
    /// open, and the display stays up. Closing the panel with back
    /// arms a new timer, which hides the display.
    #[tokio::test]
    async fn the_idle_timer_leaves_an_open_panel_up() {
        let (mut display, _) = display();
        let _ = display.update(message("summon"));
        let summoned = display.idle.armed.expect("a summon arms an idle window");
        for word in ["down", "down", "select"] {
            let _ = display.update(message(word));
        }
        assert!(display.strip.capturing().is_some());

        let _ = display.update(Message::Hide(summoned));
        settle(&mut display);
        assert!(display.strip.capturing().is_some());
        assert!(display.focus.visible());
        assert_eq!(display.idle.armed, None);

        let _ = display.update(message("back"));
        let closed = display.idle.armed.expect("closing the panel arms a window");
        let _ = display.update(Message::Hide(closed));
        settle(&mut display);
        assert!(!display.focus.visible());
    }

    /// A delay that mpv reports while the audio panel is open starts the
    /// mark's glide, the display asks for frames while it moves, and the
    /// frame ticks end it. A delay with the panel closed starts nothing.
    #[tokio::test]
    async fn a_new_delay_glides_the_mark_for_a_few_frames() {
        let (mut display, _) = display();
        let _ = display.update(property("audio-delay", json!(0.05)));
        assert!(
            !display.fading(),
            "a delay with the panel closed asks for frames"
        );

        for word in ["summon", "down", "down", "select"] {
            let _ = display.update(message(word));
        }
        settle(&mut display);
        assert!(display.strip.capturing().is_some());
        let _ = display.update(property("audio-delay", json!(0.1)));
        assert!(display.fading(), "the mark does not glide, want it to");
        for _ in 0..crate::motion::GLIDE {
            let _ = display.update(Message::Tick);
        }
        assert!(!display.fading(), "the mark still glides after its length");
    }

    /// A `track-list` with no track of the open panel's kind closes the
    /// panel, and the hide timer that the panel held off starts again.
    #[tokio::test]
    async fn a_panel_whose_tracks_leave_closes_and_the_display_can_hide() {
        let (mut display, _) = display();
        for word in ["summon", "down", "down", "select"] {
            let _ = display.update(message(word));
        }
        assert!(display.strip.capturing().is_some());
        assert_eq!(display.idle.armed, None);

        let _ = display.update(property(
            "track-list",
            json!([{ "id": 1, "type": "sub", "lang": "eng", "selected": true }]),
        ));
        assert_eq!(display.strip.capturing(), None);
        assert!(
            display.idle.armed.is_some(),
            "closing the panel arms no hide timer"
        );
    }

    #[tokio::test]
    async fn a_stale_idle_timer_hides_nothing() {
        let (mut display, _) = display();
        let _ = display.update(message("summon"));
        let first = display.idle.armed.expect("a summon arms an idle window");
        let _ = display.update(message("up"));
        settle(&mut display);

        assert_ne!(display.idle.armed, Some(first));
        let _ = display.update(Message::Hide(first));
        assert!(display.focus.visible());
        assert_eq!(display.focus.fade().value(), 1.0);
    }

    /// mpv reports `pause` once when the display starts observing it.
    /// The first report is only recorded, so a film that loads paused
    /// starts with the display hidden.
    #[tokio::test]
    async fn a_film_that_loads_paused_starts_hidden() {
        let (mut display, _) = display();
        let _ = display.update(pause(true));
        settle(&mut display);
        assert!(!display.focus.visible());
        assert_eq!(display.focus.fade().value(), 0.0);
        assert!(display.focus.paused());
    }

    #[tokio::test]
    async fn a_pause_raises_the_display_and_arms_no_hide_timer() {
        let (mut display, _) = display();
        let _ = display.update(pause(false));
        let _ = display.update(pause(true));
        settle(&mut display);
        assert!(display.focus.visible());
        assert_eq!(display.focus.fade().value(), 1.0);
        assert_eq!(display.idle.armed, None);
    }

    #[tokio::test]
    async fn a_resume_dismisses_the_display_at_once() {
        let (mut display, _) = display();
        let _ = display.update(pause(false));
        let _ = display.update(pause(true));
        settle(&mut display);

        let _ = display.update(pause(false));
        settle(&mut display);
        assert!(!display.focus.visible());
        assert_eq!(display.focus.fade().value(), 0.0);
    }

    #[tokio::test]
    async fn a_summon_during_a_fade_out_reverses_it() {
        let (mut display, _) = display();
        let _ = display.update(message("summon"));
        settle(&mut display);
        let armed = display.idle.armed.expect("a summon arms an idle window");
        let _ = display.update(Message::Hide(armed));
        for _ in 0..18 {
            let _ = display.update(Message::Tick);
        }
        let standing = display.focus.fade().value();
        assert!(standing > 0.0 && standing < 1.0);

        let _ = display.update(message("up"));
        assert!(display.focus.fade().value() >= standing);
        settle(&mut display);
        assert_eq!(display.focus.fade().value(), 1.0);
    }

    #[tokio::test]
    async fn a_resize_resolves_the_scrims_for_the_new_surface() {
        let (mut display, _) = display();
        let rows = |display: &Display| {
            display
                .scrims
                .bottom
                .as_ref()
                .map_or(0, |picture| picture.rows().len())
        };
        let before = rows(&display);
        assert!(before > 0);

        let _ = display.update(Message::Resized(Size::new(1280.0, 720.0)));
        assert_eq!(display.canvas, Canvas::for_output(Size::new(1280.0, 720.0)));
        assert!(rows(&display) < before);
    }

    /// At scale factor 2 each scrim has twice as many rows, so each row
    /// still covers one output row. A factor of 0 changes nothing.
    #[tokio::test]
    async fn a_rescale_resolves_the_scrims_at_the_new_density() {
        let (mut display, _) = display();
        let rows = |display: &Display| {
            display
                .scrims
                .top
                .as_ref()
                .map_or(0, |picture| picture.rows().len())
        };
        let single = rows(&display);

        let _ = display.update(Message::Rescaled(2.0));
        assert_eq!(rows(&display), single * 2);

        let _ = display.update(Message::Rescaled(0.0));
        assert_eq!(rows(&display), single * 2);
    }

    /// A position inside the same second and the same output pixel
    /// keeps the signature, so nothing redraws. A position in the next
    /// second changes it.
    #[tokio::test]
    async fn a_position_in_the_same_second_keeps_the_signature() {
        let (mut display, _) = display();
        let _ = display.update(message("summon"));
        settle(&mut display);

        let _ = display.update(property("time-pos", json!(1200.0)));
        let first = display.signature.expect("a position carries a signature");
        let _ = display.update(property("time-pos", json!(1200.0417)));
        assert_eq!(display.signature, Some(first));

        let _ = display.update(property("time-pos", json!(1200.6)));
        assert_ne!(display.signature, Some(first));
        assert_eq!(display.film.position, Some(1200.6));
    }

    #[tokio::test]
    async fn a_position_on_another_pixel_changes_the_signature_pixel() {
        let (mut display, _) = display();
        let _ = display.update(message("summon"));
        settle(&mut display);
        let _ = display.update(property("time-pos", json!(1200.0)));
        let first = display.signature.expect("a position carries a signature");
        let _ = display.update(property("time-pos", json!(1204.0)));
        let moved = display.signature.expect("a moved position carries one too");
        assert_ne!(moved.pixel, first.pixel);
    }

    #[tokio::test]
    async fn a_film_with_no_duration_has_no_playhead_pixel() {
        let mut display = Display::new(Ipc::default());
        let _ = display.update(message("summon"));
        settle(&mut display);
        let _ = display.update(property("time-pos", json!(12.5)));
        assert_eq!(
            display.signature.map(|signature| signature.pixel),
            Some(None)
        );
    }

    fn words(words: &[&str]) -> Message {
        Message::Mpv(ipc::Event::Message(
            words.iter().map(|word| word.to_string()).collect(),
        ))
    }

    #[tokio::test]
    async fn a_press_writes_its_log_line_when_mpv_responds() {
        let (mut display, mut commands) = display();
        let _ = display.update(message("summon"));
        let _ = display.update(message("right"));
        let _ = display.update(message("select"));
        sent(&mut commands);

        assert_eq!(
            record::answered(&display.wire.notes, 1000, "success").as_deref(),
            Some(
                "media-display: select pressed: seek to 0:20:05, sent [\"seek\",1205.0,\"absolute+exact\"] to mpv, mpv answered success"
            )
        );
        assert!(display.logged.is_empty());
    }

    /// Each repeat of a held arrow sends a command, and one log line
    /// covers the whole hold. The line is written when the display
    /// hides, and no repeat stores a note.
    #[tokio::test]
    async fn a_held_arrow_writes_one_line_when_the_display_hides() {
        let (mut display, mut commands) = display();
        let _ = display.update(message("summon"));
        let _ = display.update(message("down"));
        let _ = display.update(message("right"));
        for _ in 0..3 {
            let _ = display.update(words(&["right", "repeat"]));
        }
        assert_eq!(sent(&mut commands).len(), 4);
        assert_eq!(display.wire.notes.lock().expect("the notes").len(), 1);
        assert!(display.logged.is_empty());

        let armed = display.idle.armed.expect("the press armed the idle window");
        let _ = display.update(Message::Hide(armed));

        assert_eq!(display.logged.len(), 1);
        assert!(
            display.logged[0].starts_with("media-display: right held for 3 repeats, last sent [")
        );
    }

    #[tokio::test]
    async fn a_summon_ends_the_hold_before_it() {
        let (mut display, _commands) = display();
        let _ = display.update(message("summon"));
        let _ = display.update(message("down"));
        let _ = display.update(words(&["right", "repeat"]));

        let _ = display.update(message("summon"));

        assert_eq!(display.logged.len(), 1);
    }

    #[tokio::test]
    async fn a_press_with_a_command_sends_it_to_mpv() {
        let (mut display, mut commands) = display();
        let _ = display.update(message("summon"));
        assert!(sent(&mut commands).is_empty());

        let _ = display.update(message("right"));
        assert!(sent(&mut commands).is_empty());
        let _ = display.update(message("select"));
        assert_eq!(
            sent(&mut commands),
            vec!["{\"command\":[\"seek\",1205.0,\"absolute+exact\"],\"request_id\":1000}"]
        );

        let _ = display.update(message("select"));
        assert_eq!(
            sent(&mut commands),
            vec!["{\"command\":[\"no-osd\",\"cycle\",\"pause\"],\"request_id\":1001}"]
        );
    }

    #[tokio::test]
    async fn a_presentation_block_sets_the_item_and_the_next_replaces_it() {
        let (mut display, _) = display();
        let _ = display.update(Message::Mpv(ipc::Event::Message(vec![
            "presentation".to_string(),
            r#"{"title":"A Film","year":2014}"#.to_string(),
        ])));
        assert_eq!(
            display.presentation.title(&display.film).as_deref(),
            Some("A Film")
        );
        assert!(!display.focus.visible());

        let _ = display.update(Message::Mpv(ipc::Event::Message(vec![
            "presentation".to_string(),
        ])));
        assert_eq!(display.presentation.title(&display.film), None);
    }

    fn present(block: &str) -> Message {
        Message::Mpv(ipc::Event::Message(vec![
            "presentation".to_string(),
            block.to_string(),
        ]))
    }

    fn picture(name: &str, width: u32, height: u32) -> String {
        let path = std::env::temp_dir().join(format!("media-display-window-{name}.png"));
        let mut bytes = Vec::new();
        image::DynamicImage::ImageRgba8(image::RgbaImage::from_pixel(
            width,
            height,
            image::Rgba([255, 255, 255, 255]),
        ))
        .write_to(
            &mut std::io::Cursor::new(&mut bytes),
            image::ImageFormat::Png,
        )
        .expect("a picture the display can decode");
        std::fs::write(&path, bytes).expect("a file to read");
        path.to_string_lossy().to_string()
    }

    /// Handles one message and returns the decode jobs that the new
    /// state needs. A test runs them directly instead of on the
    /// blocking pool.
    fn jobs_after(display: &mut Display, message: Message) -> Vec<Job> {
        let _ = display.step(message);
        display.sync_art()
    }

    /// Runs a decode job, gives its answer to the display, and returns
    /// the jobs that the answer starts.
    fn run_job(display: &mut Display, job: Job) -> Vec<Job> {
        let (changed, more) = display.art.on_answer(job.run());
        if changed {
            display.redraw();
        }
        more
    }

    #[tokio::test]
    async fn an_item_decodes_its_logo_once_and_the_result_sets_its_height() {
        let (mut display, _) = display();
        let logo = picture("logo", 800, 128);
        let jobs = jobs_after(
            &mut display,
            present(&format!(r#"{{"title":"A Film","logo":"{logo}"}}"#)),
        );
        assert_eq!(jobs.len(), 1);

        assert!(run_job(&mut display, jobs.into_iter().next().unwrap()).is_empty());
        let drawn = display.art.logo().expect("the answer carries a picture");
        assert_eq!(drawn.canvas_height(&display.canvas), 110.0);
        assert!(jobs_after(&mut display, property("time-pos", json!(12.0))).is_empty());
        assert_eq!(
            jobs_after(&mut display, present(&format!(r#"{{"logo":"{logo}"}}"#))).len(),
            1
        );
    }

    /// A music item whose block names no cover waits for mpv's `path`
    /// before it requests the cover from the file. The display keeps the
    /// first answer, so a request made before the path is known would
    /// leave the item with no cover. A block that names its cover
    /// decodes it at once, and the cover draws with the OSD down.
    #[tokio::test]
    async fn a_music_item_decodes_its_cover_and_keeps_it_with_the_display_down() {
        let (mut display, _) = display();
        assert!(
            jobs_after(
                &mut display,
                present(r#"{"type":"music","title":"A Record"}"#)
            )
            .is_empty()
        );

        let jobs = jobs_after(&mut display, property("path", json!("/media/track.flac")));
        assert_eq!(jobs.len(), 1);
        assert!(run_job(&mut display, jobs.into_iter().next().unwrap()).is_empty());
        assert!(display.art.cover().is_none());
        assert!(jobs_after(&mut display, property("time-pos", json!(12.0))).is_empty());

        let cover = picture("cover", 532, 532);
        let jobs = jobs_after(
            &mut display,
            present(&format!(r#"{{"type":"music","art":"{cover}"}}"#)),
        );
        assert_eq!(jobs.len(), 1);
        let _ = run_job(&mut display, jobs.into_iter().next().unwrap());
        assert!(display.art.cover().is_some());
        assert!(!display.focus.visible());
    }

    /// A pause inside a span requests the portraits of the people that
    /// the spans file names there, and a resume hides the cast row.
    #[tokio::test]
    async fn a_pause_in_a_span_decodes_the_portraits_of_its_people() {
        let (mut display, _) = display();
        let _ = jobs_after(
            &mut display,
            present(r#"{"appearances":"/art/spans.json","contributors":"/art/.contributors"}"#),
        );
        let spans = Spans::parse(
            r#"{"format":"liken.sh/appearances/spans/v1",
                "people":{"ja/jane":{"name":"Jane","portrait":"ja/jane/headshot.jpg"}},
                "spans":[{"start":10.0,"end":20.0,"people":["ja/jane"]}]}"#,
        );
        let _ = jobs_after(&mut display, Message::Cast(1, spans));
        let _ = jobs_after(&mut display, property("time-pos", json!(12.0)));
        let _ = jobs_after(&mut display, pause(false));
        let jobs = jobs_after(&mut display, pause(true));
        assert_eq!(
            jobs.iter().map(Job::reference).collect::<Vec<_>>(),
            vec!["/art/.contributors/ja/jane/headshot.jpg"]
        );
        let _ = jobs_after(&mut display, pause(false));
        assert!(!display.cast_showing());
    }

    #[tokio::test]
    async fn only_a_scan_on_a_trickplay_item_decodes_a_tile() {
        let (mut display, _) = display();
        let _ = jobs_after(&mut display, present(r#"{"title":"A Film"}"#));
        let _ = jobs_after(&mut display, message("summon"));
        assert!(jobs_after(&mut display, message("right")).is_empty());
        assert!(display.previewing().is_none());

        let jobs = jobs_after(
            &mut display,
            present(r#"{"title":"A Film","trickplay":"/art/tiles"}"#),
        );
        assert!(display.previewing().is_some());
        assert_eq!(jobs.len(), 1);
        // The directory has no sprite sheet, so the decode returns no
        // tile and starts no further job.
        assert!(run_job(&mut display, jobs.into_iter().next().unwrap()).is_empty());
        assert!(display.art.tile().is_none());
    }

    #[tokio::test]
    async fn an_open_panel_hides_the_logo_and_the_thumbnail() {
        let (mut display, _) = display();
        let _ = display.update(present(r#"{"title":"A Film","trickplay":"/art/tiles"}"#));
        let _ = display.update(message("summon"));
        let _ = display.update(message("right"));
        assert!(display.showing());
        assert!(display.previewing().is_some());

        let _ = display.update(message("down"));
        let _ = display.update(message("down"));
        let _ = display.update(message("select"));
        assert!(!display.showing());
        assert!(display.previewing().is_none());
    }

    pub(super) const OFFER: &str = r#"{"reason":"Next in Harbor Lights",
        "title":"E05 \u00b7 The Long Tide",
        "detail":"Harbor Lights \u00b7 S02 \u00b7 45 min"}"#;

    pub(super) fn block(word: &str, text: &str) -> Message {
        Message::Mpv(ipc::Event::Message(vec![
            word.to_string(),
            text.to_string(),
        ]))
    }

    #[tokio::test]
    async fn a_next_message_stores_the_offer_and_raises_nothing() {
        let (mut display, _) = display();
        let _ = display.update(block("next", OFFER));
        assert!(display.upnext.available());
        assert!(!display.focus.visible());
        assert_eq!(display.focus.fade().value(), 0.0);

        let _ = display.update(block("next", "{}"));
        assert!(!display.upnext.available());
    }

    #[tokio::test]
    async fn the_position_raises_the_card_with_the_display_down() {
        let (mut display, _) = display();
        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("time-pos", json!(3000.0)));
        assert!(!display.upnext.fade().running());
        assert_eq!(display.card.armed, None);

        let _ = display.update(property("time-pos", json!(5940.0)));
        settle(&mut display);
        assert!(display.upnext.draws_outside(false));
        assert!(!display.focus.visible());
        let armed = display.card.armed.expect("the rise arms a hide window");

        let _ = display.update(Message::HideCard(armed));
        settle(&mut display);
        assert!(!display.upnext.draws_outside(false));
        assert_eq!(display.card.armed, None);
    }

    #[tokio::test]
    async fn a_stale_card_timer_hides_nothing() {
        let (mut display, _) = display();
        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("time-pos", json!(5940.0)));
        settle(&mut display);
        let armed = display.card.armed.expect("the rise arms a hide window");

        let _ = display.update(Message::HideCard(armed + 1));
        settle(&mut display);
        assert!(display.upnext.draws_outside(false));
    }

    /// What a select on the card sends to mpv: a `liken-next` script
    /// message that requests the next work, and a pause that stops the
    /// sound of the credits.
    pub(super) const TAKE: [&str; 2] = [
        "{\"command\":[\"script-message\",\"liken-next\"],\"request_id\":0}",
        "{\"command\":[\"no-osd\",\"set\",\"pause\",\"yes\"],\"request_id\":1000}",
    ];

    #[tokio::test]
    async fn a_select_on_the_card_asks_for_the_next_work() {
        let (mut display, mut commands) = display();
        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("time-pos", json!(5940.0)));
        let _ = display.update(message("summon"));
        let _ = display.update(message("up"));
        assert!(sent(&mut commands).is_empty());

        let _ = display.update(message("select"));
        assert_eq!(sent(&mut commands), TAKE);
        assert!(display.upnext.waiting());

        let _ = display.update(message("select"));
        assert!(sent(&mut commands).is_empty());
        let _ = display.update(message("back"));
        assert_eq!(
            sent(&mut commands),
            vec!["{\"command\":[\"script-message\",\"liken-exit\"],\"request_id\":0}"]
        );
    }

    #[tokio::test]
    async fn a_select_with_the_display_down_takes_the_showing_card() {
        let (mut display, mut commands) = display();
        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("time-pos", json!(5940.0)));
        settle(&mut display);

        let _ = display.update(message("select"));
        assert_eq!(sent(&mut commands), TAKE);
        assert!(display.upnext.waiting());
        assert!(!display.focus.visible());

        assert!(display.fading(), "the card fades out on the hand-off");
        settle(&mut display);
        assert!(!display.upnext.draws_outside(false));
        assert!(display.committed.is_empty(), "no arranger, no hand-off");
    }

    #[tokio::test]
    async fn a_select_after_the_card_leaves_pauses() {
        let (mut display, mut commands) = display();
        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("time-pos", json!(5940.0)));
        settle(&mut display);
        let armed = display.card.armed.expect("the rise arms a hide window");
        let _ = display.update(Message::HideCard(armed));
        settle(&mut display);

        let _ = display.update(message("select"));
        assert_eq!(
            sent(&mut commands),
            vec!["{\"command\":[\"no-osd\",\"cycle\",\"pause\"],\"request_id\":1000}"]
        );
        assert!(!display.upnext.waiting());
    }

    /// A move to another playlist entry drops the offer. The first
    /// `playlist-pos` report does not.
    #[tokio::test]
    async fn a_new_item_drops_the_offer() {
        let (mut display, _) = display();
        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("playlist-pos", json!(0)));
        assert!(display.upnext.available());

        let _ = display.update(property("playlist-pos", json!(1)));
        assert!(!display.upnext.available());
    }

    /// An item with an intro around the fixture's playhead at 1200
    /// seconds, and credits near the end.
    const MARKED: &str = r#"{"title":"A Film","marks":[
        {"kind":"intro","end":1250.0,"source":"theintrodb"},
        {"kind":"intro","start":1100.0,"end":1250.0},
        {"kind":"credits","start":5400.0,"end":5900.0}]}"#;

    /// With the display down, the skip control shows for the first 10
    /// seconds of the intro. The two candidates merge into a span that
    /// starts at 550 seconds, the median of 0 and 1100, so the control
    /// shows alone from 550 to 560 seconds, and then only as a stop of
    /// the display until the intro ends.
    #[tokio::test]
    async fn the_skip_control_shows_alone_for_the_start_of_the_intro() {
        let (mut display, _) = display();
        let _ = display.update(present(MARKED));
        let _ = display.update(property("time-pos", json!(550.0)));
        settle(&mut display);
        assert!(display.skip.available());
        assert!(display.skip.draws_outside(false));
        assert!(!display.focus.visible());

        let _ = display.update(property("time-pos", json!(560.0)));
        settle(&mut display);
        assert!(display.skip.available());
        assert!(!display.skip.draws_outside(false));
        assert!(!display.draws(Part::Above));

        // Down raises the display with the control focused, and the
        // control leaves with it.
        let _ = display.update(message("down"));
        assert!(display.focus.visible());
        assert_eq!(display.focus.focused_stop(), Some(Stop::Skip));
        let _ = display.update(message("back"));
        settle(&mut display);
        assert!(!display.focus.visible());
        assert!(!display.skip.draws_outside(false));
        assert!(!display.draws(Part::Above));

        let _ = display.update(property("time-pos", json!(1300.0)));
        settle(&mut display);
        assert!(!display.skip.available());
        assert!(!display.skip.draws_outside(false));
    }

    #[tokio::test]
    async fn an_item_with_no_marks_offers_no_skip_control() {
        let (mut display, _) = display();
        let _ = display.update(present(r#"{"title":"A Film"}"#));
        for at in [0.0, 1200.0, 5950.0] {
            let _ = display.update(property("time-pos", json!(at)));
            assert!(!display.skip.available(), "{at}");
        }
    }

    /// A select on the skip control seeks to the end of the intro. When
    /// the playhead leaves the span, the focus moves to the bar.
    #[tokio::test]
    async fn the_press_that_raises_the_display_and_a_select_skip_the_intro() {
        let (mut display, mut commands) = display();
        let _ = display.update(present(MARKED));
        let _ = display.update(message("down"));
        assert_eq!(display.focus.focused_stop(), Some(Stop::Skip));

        let _ = display.update(message("select"));
        assert_eq!(
            sent(&mut commands),
            vec!["{\"command\":[\"seek\",1250.0,\"absolute+exact\"],\"request_id\":1000}"]
        );
        let _ = display.update(property("time-pos", json!(1250.0)));
        assert_eq!(display.focus.focused_stop(), Some(Stop::Fine));
    }

    #[tokio::test]
    async fn the_credits_raise_the_card_at_their_start() {
        credits_raise_the_card(MARKED);
    }

    #[tokio::test]
    async fn the_credits_raise_an_episodes_card_at_their_start() {
        credits_raise_the_card(
            r#"{"series":"Harbor Lights","season":2,"episode":4,
                "marks":[{"kind":"credits","start":5400.0,"end":5900.0}]}"#,
        );
    }

    fn credits_raise_the_card(item: &str) {
        let (mut display, _) = display();
        let _ = display.update(present(item));
        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("time-pos", json!(5399.0)));
        assert_eq!(display.card.armed, None);

        let _ = display.update(property("time-pos", json!(5400.0)));
        assert!(display.card.armed.is_some());
        assert!(display.upnext.showing_card());
    }

    /// For an episode, the wait for a scene after the credits ends at
    /// the series rule, two minutes before the end of this 100-minute
    /// file. A film's rule would end it four minutes before the end.
    #[tokio::test]
    async fn an_episodes_card_waits_for_a_late_scene() {
        let (mut display, _) = display();
        let _ = display.update(present(
            r#"{"series":"Harbor Lights","season":2,"episode":4,"marks":[
                {"kind":"credits","start":5700.0},
                {"kind":"post-credits","start":5820.0,"end":5870.0}]}"#,
        ));
        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("time-pos", json!(5869.0)));
        assert_eq!(display.card.armed, None);

        let _ = display.update(property("time-pos", json!(5870.0)));
        assert!(display.upnext.showing_card());
    }

    #[tokio::test]
    async fn an_unmarked_episode_raises_the_card_by_the_series_rule() {
        let (mut display, _) = display();
        let _ = display.update(present(
            r#"{"series":"Harbor Lights","season":2,"episode":4}"#,
        ));
        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("time-pos", json!(5879.0)));
        assert_eq!(display.card.armed, None);

        let _ = display.update(property("time-pos", json!(5880.0)));
        assert!(display.card.armed.is_some());
        assert!(display.upnext.showing_card());
    }

    /// During credits that come before a scene, the skip control shows
    /// and the card waits. A select on the control seeks to the end of
    /// the first credits span, and the card appears when the second
    /// span starts.
    #[tokio::test]
    async fn the_card_waits_for_the_scene_and_the_control_leads_to_it() {
        let (mut display, mut commands) = display();
        let _ = display.update(present(
            r#"{"marks":[{"kind":"credits","start":5300.0,"end":5600.0},
                {"kind":"credits","start":5700.0}]}"#,
        ));
        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("time-pos", json!(5400.0)));
        assert!(!display.upnext.showing_card());
        assert!(display.skip.available());

        let _ = display.update(message("down"));
        assert_eq!(display.focus.focused_stop(), Some(Stop::Skip));
        let _ = display.update(message("up"));
        assert_eq!(display.focus.focused_stop(), Some(Stop::Next));
        let _ = display.update(message("down"));
        let _ = display.update(message("select"));
        assert_eq!(
            sent(&mut commands),
            vec!["{\"command\":[\"seek\",5600.0,\"absolute+exact\"],\"request_id\":1000}"]
        );

        let _ = display.update(property("time-pos", json!(5699.0)));
        assert!(!display.upnext.showing_card());
        let _ = display.update(property("time-pos", json!(5700.0)));
        assert!(display.upnext.showing_card());
        assert!(!display.skip.available());
    }

    #[tokio::test]
    async fn a_level_change_shows_the_row_alone() {
        let (mut display, _) = display();
        assert!(!display.volume.showing());

        let _ = display.update(words(&["volume-changed", "0.4", "no"]));
        settle(&mut display);
        assert!(display.volume.showing());
        assert!(!display.focus.visible());
        assert_eq!(display.focus.fade().value(), 0.0);

        let armed = display.row.armed.expect("a level change arms a window");
        let _ = display.update(Message::HideRow(armed));
        settle(&mut display);
        assert!(!display.volume.showing());
    }

    /// When the sidecar marks a level as not to be drawn, because the
    /// receiver shows its own overlay on the TV, the display records the
    /// level and shows no row.
    #[tokio::test]
    async fn a_level_change_the_receiver_draws_shows_no_row() {
        let (mut display, _) = display();

        let _ = display.update(words(&["volume-changed", "0.4", "no", "no"]));
        settle(&mut display);

        assert!(!display.volume.showing());
        assert!(display.row.armed.is_none());
    }

    #[tokio::test]
    async fn a_level_change_with_no_level_draws_nothing() {
        let (mut display, _) = display();
        let _ = display.update(message("volume-changed"));
        settle(&mut display);
        assert!(!display.volume.showing());
    }

    /// mpv plays at unity gain, so its own `volume` and `mute` do not
    /// reflect the room's level, and they show no row.
    #[tokio::test]
    async fn mpvs_own_volume_shows_no_row() {
        let (mut display, _) = display();
        let _ = display.update(property("volume", json!(40.0)));
        let _ = display.update(property("mute", json!(true)));
        settle(&mut display);
        assert!(!display.volume.showing());
    }

    #[tokio::test]
    async fn the_frame_tick_runs_for_every_fade() {
        let (mut display, _) = display();
        assert!(!display.fading());

        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("time-pos", json!(5940.0)));
        assert!(display.fading());
        settle(&mut display);
        assert!(!display.fading());

        let _ = display.update(words(&["volume-changed", "0.4", "no"]));
        assert!(display.fading());
        settle(&mut display);
        assert!(!display.fading());
    }

    /// A `next` block with a picture of 800 by 400, which is wider than
    /// the card's picture box.
    pub(super) fn offer_with_art() -> String {
        format!(
            r#"{{"title":"E05 · The Long Tide","art":"{}"}}"#,
            picture("next", 800, 400)
        )
    }

    #[tokio::test]
    async fn an_offer_with_a_picture_decodes_it() {
        let (mut display, _) = display();
        assert!(jobs_after(&mut display, block("next", OFFER)).is_empty());
        assert_eq!(display.upnext.art(), None);

        let jobs = jobs_after(&mut display, block("next", &offer_with_art()));
        assert!(display.upnext.art().is_some());
        assert_eq!(jobs.len(), 1);
    }

    /// The card gets the offer's picture at its drawn size, in canvas
    /// units. The picture is wider than its box, so it is scaled down to
    /// fit.
    #[tokio::test]
    async fn the_card_takes_the_offers_picture_at_the_size_it_draws_at() {
        let (mut display, _) = display();
        let canvas = display.canvas;
        assert_eq!(upnext::Art::next(&display.bridge(&canvas)), None);

        let jobs = jobs_after(&mut display, block("next", &offer_with_art()));
        let _ = run_job(&mut display, jobs.into_iter().next().unwrap());
        assert_eq!(
            upnext::Art::next(&display.bridge(&canvas)),
            Some(Size::new(440.0, 220.0))
        );
    }

    /// Sends a duration and then the positions through a `Pace`, and
    /// returns how many positions it forwards.
    fn paced(visible: bool, duration: f64, positions: &[f64]) -> usize {
        let (_sender, watching) = watch::channel(Watching {
            canvas: Canvas::default(),
            visible,
        });
        let mut pace = Pace::new(watching);
        assert!(pace.forwards(&ipc::Event::Property {
            name: "duration".to_string(),
            value: json!(duration),
        }));
        positions
            .iter()
            .filter(|at| {
                pace.forwards(&ipc::Event::Property {
                    name: "time-pos".to_string(),
                    value: json!(*at),
                })
            })
            .count()
    }

    /// Ten positions, 0.04 seconds apart, that all round to the same
    /// whole second on the time label.
    fn one_second(from: f64) -> Vec<f64> {
        (0..10)
            .map(|frame| from + f64::from(frame) / 25.0)
            .collect()
    }

    #[tokio::test]
    async fn a_second_of_positions_reaches_the_frame_loop_once() {
        assert_eq!(paced(true, 6000.0, &one_second(1200.6)), 1);
        assert_eq!(paced(false, 6000.0, &one_second(1200.6)), 1);

        let two: Vec<f64> = one_second(1200.6)
            .into_iter()
            .chain(one_second(1201.6))
            .collect();
        assert_eq!(paced(true, 6000.0, &two), 2);
    }

    /// In a 100-second film the playhead crosses output pixels within one
    /// second, so `Pace` forwards those positions while the OSD is up.
    #[tokio::test]
    async fn a_playhead_that_moves_inside_a_second_is_forwarded() {
        assert!(paced(true, 100.0, &one_second(50.6)) > 1);
        assert_eq!(paced(false, 100.0, &one_second(50.6)), 1);
    }

    #[tokio::test]
    async fn every_other_event_reaches_the_frame_loop() {
        let (_sender, watching) = watch::channel(Watching {
            canvas: Canvas::default(),
            visible: true,
        });
        let mut pace = Pace::new(watching);
        assert!(pace.forwards(&ipc::Event::Message(vec!["summon".to_string()])));
        assert!(pace.forwards(&ipc::Event::Detached));
        assert!(pace.forwards(&ipc::Event::Property {
            name: "pause".to_string(),
            value: json!(true),
        }));
    }

    #[tokio::test]
    async fn a_position_with_the_display_down_computes_no_signature() {
        let (mut display, _) = display();
        let _ = display.update(property("time-pos", json!(1200.0)));
        assert_eq!(display.signature, None);

        let _ = display.update(message("summon"));
        settle(&mut display);
        let _ = display.update(property("time-pos", json!(1201.0)));
        assert!(display.signature.is_some());
    }

    #[tokio::test]
    async fn a_minute_message_keeps_the_raised_display_up() {
        let (mut display, _) = display();
        let _ = display.update(pause(false));
        let _ = display.update(pause(true));
        settle(&mut display);
        assert!(display.focus.visible());

        let _ = display.update(Message::Minute);
        assert!(display.focus.visible());
    }
}
