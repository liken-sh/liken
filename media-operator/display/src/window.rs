//! The display's own Wayland surface, and what it draws on it.
//!
//! The window is 1920 by 1080 in logical pixels, undecorated, and
//! transparent. Between summons it draws nothing, and a frame that draws
//! nothing is a fully transparent frame.

use std::hash::{Hash, Hasher};
use std::path::Path;
use std::sync::Arc;
use std::time::{Duration, Instant};

use iced::widget::canvas;
use iced::{Color, Element, Length, Rectangle, Renderer, Size, Subscription, Task, Theme};
use jiff::Zoned;
use tokio::sync::{broadcast, watch};

use crate::art::{self, Answer, Art, Job};
use crate::canvas::{Brush, Canvas, Scrims};
use crate::fade::Hide;
use crate::film::Film;
use crate::focus::{Focus, Parts, Stop};
use crate::ipc::{self, Command, Ipc};
use crate::presentation::Presentation;
use crate::scrubber::Scrubber;
use crate::skip::Skip;
use crate::strip::Strip;
use crate::upnext::{self, UpNext};
use crate::volume::Volume;
use crate::{clock, header, images, theme};

/// How long the display waits for mpv to report a position before it exits
/// and lets the kubelet restart the container. It is longer than a player
/// takes to open a film and shorter than a person waits at a bare picture.
pub const PLAYER_GRACE: Duration = Duration::from_secs(60);

/// How many messages the socket reader may run ahead of the frame loop, and
/// how many commands the frame loop may run ahead of the socket.
const EVENT_QUEUE: usize = 64;

/// The wall clock's own step, which the reading and the end time follow while
/// the display stands open with no position arriving.
const MINUTE: Duration = Duration::from_secs(60);

/// What moves the display.
#[derive(Debug, Clone)]
pub enum Message {
    /// One line mpv wrote.
    Mpv(ipc::Event),
    /// One step of the fade.
    Tick,
    /// One minute of the wall clock. A paused film pushes no position, so
    /// this is what moves the reading and the end time while it stands.
    Minute,
    /// The surface the compositor configured, as the window reports it.
    Resized(Size),
    /// The idle window ran out. The count says which summon armed it, so a
    /// window a later summon replaced dismisses nothing.
    Hide(u64),
    /// The up-next card's own window ran out, so the card leaves while the
    /// film plays on.
    HideCard(u64),
    /// The volume row's own window ran out.
    HideRow(u64),
    /// One decode came back from the blocking pool.
    Art(Answer),
}

/// One hide window: how many have been armed, and which one stands. A window a
/// later change replaced takes nothing down, so each one carries the count of
/// the change that armed it.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
struct Idle {
    count: u64,
    armed: Option<u64>,
}

impl Idle {
    fn arm(&mut self, hide: Hide, wrap: fn(u64) -> Message) -> Task<Message> {
        match hide {
            Hide::Arm => {
                self.count += 1;
                let at = self.count;
                self.armed = Some(at);
                Task::perform(tokio::time::sleep(theme::IDLE_HIDE), move |()| wrap(at))
            }
            Hide::Cancel => {
                self.armed = None;
                Task::none()
            }
            Hide::Keep => Task::none(),
        }
    }

    /// Whether this window is the one that stands, which takes it down.
    fn expired(&mut self, at: u64) -> bool {
        if self.armed != Some(at) {
            return false;
        }
        self.armed = None;
        true
    }
}

/// The whole of the display's state: what mpv reports, what the item declared,
/// where the focus stands, and how far the fade has moved.
pub struct Display {
    wire: Wire,
    focus: Focus,
    film: Film,
    presentation: Presentation,
    scrubber: Scrubber,
    strip: Strip,
    upnext: UpNext,
    skip: Skip,
    volume: Volume,
    art: Art,
    /// The hide windows the display, the card, and the row wait out, each on
    /// its own clock and none reading another's.
    idle: Idle,
    card: Idle,
    row: Idle,
    /// The monotonic clock the seek ramp reads, which the scan's rate needs
    /// and the wall clock cannot give.
    started: Instant,
    /// The last position signature, so a push that moves nothing a person can
    /// see redraws nothing.
    signature: Option<Signature>,
    /// The surface the compositor configured, which the window reports and
    /// every decode and every snap reads.
    canvas: Canvas,
    /// The scrims for that surface, resolved once per size.
    scrims: Scrims,
    /// What the socket reader needs to tell whether one position push moves
    /// anything on screen.
    watching: watch::Sender<Watching>,
    /// The two drawn layers. mpv pushes a position on every video frame, and a
    /// layer is rebuilt only when one of the things it draws changes.
    below: canvas::Cache,
    above: canvas::Cache,
}

/// What a position moves on screen: the playhead's own output pixel and the
/// whole second the label reads.
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
                watching: watched,
            },
            focus: Focus::new(),
            film: Film::default(),
            presentation: Presentation::default(),
            scrubber: Scrubber::default(),
            strip: Strip::default(),
            upnext: UpNext::default(),
            skip: Skip::default(),
            volume: Volume::default(),
            art: Art::default(),
            idle: Idle::default(),
            card: Idle::default(),
            row: Idle::default(),
            started: Instant::now(),
            signature: None,
            canvas,
            scrims: Scrims::for_canvas(&canvas),
            watching,
            below: canvas::Cache::new(),
            above: canvas::Cache::new(),
        }
    }

    /// One message, the art's own turn, which asks for what the state it
    /// leaves behind needs, and the word to the socket reader about what the
    /// display draws now.
    pub fn update(&mut self, message: Message) -> Task<Message> {
        let task = self.step(message);
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
            Message::Mpv(ipc::Event::Detached) => {}
            Message::Tick => {
                self.focus.fade_mut().step();
                self.upnext.fade_mut().step();
                self.skip.fade_mut().step();
                self.volume.fade_mut().step();
                self.redraw();
            }
            // The wall clock and the end time stand in the lower layer, and
            // nothing else moves on a minute.
            Message::Minute => self.redraw_below(),
            Message::Resized(size) => self.on_resize(size),
            Message::Hide(at) if self.idle.expired(at) => {
                let mut focus = self.focus;
                focus.dismiss(&mut self.parts());
                self.focus = focus;
                self.redraw();
                return self.windows();
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
            Message::Art(answer) => {
                let (changed, jobs) = self.art.on_answer(answer);
                if changed {
                    self.redraw();
                }
                return decode(jobs);
            }
        }
        Task::none()
    }

    /// The surface changed size. Every scrim is resolved for the new one, and
    /// the decodes that follow ask at the new box.
    fn on_resize(&mut self, size: Size) {
        let canvas = Canvas::for_output(size);
        if canvas == self.canvas {
            return;
        }
        self.canvas = canvas;
        self.scrims = Scrims::for_canvas(&canvas);
        self.redraw();
    }

    // Rebuild both layers on the next frame, which is what a change to
    // anything the display draws asks for.
    fn redraw(&self) {
        self.redraw_below();
        self.redraw_above();
    }

    // The layer under the OSD: the header, the clock, the scrubber, the
    // strip, and the offer inside the OSD. The position moves this one alone.
    fn redraw_below(&self) {
        self.below.clear();
    }

    // The layer over the OSD: an open chooser, the card on its own clock, and
    // the volume row. A level and a hide window move this one alone.
    fn redraw_above(&self) {
        self.above.clear();
    }

    /// The modules one press reaches. The frame loop owns them, so it hands
    /// them to the router for the length of the press.
    fn parts(&mut self) -> Parts<'_> {
        let now = self.now();
        Parts {
            presentation: &self.presentation,
            film: &self.film,
            scrubber: &mut self.scrubber,
            strip: &mut self.strip,
            now,
            upnext: &mut self.upnext,
            skip: &mut self.skip,
        }
    }

    /// The six navigation words the command sidecar sends, the summon it sends
    /// after a seek of its own, and the block it sends for every item.
    fn on_message(&mut self, words: &[String]) -> Task<Message> {
        let Some(word) = words.first() else {
            return Task::none();
        };
        let mut focus = self.focus;
        if word == ipc::SUMMON {
            focus.summon(&self.parts());
        } else if let Some(action) = crate::focus::Action::from_word(word) {
            let commands = focus.nav(action, &mut self.parts());
            self.send(commands);
        } else if word == ipc::PRESENTATION {
            self.presentation
                .receive(words.get(1).map_or("", String::as_str));
            // A new block is a new item. It drops the previous item's
            // pictures and the measurements of its lines, and the turn that
            // follows decodes the new item's logo.
            crate::canvas::forget_measurements();
            self.art.on_item(&self.presentation, &self.canvas);
            // The new item's marks may place the playhead inside a span, or
            // outside the one the last item offered.
            self.follow_marks();
        } else if word == ipc::NEXT {
            self.upnext.receive(words.get(1).map_or("", String::as_str));
        } else if word == ipc::VOLUME_CHANGED {
            self.volume.show();
        } else {
            return Task::none();
        }
        self.focus = focus;
        self.redraw();
        self.windows()
    }

    /// mpv pushes a position on every video frame, and a redraw builds the
    /// whole layer again. Two things on screen follow the value: the playhead
    /// moves one output pixel every few seconds, and the time label changes
    /// once a second. The signature carries both, so a push that leaves it
    /// alone moves nothing a person can see and redraws nothing.
    ///
    /// The offer reads the position as well, and raises its card at the rise,
    /// and the skip control comes and goes with the intro and the recap.
    /// Those are the two things a position does with the display down.
    fn on_property(&mut self, name: &str, value: &serde_json::Value) -> Task<Message> {
        self.film.apply(name, value);
        if !draws(name) {
            return self.own_clock(name, value);
        }
        if name == "time-pos" {
            let credits = self.presentation.marks().credits(self.film.duration);
            let rose = self.upnext.on_percent(self.percent(), &self.film, credits);
            if rose {
                self.redraw();
            }
            self.follow_marks();
            // With the display down nothing under it follows the position, so
            // the signature is not worth the work.
            if self.focus.fade().value() > 0.0 {
                let signature = self.signature(value.as_f64());
                if self.signature != Some(signature) {
                    self.signature = Some(signature);
                    self.redraw_below();
                }
            }
            return match rose {
                true => self.windows(),
                false => Task::none(),
            };
        }
        self.redraw();
        if name == "pause" {
            let mut focus = self.focus;
            focus.on_pause(value.as_bool().unwrap_or(false), &mut self.parts());
            self.focus = focus;
            return self.windows();
        }
        // The offer is for the item the Play started on, so a move to another
        // item drops it.
        if name == "playlist-pos" {
            self.upnext.on_playlist_pos(value.as_i64());
        }
        Task::none()
    }

    /// The two properties no part of the display draws by itself. The volume
    /// row records the level and the muted flag, and redraws only while it is
    /// on screen, so a level that lands after the sidecar's message reaches
    /// the bar it belongs to.
    fn own_clock(&mut self, name: &str, value: &serde_json::Value) -> Task<Message> {
        match name {
            "volume" => self.volume.on_volume(value.as_f64()),
            "mute" => self.volume.on_mute(value.as_bool()),
            _ => return Task::none(),
        }
        if self.volume.showing() {
            self.redraw_above();
        }
        Task::none()
    }

    /// Offer the skip control while the playhead is inside an intro or a
    /// recap, and take it down when the playhead leaves. A focus on the
    /// control that has gone moves to the bar.
    fn follow_marks(&mut self) {
        let inside = self
            .presentation
            .marks()
            .skippable(self.film.position, self.film.duration);
        if !self.skip.on_position(inside) {
            return;
        }
        let mut focus = self.focus;
        focus.refocus(&self.parts());
        self.focus = focus;
        self.redraw();
    }

    /// How far into the work the playhead stands, as the hundredths mpv
    /// reports in `percent-pos`. The offer reads it to tell whether the
    /// playhead has crossed the rise, and it is the position over the length,
    /// so the display observes one property for both.
    fn percent(&self) -> Option<f64> {
        let duration = self.film.duration.filter(|duration| *duration > 0.0)?;
        Some(self.film.position? / duration * 100.0)
    }

    /// What the decode needs to know for this state: the surface the compositor
    /// configured, the item, the file mpv plays, and the scan in flight.
    fn sync_art(&mut self) -> Vec<Job> {
        let (canvas, preview) = (self.canvas, self.previewing());
        self.art.sync(
            &self.presentation,
            &self.film,
            &canvas,
            preview,
            self.upnext.art(),
        )
    }

    fn bridge(&self, canvas: &Canvas) -> Bridge<'_> {
        Bridge {
            art: &self.art,
            canvas: *canvas,
        }
    }

    /// The monotonic second the tile's own gate reads, which the wall clock
    /// cannot give.
    fn now(&self) -> f64 {
        self.started.elapsed().as_secs_f64()
    }

    /// Whether a picture the OSD carries shows. The logo, the tile, and the
    /// offer's picture show while the OSD is up, and hide when it hides and
    /// while a chooser captures, so a corner logo never lingers over a plain
    /// frame or floats above the chooser's dim.
    fn showing(&self) -> bool {
        self.focus.visible() && self.strip.capturing().is_none()
    }

    /// The time the thumbnail previews. It shows only while a fine scan is in
    /// flight, for an item that declares trickplay. At rest the video shows the
    /// frame the thumbnail would, so the tile appears only when the scan
    /// previews another position.
    fn previewing(&self) -> Option<f64> {
        (self.showing()
            && self.focus.focused_stop() == Some(Stop::Fine)
            && self.scrubber.scanning()
            && self.presentation.trickplay().is_some())
        .then(|| self.scrubber.cursor_time(&self.film))
        .flatten()
    }

    fn signature(&self, at: Option<f64>) -> Signature {
        Signature {
            pixel: crate::scrubber::position_pixel(&self.canvas, &self.film, at),
            second: (at.unwrap_or(0.0) + 0.5).floor() as i64,
        }
    }

    /// Arm or cancel every hide window the last change asked for. The
    /// display, the up-next card, and the volume row each wait out a window of
    /// its own.
    fn windows(&mut self) -> Task<Message> {
        let idle = self.idle.arm(self.focus.take_hide(), Message::Hide);
        let card = self.card.arm(self.upnext.take_hide(), Message::HideCard);
        let row = self.row.arm(self.volume.take_hide(), Message::HideRow);
        Task::batch([idle, card, row])
    }

    /// Write every command one press asked for to the socket the display
    /// already reads.
    fn send(&self, commands: Vec<Command>) {
        for command in commands {
            let _ = self.wire.commands.send(ipc::line(&command));
        }
    }

    /// Draw the header and the clock, then the scrubber, then the strip. The
    /// scrubber owns two focus stops but draws one bar, told which axis is
    /// focused.
    fn paint_below(&self, brush: &mut Brush<'_>, now: &Zoned) {
        let canvas = brush.canvas();
        // The cover does not track the OSD, because a music screen with the OSD
        // down would otherwise be a black frame. Every other picture rises and
        // falls with the layer; this one holds the frame at full strength.
        if let Some(cover) = self.art.cover() {
            brush.at_fade(1.0, |brush| {
                cover.draw(brush, art::cover_at(&canvas, cover));
            });
        }
        if brush.fade() <= 0.0 {
            return;
        }

        // The top scrim backs the header and the clock, drawn before them so
        // their text is on top.
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
            brush.scrim(&self.scrims.top);
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
        let strip = self.strip.lines(
            &canvas,
            &self.presentation,
            &self.film,
            self.focus.focused_stop() == Some(Stop::Strip),
        );
        if bar.is_some() || !counter.is_empty() || !strip.is_empty() {
            // The bottom scrim backs the scrubber and the strip, drawn before
            // them so their text is on top.
            brush.scrim(&self.scrims.bottom);
            if let Some(bar) = bar.as_ref() {
                self.scrubber.draw(brush, bar);
            }
            for line in counter.into_iter().chain(strip) {
                brush.text(line);
            }
        }
        // The skip control draws after the bottom cluster, so it reads over
        // the scrim, and before the thumbnail, so a scan near the start of
        // the film shows its frame over the control.
        self.skip
            .draw(brush, self.focus.focused_stop() == Some(Stop::Skip));
        // The thumbnail stands over the bar, centred on the playhead it
        // previews.
        if let Some(tile) = self.art.tile()
            && self.previewing().is_some()
            && let Some(cursor) = self.scrubber.cursor_x(&canvas, &self.film)
        {
            tile.draw(brush, art::tile_at(&canvas, tile, cursor));
        }
        // The offer draws after the bottom cluster, so the chip and the card
        // read over the scrim and beside the scrubber.
        self.upnext.draw(
            brush,
            self.focus.focused_stop() == Some(Stop::Next),
            &self.bridge(&canvas),
        );
    }

    /// The open chooser, which covers every region while it captures input.
    ///
    // The chooser draws in a layer of its own. iced_wgpu draws one layer in
    // four passes, quads then meshes then images then text, so a rectangle in
    // the same layer as the text lands under every line of it. A second layer
    // draws after the whole of the first, and the dim and the panel cover the
    // text they are meant to cover.
    fn paint_above(&self, brush: &mut Brush<'_>) {
        let canvas = brush.canvas();
        if self.strip.capturing().is_some() {
            // A chooser is open. Dim the whole frame under it, so the list
            // reads as the one thing in focus and the scrubber and strip
            // recede.
            brush.rect(
                Rectangle::new(
                    iced::Point::ORIGIN,
                    Size::new(canvas.width(), canvas.height()),
                ),
                theme::at(theme::color::SHADOW, theme::alpha::DIM),
            );
            self.strip.draw_chooser(brush, &self.film);
        }
        // The card shows itself when the playhead crosses the rise, and it
        // stays for the whole wait, both with the OSD down. So it draws
        // outside the OSD block on a fade of its own.
        self.upnext
            .draw_outside(brush, self.focus.visible(), &self.bridge(&canvas));
        // The skip control stays over the bare video for the whole span, on a
        // fade of its own, for the same reason.
        self.skip.draw_outside(brush, self.focus.visible());
        // The volume row draws outside the OSD block, because it comes and
        // goes on a clock of its own and a level change must show the level
        // and nothing else. It draws last, so it reads over a chooser's dim as
        // well as over the bare video.
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

    /// The socket reader always runs. The frame tick runs only while the
    /// fade is moving, so a display standing at either end costs the process
    /// nothing.
    pub fn subscription(&self) -> Subscription<Message> {
        let mpv = Subscription::run_with(self.wire.clone(), |wire| {
            let ipc = Ipc::at(&wire.path);
            let commands = wire.commands.clone();
            let mut pace = Pace::new(wire.watching.clone());
            iced::stream::channel(EVENT_QUEUE, async move |mut output| {
                use iced::futures::SinkExt;

                let (sender, mut events) = tokio::sync::mpsc::channel(EVENT_QUEUE);
                tokio::spawn(async move { ipc.serve(sender, &commands).await });
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
        ];
        if self.fading() {
            running.push(iced::time::every(theme::FADE_TICK).map(|_| Message::Tick));
        }
        // A paused film pushes no position, and the display stands open for
        // as long as the pause lasts, so the reading and the end time follow
        // a tick of their own while it is up.
        if self.focus.visible() {
            running.push(iced::time::every(MINUTE).map(|_| Message::Minute));
        }
        Subscription::batch(running)
    }

    /// Whether any of the four fades is moving: the display's own, the
    /// up-next card's, the skip control's, and the volume row's.
    fn fading(&self) -> bool {
        self.focus.fade().running()
            || self.upnext.fade().running()
            || self.skip.fade().running()
            || self.volume.fade().running()
    }
}

// The decode runs on tokio's blocking pool. A file read, an https fetch,
// and a sprite-sheet decode are all blocking work that must never run
// inside a frame. A decode that panics answers with no picture, so the slot
// it names stops waiting on it, and the line names the reference that did it.
fn decode(jobs: Vec<Job>) -> Task<Message> {
    Task::batch(jobs.into_iter().map(|job| {
        let failed = job.clone();
        Task::future(async move { tokio::task::spawn_blocking(move || job.run()).await }).then(
            move |answer| {
                Task::done(Message::Art(match answer {
                    Ok(answer) => answer,
                    Err(_) => {
                        eprintln!("media-display: the decode of {} failed", failed.reference());
                        failed.clone().empty()
                    }
                }))
            },
        )
    }))
}

// The socket and the channel the frame loop writes its commands to, as one
// subscription, and the word back about what the display draws now. The path
// alone names the subscription, so a redraw that rebuilds it attaches to the
// same socket instead of dialing again.
#[derive(Debug, Clone)]
pub struct Wire {
    path: Arc<Path>,
    commands: broadcast::Sender<String>,
    watching: watch::Receiver<Watching>,
}

impl Hash for Wire {
    fn hash<H: Hasher>(&self, state: &mut H) {
        self.path.hash(state);
    }
}

/// What the socket reader reads to tell whether one position push moves
/// anything: the surface the playhead travels across, and whether the display
/// draws at all.
#[derive(Debug, Clone, Copy, PartialEq)]
struct Watching {
    canvas: Canvas,
    visible: bool,
}

/// The gate on the position pushes.
///
/// mpv pushes a position on every video frame, and the toolkit presents a
/// frame for every batch of messages it takes, so a push that moves no pixel
/// costs a surface commit and a recomposite of the whole screen. The reader
/// forwards one only when the second the time label reads changes, or the
/// playhead moves an output pixel. With the display down nothing under it
/// follows the position, so the whole second alone carries it, which holds
/// the film's own state current at one message a second for the summon that
/// follows.
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

    /// Whether one event reaches the frame loop.
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

// The two halves meet here. The card states its seam in canvas units and the
// display holds its pictures in real pixels, so this carries the canvas the
// frame draws on and reads the one in the other.
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

/// Whether one property push changes anything the display draws.
///
/// The volume indicator comes and goes on a clock of its own, so neither of
/// the two properties it reads redraws the layer by itself.
fn draws(name: &str) -> bool {
    !matches!(name, "volume" | "mute")
}

/// Which of the two layers one canvas draws.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Part {
    Below,
    Above,
}

/// One layer of the display, which the modules draw into.
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
        // The canvas comes from the surface the compositor configured, which
        // the window reports on its own, because the region a Layout gives
        // this pod is the compositor's choice and not this client's. The
        // frame loop learns it before it draws, so the first decode of an
        // item asks for the box the screen really takes.
        let canvas = self.display.canvas;

        // A frame with nothing in it is what the display draws between
        // summons: the film below shows through every pixel of the window.
        // The up-next card, the skip control, and the volume row come and go
        // on clocks of their own, so the layer above draws for them with the
        // display down. The
        // cover is the one picture that holds the frame with the OSD down, so
        // the layer under the OSD draws for it as well.
        let fade = self.display.focus.fade().value();
        let draws = match self.part {
            Part::Below => fade > 0.0 || self.display.art.cover().is_some(),
            Part::Above => {
                fade > 0.0
                    || self
                        .display
                        .upnext
                        .draws_outside(self.display.focus.visible())
                    || self
                        .display
                        .skip
                        .draws_outside(self.display.focus.visible())
                    || self.display.volume.showing()
            }
        };
        if !draws {
            return Vec::new();
        }

        let (cache, now) = match self.part {
            Part::Below => (&self.display.below, Some(clock::now())),
            Part::Above => (&self.display.above, None),
        };
        vec![cache.draw(renderer, bounds.size(), |frame| {
            frame.scale(canvas.scale());
            let mut brush = Brush::new(frame, canvas, fade);
            match now {
                Some(now) => self.display.paint_below(&mut brush, &now),
                None => self.display.paint_above(&mut brush),
            }
        })]
    }
}

/// Open the window and run the frame loop. The caller waits for mpv first,
/// so the surface this opens arrives after the film's.
pub fn run(ipc: Ipc) -> iced::Result {
    iced::application(
        // The window answers its own size once at the start. Every later
        // size arrives as a resize event, so the display holds the surface
        // it draws on from its first frame.
        //
        // The display reads no pointer, so its surface takes an empty
        // input region and the pointer reaches the film's surface under
        // it. The compositor's shell gives the keyboard to the surface a
        // click lands on, so without this the scrim takes every click
        // and the player never gets a key.
        move || {
            (
                Display::new(ipc.clone()),
                iced::window::oldest().and_then(|id| {
                    iced::window::enable_mouse_passthrough(id)
                        .chain(iced::window::size(id).map(Message::Resized))
                }),
            )
        },
        Display::update,
        Display::view,
    )
    .subscription(Display::subscription)
    // The window is transparent, so the whole style is the ground it
    // does not paint.
    .style(|_display, _theme| iced::theme::Style {
        background_color: Color::TRANSPARENT,
        text_color: theme::color::text(),
    })
    .window(iced::window::Settings {
        size: Size::new(theme::CANVAS_WIDTH, theme::CANVAS_HEIGHT),
        decorations: false,
        transparent: true,
        resizable: false,
        ..Default::default()
    })
    // The brand's two faces come out of the binary, not out of whatever
    // the image has installed, so every liken display draws the same
    // face.
    .font(liken_iced::font::REGULAR_OTF)
    .font(liken_iced::font::ITALIC_OTF)
    .default_font(liken_iced::font::REGULAR)
    .title("liken display")
    .run()
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn message(word: &str) -> Message {
        Message::Mpv(ipc::Event::Message(vec![word.to_string()]))
    }

    fn property(name: &str, value: serde_json::Value) -> Message {
        Message::Mpv(ipc::Event::Property {
            name: name.to_string(),
            value,
        })
    }

    fn pause(paused: bool) -> Message {
        property("pause", json!(paused))
    }

    /// One display over a film with a length, chapters, and two audio tracks,
    /// with a reader on the socket so the commands it sends can be read back.
    fn display() -> (Display, broadcast::Receiver<String>) {
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

    /// Run every fade to wherever it is going, the way the tick does.
    fn settle(display: &mut Display) {
        while display.fading() {
            let _ = display.update(Message::Tick);
        }
    }

    fn sent(commands: &mut broadcast::Receiver<String>) -> Vec<String> {
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

    #[tokio::test]
    async fn the_summon_and_the_six_actions_raise_the_display() {
        for word in ["summon", "up", "down", "left", "right"] {
            let (mut display, _) = display();
            let _ = display.update(message(word));
            settle(&mut display);
            assert!(display.focus.visible(), "{word} raises the display");
            assert_eq!(display.focus.fade().value(), 1.0);
        }
    }

    #[tokio::test]
    async fn a_message_the_display_has_no_case_for_raises_nothing() {
        let (mut display, _) = display();
        let _ = display.update(message("liken-art"));
        let _ = display.update(Message::Mpv(ipc::Event::Message(Vec::new())));
        assert!(!display.focus.visible());
        assert_eq!(display.focus.fade().value(), 0.0);
    }

    #[tokio::test]
    async fn the_idle_window_dismisses_the_display() {
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

    /// A second summon arms a second window, and the first one's expiry
    /// dismisses nothing.
    #[tokio::test]
    async fn a_stale_idle_window_dismisses_nothing() {
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

    /// mpv reports the pause state once at registration. The first report
    /// only records it, so a film that loads paused starts hidden.
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
    async fn a_pause_raises_the_display_and_arms_no_idle_window() {
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

    /// A summon while the fade is falling reverses it in place, so the
    /// display never drops to nothing and rises again.
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

    /// The display resolves the scrims for the surface it draws on, and a
    /// surface of another size resolves them again.
    #[tokio::test]
    async fn a_resize_resolves_the_scrims_for_the_new_surface() {
        let (mut display, _) = display();
        let bands = display.scrims.clone();
        assert!(
            bands
                .top
                .iter()
                .any(|band| band.stops.iter().any(|alpha| *alpha > 0.0))
        );

        let _ = display.update(Message::Resized(Size::new(1280.0, 720.0)));
        assert_eq!(display.canvas, Canvas::for_output(Size::new(1280.0, 720.0)));
        assert_ne!(display.scrims, bands);
    }

    /// A frame of playback that moves nothing redraws nothing, and a whole
    /// second of it redraws the layer.
    #[tokio::test]
    async fn a_position_that_moves_nothing_a_person_sees_redraws_nothing() {
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

    /// A pixel of playhead travel redraws the layer, although the label reads
    /// the same second.
    #[tokio::test]
    async fn a_pixel_of_playhead_travel_carries_a_new_signature() {
        let (mut display, _) = display();
        let _ = display.update(message("summon"));
        settle(&mut display);
        let _ = display.update(property("time-pos", json!(1200.0)));
        let first = display.signature.expect("a position carries a signature");
        let _ = display.update(property("time-pos", json!(1204.0)));
        let moved = display.signature.expect("a moved position carries one too");
        assert_ne!(moved.pixel, first.pixel);
    }

    /// A film with no length carries no pixel, so a position on it follows the
    /// whole second alone.
    #[tokio::test]
    async fn a_film_with_no_length_carries_no_playhead_pixel() {
        let mut display = Display::new(Ipc::default());
        let _ = display.update(message("summon"));
        settle(&mut display);
        let _ = display.update(property("time-pos", json!(12.5)));
        assert_eq!(
            display.signature.map(|signature| signature.pixel),
            Some(None)
        );
    }

    /// Every press the router answers reaches mpv on the display's own socket.
    #[tokio::test]
    async fn a_press_the_router_answers_reaches_mpv() {
        let (mut display, mut commands) = display();
        let _ = display.update(message("summon"));
        assert!(sent(&mut commands).is_empty());

        let _ = display.update(message("right"));
        assert!(sent(&mut commands).is_empty());
        let _ = display.update(message("select"));
        assert_eq!(
            sent(&mut commands),
            vec!["{\"command\":[\"seek\",1205.0,\"absolute+exact\"],\"request_id\":0}"]
        );

        let _ = display.update(message("select"));
        assert_eq!(
            sent(&mut commands),
            vec!["{\"command\":[\"no-osd\",\"cycle\",\"pause\"],\"request_id\":0}"]
        );
    }

    /// The block the sidecar sends names the item, and a block for another
    /// item replaces it.
    #[tokio::test]
    async fn the_block_the_sidecar_sends_names_the_item() {
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

    /// One block the sidecar sends, as the message it arrives in.
    fn present(block: &str) -> Message {
        Message::Mpv(ipc::Event::Message(vec![
            "presentation".to_string(),
            block.to_string(),
        ]))
    }

    /// One picture on disk, of the size a decode reads back.
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

    /// One message, and the decodes the state it leaves behind asks for. The
    /// frame loop hands these to the blocking pool; a test runs them itself.
    fn asks(display: &mut Display, message: Message) -> Vec<Job> {
        let _ = display.step(message);
        display.sync_art()
    }

    /// One decode run the way the pool runs it, with its answer taken back
    /// into the display and the decode it may start in turn.
    fn answer(display: &mut Display, job: Job) -> Vec<Job> {
        let (changed, more) = display.art.on_answer(job.run());
        if changed {
            display.redraw();
        }
        more
    }

    /// A new item decodes the logo it declares, and the answer clears the
    /// header's second line.
    #[tokio::test]
    async fn an_item_decodes_its_logo_and_the_answer_moves_the_second_line() {
        let (mut display, _) = display();
        let logo = picture("logo", 800, 128);
        let jobs = asks(
            &mut display,
            present(&format!(r#"{{"title":"A Film","logo":"{logo}"}}"#)),
        );
        assert_eq!(jobs.len(), 1);

        assert!(answer(&mut display, jobs.into_iter().next().unwrap()).is_empty());
        let drawn = display.art.logo().expect("the answer carries a picture");
        assert_eq!(drawn.canvas_height(&display.canvas), 110.0);
        // The box is asked for once per item, and a new item asks again.
        assert!(asks(&mut display, property("time-pos", json!(12.0))).is_empty());
        assert_eq!(
            asks(&mut display, present(&format!(r#"{{"logo":"{logo}"}}"#))).len(),
            1
        );
    }

    /// A music item whose block names no cover waits for the file mpv plays,
    /// because an answer of no cover would end the asking for the whole run.
    /// One whose block names its cover decodes it at once, and holds it
    /// whether or not the OSD is up.
    #[tokio::test]
    async fn a_music_item_decodes_its_cover_and_holds_it() {
        let (mut display, _) = display();
        assert!(
            asks(
                &mut display,
                present(r#"{"type":"music","title":"A Record"}"#)
            )
            .is_empty()
        );

        let jobs = asks(&mut display, property("path", json!("/media/track.flac")));
        assert_eq!(jobs.len(), 1);
        assert!(answer(&mut display, jobs.into_iter().next().unwrap()).is_empty());
        assert!(display.art.cover().is_none());
        // The box is answered, so the asking ends there.
        assert!(asks(&mut display, property("time-pos", json!(12.0))).is_empty());

        let cover = picture("cover", 532, 532);
        let jobs = asks(
            &mut display,
            present(&format!(r#"{{"type":"music","art":"{cover}"}}"#)),
        );
        assert_eq!(jobs.len(), 1);
        let _ = answer(&mut display, jobs.into_iter().next().unwrap());
        assert!(display.art.cover().is_some());
        assert!(!display.focus.visible());
    }

    /// The tile is decoded only while a fine scan is in flight for an item
    /// that declares trickplay.
    #[tokio::test]
    async fn only_a_scan_on_a_trickplay_item_decodes_a_tile() {
        let (mut display, _) = display();
        let _ = asks(&mut display, present(r#"{"title":"A Film"}"#));
        let _ = asks(&mut display, message("summon"));
        assert!(asks(&mut display, message("right")).is_empty());
        assert!(display.previewing().is_none());

        let jobs = asks(
            &mut display,
            present(r#"{"title":"A Film","trickplay":"/art/tiles"}"#),
        );
        assert!(display.previewing().is_some());
        assert_eq!(jobs.len(), 1);
        // The directory holds no sheet, so the tile answers nothing and the
        // gate reopens.
        assert!(answer(&mut display, jobs.into_iter().next().unwrap()).is_empty());
        assert!(display.art.tile().is_none());
    }

    /// A chooser hides the logo and the tile, the way a hidden display does.
    #[tokio::test]
    async fn a_chooser_hides_the_pictures_the_osd_carries() {
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

    /// The block the sidecar sends for the work that follows this one.
    const OFFER: &str = r#"{"reason":"Next in Harbor Lights",
        "title":"E05 \u00b7 The Long Tide",
        "detail":"Harbor Lights \u00b7 S02 \u00b7 45 min"}"#;

    fn block(word: &str, text: &str) -> Message {
        Message::Mpv(ipc::Event::Message(vec![
            word.to_string(),
            text.to_string(),
        ]))
    }

    /// The offer arrives on its own message and raises nothing by itself.
    #[tokio::test]
    async fn the_offer_the_sidecar_sends_raises_nothing() {
        let (mut display, _) = display();
        let _ = display.update(block("next", OFFER));
        assert!(display.upnext.available());
        assert!(!display.focus.visible());
        assert_eq!(display.focus.fade().value(), 0.0);

        let _ = display.update(block("next", "{}"));
        assert!(!display.upnext.available());
    }

    /// The position raises the card at the rise, on its own fade and its own
    /// hide window, with the display still down.
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

    /// A hide window a later change replaced takes nothing down.
    #[tokio::test]
    async fn a_stale_card_window_takes_nothing_down() {
        let (mut display, _) = display();
        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("time-pos", json!(5940.0)));
        settle(&mut display);
        let armed = display.card.armed.expect("the rise arms a hide window");

        let _ = display.update(Message::HideCard(armed + 1));
        settle(&mut display);
        assert!(display.upnext.draws_outside(false));
    }

    /// A select on the card asks the sidecar for the next work, and the wait
    /// deadens every press but back.
    #[tokio::test]
    async fn a_select_on_the_card_asks_for_the_next_work() {
        let (mut display, mut commands) = display();
        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("time-pos", json!(5940.0)));
        let _ = display.update(message("summon"));
        let _ = display.update(message("up"));
        assert!(sent(&mut commands).is_empty());

        let _ = display.update(message("select"));
        assert_eq!(
            sent(&mut commands),
            vec!["{\"command\":[\"script-message\",\"liken-next\"],\"request_id\":0}"]
        );
        assert!(display.upnext.waiting());

        let _ = display.update(message("select"));
        assert!(sent(&mut commands).is_empty());
        let _ = display.update(message("back"));
        assert_eq!(
            sent(&mut commands),
            vec!["{\"command\":[\"script-message\",\"liken-exit\"],\"request_id\":0}"]
        );
    }

    /// A new item drops the offer, and the first report of the position the
    /// Play started on does not.
    #[tokio::test]
    async fn a_new_item_drops_the_offer() {
        let (mut display, _) = display();
        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("playlist-pos", json!(0)));
        assert!(display.upnext.available());

        let _ = display.update(property("playlist-pos", json!(1)));
        assert!(!display.upnext.available());
    }

    /// An item whose marks place its intro around the playhead the fixture
    /// film stands at, and its credits in its second half.
    const MARKED: &str = r#"{"title":"A Film","marks":[
        {"kind":"intro","end":1250.0,"source":"theintrodb"},
        {"kind":"intro","start":1100.0,"end":1250.0},
        {"kind":"credits","start":5400.0,"end":5900.0}]}"#;

    /// The skip control comes with the intro and leaves with it, on its own
    /// fade, with the display down.
    #[tokio::test]
    async fn the_skip_control_follows_the_intro_with_the_display_down() {
        let (mut display, _) = display();
        let _ = display.update(present(MARKED));
        settle(&mut display);
        assert!(display.skip.available());
        assert!(display.skip.draws_outside(false));
        assert!(!display.focus.visible());

        let _ = display.update(property("time-pos", json!(1300.0)));
        settle(&mut display);
        assert!(!display.skip.available());
        assert!(!display.skip.draws_outside(false));
    }

    /// An item with no marks offers no skip control anywhere in the film.
    #[tokio::test]
    async fn an_item_with_no_marks_offers_no_skip_control() {
        let (mut display, _) = display();
        let _ = display.update(present(r#"{"title":"A Film"}"#));
        for at in [0.0, 1200.0, 5950.0] {
            let _ = display.update(property("time-pos", json!(at)));
            assert!(!display.skip.available(), "{at}");
        }
    }

    /// The press that wakes the display lands on the control, and the select
    /// after it seeks to the end of the intro. The focus moves to the bar when
    /// the playhead leaves the span.
    #[tokio::test]
    async fn a_wake_and_a_select_skip_the_intro() {
        let (mut display, mut commands) = display();
        let _ = display.update(present(MARKED));
        let _ = display.update(message("down"));
        assert_eq!(display.focus.focused_stop(), Some(Stop::Skip));

        let _ = display.update(message("select"));
        assert_eq!(
            sent(&mut commands),
            vec!["{\"command\":[\"seek\",1250.0,\"absolute+exact\"],\"request_id\":0}"]
        );
        let _ = display.update(property("time-pos", json!(1250.0)));
        assert_eq!(display.focus.focused_stop(), Some(Stop::Fine));
    }

    /// Credits in the second half raise the card at their start, earlier than
    /// the time that remains would.
    #[tokio::test]
    async fn the_credits_raise_the_card_at_their_start() {
        let (mut display, _) = display();
        let _ = display.update(present(MARKED));
        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("time-pos", json!(5399.0)));
        assert_eq!(display.card.armed, None);

        let _ = display.update(property("time-pos", json!(5400.0)));
        assert!(display.card.armed.is_some());
        assert!(display.upnext.showing_card());
    }

    /// In the credits before a scene, the skip control shows and the card
    /// waits. A wake lands on the control, up reaches the offer's chip, and a
    /// select on the control seeks to the scene in the gap between the two
    /// credits spans. The card rises at the credits after the scene.
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
            vec!["{\"command\":[\"seek\",5600.0,\"absolute+exact\"],\"request_id\":0}"]
        );

        let _ = display.update(property("time-pos", json!(5699.0)));
        assert!(!display.upnext.showing_card());
        let _ = display.update(property("time-pos", json!(5700.0)));
        assert!(display.upnext.showing_card());
        assert!(!display.skip.available());
    }

    /// The level shows the row and nothing else on the display.
    #[tokio::test]
    async fn a_level_change_shows_the_row_alone() {
        let (mut display, _) = display();
        let _ = display.update(property("volume", json!(40.0)));
        assert!(!display.volume.showing());

        let _ = display.update(message("volume-changed"));
        settle(&mut display);
        assert!(display.volume.showing());
        assert!(!display.focus.visible());
        assert_eq!(display.focus.fade().value(), 0.0);

        let armed = display.row.armed.expect("a level change arms a window");
        let _ = display.update(Message::HideRow(armed));
        settle(&mut display);
        assert!(!display.volume.showing());
    }

    /// The row draws for a level the observer reported, so a message with no
    /// level behind it draws nothing.
    #[tokio::test]
    async fn a_level_change_with_no_level_draws_nothing() {
        let (mut display, _) = display();
        let _ = display.update(message("volume-changed"));
        settle(&mut display);
        assert!(!display.volume.showing());

        let _ = display.update(property("volume", json!(40.0)));
        assert!(display.volume.showing());
        let _ = display.update(property("mute", json!(true)));
        assert!(display.volume.showing());
    }

    /// The frame tick runs while any of the three fades moves, and the card's
    /// fade moves with the display down.
    #[tokio::test]
    async fn the_frame_tick_runs_for_every_fade() {
        let (mut display, _) = display();
        assert!(!display.fading());

        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("time-pos", json!(5940.0)));
        assert!(display.fading());
        settle(&mut display);
        assert!(!display.fading());

        let _ = display.update(property("volume", json!(40.0)));
        let _ = display.update(message("volume-changed"));
        assert!(display.fading());
        settle(&mut display);
        assert!(!display.fading());
    }

    /// One offer that carries a picture, 800 by 400, which the card's own box
    /// is narrower than.
    fn offer_with_art() -> String {
        format!(
            r#"{{"title":"E05 · The Long Tide","art":"{}"}}"#,
            picture("next", 800, 400)
        )
    }

    #[tokio::test]
    async fn an_offer_with_a_picture_decodes_it() {
        let (mut display, _) = display();
        assert!(asks(&mut display, block("next", OFFER)).is_empty());
        assert_eq!(display.upnext.art(), None);

        let jobs = asks(&mut display, block("next", &offer_with_art()));
        assert!(display.upnext.art().is_some());
        assert_eq!(jobs.len(), 1);
    }

    /// The card takes the offer's picture at the size it draws at, in canvas
    /// units. The picture is wider than its box, so it comes back letterboxed.
    #[tokio::test]
    async fn the_card_takes_the_offers_picture_at_the_size_it_draws_at() {
        let (mut display, _) = display();
        let canvas = display.canvas;
        assert_eq!(upnext::Art::next(&display.bridge(&canvas)), None);

        let jobs = asks(&mut display, block("next", &offer_with_art()));
        let _ = answer(&mut display, jobs.into_iter().next().unwrap());
        assert_eq!(
            upnext::Art::next(&display.bridge(&canvas)),
            Some(Size::new(440.0, 220.0))
        );
    }

    /// One run of positions as mpv pushes them, and how many of them the
    /// reader forwards to the frame loop.
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

    /// One second of pushes as mpv writes them, all reading the same whole
    /// second on the time label.
    fn one_second(from: f64) -> Vec<f64> {
        (0..10)
            .map(|frame| from + f64::from(frame) / 25.0)
            .collect()
    }

    /// mpv pushes a position on every video frame. A run of them inside one
    /// second moves neither the time label nor the playhead of a long film,
    /// so one of them reaches the frame loop and the rest are dropped.
    #[tokio::test]
    async fn a_second_of_position_pushes_reaches_the_frame_loop_once() {
        assert_eq!(paced(true, 6000.0, &one_second(1200.6)), 1);
        assert_eq!(paced(false, 6000.0, &one_second(1200.6)), 1);

        // The second the label reads changes, so that push is forwarded.
        let two: Vec<f64> = one_second(1200.6)
            .into_iter()
            .chain(one_second(1201.6))
            .collect();
        assert_eq!(paced(true, 6000.0, &two), 2);
    }

    /// On a short work the playhead travels a pixel inside one second, so the
    /// pushes that move it are forwarded while the display is up, and the
    /// whole second alone carries them while it is down.
    #[tokio::test]
    async fn a_playhead_that_moves_inside_a_second_is_forwarded() {
        assert!(paced(true, 100.0, &one_second(50.6)) > 1);
        assert_eq!(paced(false, 100.0, &one_second(50.6)), 1);
    }

    /// Every message that is not a position reaches the frame loop.
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

    /// While the display draws nothing under it, a position moves no
    /// signature, so the work of measuring one is not done at all. A summon
    /// puts the signature back.
    #[tokio::test]
    async fn a_position_with_the_display_down_carries_no_signature() {
        let (mut display, _) = display();
        let _ = display.update(property("time-pos", json!(1200.0)));
        assert_eq!(display.signature, None);

        let _ = display.update(message("summon"));
        settle(&mut display);
        let _ = display.update(property("time-pos", json!(1201.0)));
        assert!(display.signature.is_some());
    }

    /// A paused film pushes no position, so the wall clock follows a tick of
    /// its own while the display stands, and that tick rebuilds the layer the
    /// reading draws in.
    #[tokio::test]
    async fn the_wall_clock_follows_a_tick_while_the_display_stands() {
        let (mut display, _) = display();
        let _ = display.update(pause(false));
        let _ = display.update(pause(true));
        settle(&mut display);
        assert!(display.focus.visible());

        // The minute rebuilds the layer under the OSD, where the reading and
        // the end time draw.
        let _ = display.update(Message::Minute);
        assert!(display.focus.visible());
    }

    /// A socket that closes moves nothing, and the reader dials again.
    #[tokio::test]
    async fn a_socket_that_closes_moves_nothing() {
        let (mut display, _) = display();
        let _ = display.update(Message::Mpv(ipc::Event::Detached));
        assert!(!display.focus.visible());
        assert_eq!(display.focus.fade().value(), 0.0);
    }
}
