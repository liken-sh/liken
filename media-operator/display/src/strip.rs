//! The control strip along the bottom of the screen and the track panel
//! that each of its stops opens. The strip has a stop for the audio, one
//! for the subtitles when the file has a subtitle track, and one for the
//! video when the file has more than one video track. Each stop names the
//! track that plays, and the delay when it is not zero. Left and right
//! move the focus across the stops, and select opens the focused stop's
//! panel. While a panel is open, `Focus` sends it every press but back
//! through `Strip::handle`.
//!
//! A panel has two axes. Up and down move the focus through its rows: the
//! timing row at the top, then the tracks. Left and right move the delay
//! one step whichever row has the focus, so no list of tracks is between
//! the viewer and the timing.
//!
//! This module holds the state and the keys. `strip/draw.rs` turns the
//! state into lines and shapes.

mod draw;

use std::cell::{Ref, RefCell};

use iced::Rectangle;

use crate::canvas::Brush;
use crate::film::{self, Film};
use crate::focus::Action;
use crate::ipc::Command;
use crate::listing::{self, Row, Value};
use crate::motion::{self, Glide};
use crate::presentation::Presentation;
use crate::streams::Streams;
use crate::timing::{Delay, Timing};
use crate::track::Track;

/// The order of the stops that left and right walk.
const ORDER: [Track; 3] = [Track::Audio, Track::Subtitles, Track::Video];

/// The row of an open panel that has the focus: the timing row, or the
/// track with this mpv id, `None` being the subtitles' `Off` row. The
/// focus follows the track and not its place, because a select can play
/// a track in another language and the rows then sort again.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum PanelFocus {
    Timing,
    Track(Option<i64>),
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct Open {
    stop: Track,
    focus: PanelFocus,
}

/// The rows of each kind for one `track-list` and one block, which the
/// strip keeps so that a frame of a fade does not name and sort every
/// track again.
#[derive(Debug, Default)]
struct Listings {
    key: Option<(Vec<film::Track>, Streams)>,
    rows: [Vec<Row>; 3],
}

/// The focused stop, the open panel, the delays that a compare set
/// aside, and the glides of the panel's underline and of its scale's mark.
#[derive(Debug, Default)]
pub struct Strip {
    index: usize,
    open: Option<Open>,
    timing: Timing,
    listings: RefCell<Listings>,
    /// The underline's glide. Its x is measured from the centre line, so
    /// it does not depend on the width of the canvas.
    underline: Option<Glide<Rectangle>>,
    mark: Option<Glide<f32>>,
}

/// What the strip shows for one stop: the texts that can name the playing
/// track from the longest down, the delay in words, and whether the stop
/// has the focus or its panel is open.
#[derive(Debug, Clone, PartialEq)]
pub struct StopView {
    pub track: Track,
    pub values: Vec<Value>,
    pub delay: Option<String>,
    pub focused: bool,
    pub open: bool,
}

/// What an open panel shows, and the glides that move its underline and
/// its mark.
#[derive(Debug, Clone, PartialEq)]
pub struct PanelView {
    pub track: Track,
    pub rows: Vec<Row>,
    pub focus: PanelFocus,
    pub timing: Option<TimingView>,
    pub underline: Option<Glide<Rectangle>>,
    pub mark: Option<Glide<f32>>,
}

/// The timing readout and its scale. Each place on the scale is a share
/// from -1 at the sooner end to 1 at the later end.
#[derive(Debug, Clone, PartialEq)]
pub struct TimingView {
    pub reading: String,
    pub share: f32,
    /// Where the delay that the next select sets back is, during a compare.
    pub returns_to: Option<f32>,
    pub hint: Option<String>,
    /// Whether a track of this kind plays. Without one, the readout and
    /// the scale are faint.
    pub active: bool,
    /// Whether the delay is at the end of the range on each side, where a
    /// step that way changes nothing.
    pub at_sooner_end: bool,
    pub at_later_end: bool,
    /// The shares of the scale's ticks, on both sides of zero.
    pub ticks: Vec<f32>,
}

impl PanelView {
    /// The index of the focused row in `rows`, or `None` for the timing
    /// row.
    pub fn focused_row(&self) -> Option<usize> {
        match self.focus {
            PanelFocus::Timing => None,
            PanelFocus::Track(id) => self.rows.iter().position(|row| row.id == id),
        }
    }
}

impl Strip {
    /// The stops this file has, in order. A music item has none, because
    /// an album is one audio stream with nothing to choose and the row
    /// would repeat what the frame already shows.
    pub fn present(presentation: &Presentation, film: &Film) -> Vec<Track> {
        if presentation.is_music() {
            return Vec::new();
        }
        ORDER
            .into_iter()
            .filter(|track| track.available(film))
            .collect()
    }

    pub fn available(presentation: &Presentation, film: &Film) -> bool {
        !Self::present(presentation, film).is_empty()
    }

    /// The stop whose panel is open and receives every press, if any.
    pub fn capturing(&self) -> Option<Track> {
        self.open.map(|open| open.stop)
    }

    /// Closes the open panel. The focus stays on the stop that opened it.
    pub fn close(&mut self) {
        self.open = None;
    }

    /// The rows of one kind, named and sorted once for each `track-list`
    /// and block.
    fn rows<'a>(
        &'a self,
        track: Track,
        presentation: &Presentation,
        film: &Film,
    ) -> Ref<'a, [Row]> {
        let streams = presentation.streams(film);
        let fresh = self
            .listings
            .borrow()
            .key
            .as_ref()
            .is_some_and(|(tracks, block)| *tracks == film.tracks && block == streams);
        if !fresh {
            let mut listings = self.listings.borrow_mut();
            for (at, kind) in ORDER.into_iter().enumerate() {
                listings.rows[at] = listing::rows(kind, film, streams);
            }
            listings.key = Some((film.tracks.clone(), streams.clone()));
        }
        let at = ORDER.iter().position(|kind| *kind == track).unwrap_or(0);
        Ref::map(self.listings.borrow(), |listings| {
            listings.rows[at].as_slice()
        })
    }

    /// The delay that a panel of `track` sets, or `None` for the video and
    /// for a still photo, which has no timeline.
    fn delay(track: Track, presentation: &Presentation) -> Option<Delay> {
        track.delay().filter(|_| !presentation.is_image())
    }

    /// Handles a press while no panel is open. A select opens the focused
    /// stop's panel with the focus on the track that plays.
    pub fn press(
        &mut self,
        action: Action,
        presentation: &Presentation,
        film: &Film,
    ) -> Vec<Command> {
        let present = Self::present(presentation, film);
        if present.is_empty() {
            return Vec::new();
        }
        self.index = self.index.min(present.len() - 1);
        match action {
            Action::Left => self.index = self.index.saturating_sub(1),
            Action::Right => self.index = (self.index + 1).min(present.len() - 1),
            Action::Select => {
                let stop = present[self.index];
                let rows = self.rows(stop, presentation, film);
                let focus = match listing::playing(&rows).or(rows.first()) {
                    Some(row) => PanelFocus::Track(row.id),
                    None => PanelFocus::Timing,
                };
                drop(rows);
                self.open = Some(Open { stop, focus });
                self.underline = None;
                self.mark = None;
            }
            _ => {}
        }
        Vec::new()
    }

    /// The panel's rows in focus order: the timing row when the panel has
    /// one, then each track.
    fn stops_of(&self, stop: Track, presentation: &Presentation, film: &Film) -> Vec<PanelFocus> {
        let timing = Self::delay(stop, presentation).map(|_| PanelFocus::Timing);
        timing
            .into_iter()
            .chain(
                self.rows(stop, presentation, film)
                    .iter()
                    .map(|row| PanelFocus::Track(row.id)),
            )
            .collect()
    }

    /// Handles a press while a panel is open.
    ///
    /// `tick` is the display's frame clock, which starts the underline's
    /// glide when the focus moves.
    pub fn handle(
        &mut self,
        action: Action,
        presentation: &Presentation,
        film: &Film,
        tick: u64,
    ) -> Vec<Command> {
        let (Some(open), Some(view)) = (self.open, self.panel(presentation, film)) else {
            return Vec::new();
        };
        let open = Open {
            focus: view.focus,
            ..open
        };
        let delay = Self::delay(open.stop, presentation);
        match action {
            Action::Up | Action::Down => {
                let order = self.stops_of(open.stop, presentation, film);
                let at = order.iter().position(|focus| *focus == open.focus);
                let next = match (action, at) {
                    (Action::Up, Some(at)) => at.saturating_sub(1),
                    (_, Some(at)) => (at + 1).min(order.len().saturating_sub(1)),
                    (_, None) => 0,
                };
                // The focus that is drawn can differ from the one recorded,
                // when the recorded track left the list, so a press compares
                // with the drawn one and starts no glide to where it is.
                if let (Some(focus), Some(open)) = (order.get(next), self.open.as_mut())
                    && view.focus != *focus
                {
                    open.focus = *focus;
                    // The glide keeps its x from the centre line, so it is
                    // measured on a 16:9 canvas and moved to the line.
                    let centre = crate::theme::CANVAS_WIDTH / 2.0;
                    self.underline = draw::underline(&view, centre, tick).map(|drawn| {
                        let relative = Rectangle {
                            x: drawn.x - centre,
                            ..drawn
                        };
                        Glide::new(self.underline.as_ref(), relative, tick)
                    });
                }
                Vec::new()
            }
            Action::Left | Action::Right => match delay {
                Some(delay) => {
                    let direction = if action == Action::Left { -1.0 } else { 1.0 };
                    self.timing.step(delay, direction, film)
                }
                None => Vec::new(),
            },
            Action::Select => match (open.focus, delay) {
                (PanelFocus::Timing, Some(delay)) => self.timing.compare(delay, film),
                (PanelFocus::Timing, None) => Vec::new(),
                (PanelFocus::Track(id), _) => {
                    let rows = self.rows(open.stop, presentation, film);
                    let row = rows.iter().find(|row| row.id == id).cloned();
                    drop(rows);
                    match row {
                        Some(row) if row.playing => {
                            self.close();
                            Vec::new()
                        }
                        Some(row) => open.stop.play(row.id),
                        None => Vec::new(),
                    }
                }
            },
            Action::Back => Vec::new(),
        }
    }

    /// What the strip shows for each stop.
    pub fn view(&self, presentation: &Presentation, film: &Film, focused: bool) -> Vec<StopView> {
        let present = Self::present(presentation, film);
        let here = focused.then(|| self.index.min(present.len().saturating_sub(1)));
        present
            .into_iter()
            .enumerate()
            .map(|(at, track)| {
                let rows = self.rows(track, presentation, film);
                let playing = listing::playing(&rows);
                let values = match playing {
                    Some(row) => row.values(track),
                    None => vec![Value {
                        name: listing::OFF.to_string(),
                        ..Value::default()
                    }],
                };
                let delay = Self::delay(track, presentation)
                    .filter(|delay| {
                        playing.is_some_and(|row| row.id.is_some()) && delay.active(film)
                    })
                    .and_then(|delay| delay.words(delay.delay(film)));
                StopView {
                    track,
                    values,
                    delay,
                    focused: here == Some(at),
                    open: self.capturing() == Some(track),
                }
            })
            .collect()
    }

    /// What the open panel shows, or `None` when no panel is open.
    pub fn panel(&self, presentation: &Presentation, film: &Film) -> Option<PanelView> {
        let open = self.open?;
        let timing = Self::delay(open.stop, presentation).map(|delay| {
            let at = delay.delay(film);
            TimingView {
                reading: delay.reading(film),
                share: delay.share(at),
                returns_to: self
                    .timing
                    .returns_to(delay, film)
                    .map(|back| delay.share(back)),
                hint: self
                    .timing
                    .hint(delay, film, open.focus == PanelFocus::Timing),
                active: delay.active(film),
                at_sooner_end: delay.at_end(at, -1.0),
                at_later_end: delay.at_end(at, 1.0),
                ticks: delay
                    .graduations()
                    .iter()
                    .flat_map(|value| [delay.share(-value), delay.share(*value)])
                    .collect(),
            }
        });
        let rows = self.rows(open.stop, presentation, film).to_vec();
        // A track that left the list gives the focus to the track that
        // plays, so the panel always shows a focused row.
        let focus = match open.focus {
            PanelFocus::Track(id) if !rows.iter().any(|row| row.id == id) => {
                listing::playing(&rows)
                    .or(rows.first())
                    .map_or(PanelFocus::Timing, |row| PanelFocus::Track(row.id))
            }
            focus => focus,
        };
        Some(PanelView {
            track: open.stop,
            rows,
            focus,
            timing,
            underline: self.underline,
            mark: self.mark,
        })
    }

    /// Starts the mark's glide when mpv reports a new value of the delay
    /// property `name` while its panel is open. `before` is the value that
    /// the mark showed, and the glide starts from where the mark is drawn
    /// on `tick`, so a held key never leaves the mark behind.
    pub fn on_delay(&mut self, name: &str, before: f64, presentation: &Presentation, tick: u64) {
        let Some(delay) = self
            .open
            .and_then(|open| Self::delay(open.stop, presentation))
            .filter(|delay| delay.property() == name)
        else {
            return;
        };
        let drawn = motion::drawn(self.mark.as_ref(), delay.share(before), tick);
        self.mark = Some(Glide::new(self.mark.as_ref(), drawn, tick));
    }

    /// Closes the open panel when the file that mpv reports has no track
    /// of its kind any more, as when the next item of a `Play` has no audio.
    /// Returns whether it closed the panel.
    pub fn on_tracks(&mut self, presentation: &Presentation, film: &Film) -> bool {
        let gone = self
            .open
            .is_some_and(|open| !Self::present(presentation, film).contains(&open.stop));
        if gone {
            self.close();
        }
        gone
    }

    /// Whether the underline or the mark is gliding on `tick`, so the
    /// display asks for another frame.
    pub fn moving(&self, tick: u64) -> bool {
        self.open.is_some()
            && (self.underline.is_some_and(|glide| glide.running(tick))
                || self.mark.is_some_and(|glide| glide.running(tick)))
    }

    /// The top edge of the open panel, or `None` when none is open. The
    /// subtitles rise above it.
    pub fn panel_top(&self, presentation: &Presentation, film: &Film) -> Option<f32> {
        self.panel(presentation, film)
            .map(|view| draw::panel_top(&view))
    }

    /// Draws the strip along the bottom of the screen.
    pub fn draw(
        &self,
        brush: &mut Brush<'_>,
        presentation: &Presentation,
        film: &Film,
        focused: bool,
    ) {
        draw::strip(brush, &self.view(presentation, film, focused));
    }

    /// Draws the darkening under the open panel, if any. The display draws
    /// it in the lower layer, under the scrubber and the strip.
    pub fn draw_shade(&self, brush: &mut Brush<'_>, presentation: &Presentation, film: &Film) {
        if let Some(view) = self.panel(presentation, film) {
            draw::shade(brush, &view);
        }
    }

    /// Draws the open panel, if any, as it is on `tick`.
    pub fn draw_panel(
        &self,
        brush: &mut Brush<'_>,
        presentation: &Presentation,
        film: &Film,
        tick: u64,
    ) {
        if let Some(view) = self.panel(presentation, film) {
            draw::panel(brush, &view, tick);
        }
    }
}

#[cfg(test)]
mod tests;
