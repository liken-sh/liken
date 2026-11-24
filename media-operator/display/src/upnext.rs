//! The up-next offer: the work that follows the current one, as the
//! `Play` names it. The command sidecar sends it to the display as a
//! JSON block in a script-message. The OSD draws the offer as a small
//! chip while the film plays, and as a card near the end of the film.
//! When the viewer selects the card, the display waits for the operator
//! to start the next work.

use iced::{Point, Rectangle, Size};
use jiff::ZonedDateTime;
use serde_json::Value;

mod column;

use crate::canvas::{Anchor, Brush, Canvas, Line, clip, measure};
use crate::clock;
use crate::fade::{Clock, Fade, Hide};
use crate::film::Film;
use crate::theme;

/// The card is 440 canvas pixels wide, with its right edge on the right
/// margin. It is 458 pixels tall (the art and four text rows), so it
/// spans y 338 to 796. That ends above the row where the scrubber draws
/// its time label, so the card never covers the scrubber.
pub const CARD_W: f32 = 440.0;
const CARD_TOP: f32 = 338.0;
pub const ART_H: f32 = 248.0;
const PAD_X: f32 = 22.0;
const PAD_Y: f32 = 16.0;
pub(crate) const CARD_R: f32 = 14.0;
/// The fill of the card when it is not focused. It is dark enough to
/// read the text over a bright frame. A focused card uses the darker
/// `theme::alpha::PANEL`.
pub(crate) const CARD_ALPHA: f32 = theme::opacity(0x54);
/// The distance from the top of each text row of the card to the top of
/// the next row, sized for the type of that row.
const REASON_PITCH: f32 = 40.0;
const TITLE_PITCH: f32 = 54.0;
const DETAIL_PITCH: f32 = 42.0;
const ENDS_H: f32 = 42.0;
/// The card keeps a row for every line, even when the block leaves the
/// line out, so every offer draws a card of the same size.
const CARD_H: f32 = ART_H + PAD_Y + REASON_PITCH + TITLE_PITCH + DETAIL_PITCH + ENDS_H + PAD_Y;

/// The chip is on the row of the scrubber's time label, at the right end.
/// `CHIP_Y` is the top of the label's line box and `CHIP_H` is the height
/// of that line box. The box around the label spans y 824 to 870, with
/// the same padding above and below the line. The skip control uses the
/// same dimensions.
pub(crate) const CHIP_Y: f32 = 833.0;
pub(crate) const CHIP_H: f32 = theme::type_scale::TINY;
pub(crate) const CHIP_PAD_X: f32 = 22.0;
pub(crate) const CHIP_PAD_Y: f32 = 9.0;
pub const CHIP_TOP: f32 = CHIP_Y - CHIP_PAD_Y;
/// Half the box height, so the chip has round ends and reads as a
/// button. A panel uses the smaller `CARD_R`.
pub(crate) const CHIP_R: f32 = (CHIP_H + 2.0 * CHIP_PAD_Y) / 2.0;

/// For a film with no credits mark, the card rises when 4% of the film
/// or 240 seconds remain, whichever is less.
///
/// A viewer who takes the offer stops this work, so the card must not
/// rise before the work counts as watched. The watched rule, in
/// `watched.go` of `library-operator` and in
/// `media-browser/src/catalog/progress/watched.rs`, counts a work as
/// watched when 5% or 300 seconds remain, whichever is less. Both
/// amounts here are smaller than that rule's.
const RISE_PERCENT: f64 = 4.0;
const RISE_SECONDS: f64 = 240.0;

/// The playback second at which the card rises for a film.
pub(crate) fn time_rise(duration: f64) -> f64 {
    duration - (duration * RISE_PERCENT / 100.0).min(RISE_SECONDS)
}

/// For an episode with no credits mark, the card rises when 2.5% of the
/// episode or 120 seconds remain, whichever is less. 4% of a short
/// episode would raise the card too early. Both amounts are smaller than
/// the watched rule's, for the reason at `RISE_PERCENT`.
const SERIES_RISE_PERCENT: f64 = 2.5;
const SERIES_RISE_SECONDS: f64 = 120.0;

/// The playback second at which the card rises for an episode.
pub(crate) fn series_rise(duration: f64) -> f64 {
    duration - (duration * SERIES_RISE_PERCENT / 100.0).min(SERIES_RISE_SECONDS)
}

/// The chip's text before the title. The sidecar's block has text only
/// for the card.
const CHIP_WORD: &str = "Up next";

/// The offer's picture, which the image decoder supplies. `next` returns
/// the size of the decoded picture, or `None` when there is none to
/// draw. The card then calls `draw` with the rectangle to draw it in.
pub trait Art {
    fn next(&self) -> Option<Size>;

    fn draw(&self, brush: &mut Brush<'_>, bounds: Rectangle);
}

/// The runtime of the next work and the time it would end if it started
/// at `now`, such as "46m · ends at 11:15 pm". Under a minute, the line
/// is the end time alone. Returns `None` when the offer has no
/// `remaining`. The card and the column both draw this line.
fn ends_at(offer: &Offer, now: &ZonedDateTime) -> Option<String> {
    let seconds = offer.remaining?;
    let ends = clock::reading(&now.saturating_add(jiff::Span::new().seconds(seconds)));
    Some(match clock::runtime(seconds) {
        Some(runtime) => format!("{runtime} \u{b7} ends at {ends}"),
        None => format!("Ends at {ends}"),
    })
}

/// The block the sidecar sent. A block with no `reason`, `title`,
/// `detail`, or `art` is not an offer.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Offer {
    pub reason: Option<String>,
    pub title: Option<String>,
    pub detail: Option<String>,
    pub art: Option<String>,
    /// How many seconds the next work plays from its start. The card adds
    /// it to the wall clock each time it draws, to get the time the next
    /// work would end.
    pub remaining: Option<i64>,
}

impl Offer {
    fn parse(text: &str) -> Option<Self> {
        let block = serde_json::from_str::<Value>(text).ok()?;
        // An empty string counts as a missing field, as in every other
        // presentation block, so a block of empty strings is no offer.
        let text_field = |name: &str| {
            block
                .get(name)
                .and_then(Value::as_str)
                .filter(|text| !text.is_empty())
                .map(str::to_string)
        };
        let offer = Self {
            reason: text_field("reason"),
            title: text_field("title"),
            detail: text_field("detail"),
            art: text_field("art"),
            remaining: None,
        };
        // `remainingSeconds` alone gives the card nothing to show, so it
        // does not make a block an offer.
        if offer == Self::default() {
            return None;
        }
        Some(Self {
            remaining: block
                .get("remainingSeconds")
                .and_then(Value::as_i64)
                .filter(|seconds| *seconds >= 0),
            ..offer
        })
    }
}

/// The offer and the state of its chip, card, and column.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct UpNext {
    offer: Option<Offer>,
    /// Whether the playhead has reached the rise point.
    risen: bool,
    /// Whether a select on the chip has expanded it into the card. The
    /// expansion lasts until the focus leaves the offer or the OSD hides.
    expanded: bool,
    /// Whether the viewer has taken the offer. The display then waits for
    /// the operator to end this `Play` and start the next one.
    waiting: bool,
    /// The card's fade and hide timer, used when the OSD is hidden.
    clock: Clock,
    /// The last playlist position that mpv reported. A different position
    /// clears the offer, but the first report does not.
    at_item: Option<i64>,
    /// Whether the compositor has resized the film to a smaller frame. The
    /// offer then draws as a column beside the film, in place of the
    /// corner card.
    stepped: bool,
    /// The column's own fade. It starts when the film reaches its new
    /// frame, because the display draws no frames while the compositor is
    /// moving the film.
    column: Fade,
}

impl UpNext {
    /// The card's fade, which the frame loop steps while it is running.
    pub fn fade(&self) -> &Fade {
        self.clock.fade()
    }

    pub fn fade_mut(&mut self) -> &mut Fade {
        self.clock.fade_mut()
    }

    /// Returns the change to the hide timer that this state needs, and
    /// resets it. The frame loop owns the timer and applies the change.
    pub fn take_hide(&mut self) -> Hide {
        self.clock.take_hide()
    }

    /// The hide timer ran out, so the card fades out.
    pub fn hide(&mut self) {
        self.clock.hide();
    }

    /// Replaces the offer with the one in `text`, a JSON block. A block
    /// that is not an offer, such as an empty object, clears the current
    /// offer.
    pub fn receive(&mut self, text: &str) {
        self.reset();
        self.offer = Offer::parse(text);
    }

    /// Clears the offer when the playlist moves to another item, because
    /// the offer is for the item that the `Play` started on. mpv reports
    /// the position once when the display starts observing it. That first
    /// report is the item already playing, so it does not clear the offer.
    pub fn on_playlist_pos(&mut self, value: Option<i64>) {
        let Some(value) = value else {
            return;
        };
        if self.at_item.is_some_and(|at| at != value) && self.offer.is_some() {
            self.reset();
            self.offer = None;
        }
        self.at_item = Some(value);
    }

    /// Raises the card when the playhead reaches `rise`, the playback
    /// second of the start of the credits, or of `time_rise` or
    /// `series_rise` when the item has no credits mark. With no `rise`,
    /// the card never rises, and once it has risen, a seek back does not
    /// lower it. mpv sends `percent-pos` on every change, so no timer is
    /// needed.
    ///
    /// Returns true only on the call that raises the card, because no
    /// other update of `percent-pos` changes what the offer draws.
    pub fn on_percent(&mut self, value: Option<f64>, film: &Film, rise: Option<f64>) -> bool {
        let Some(value) = value else {
            return false;
        };
        if self.offer.is_none() || self.risen {
            return false;
        }
        let Some(duration) = film.duration.filter(|duration| *duration > 0.0) else {
            return false;
        };
        let Some(rise) = rise else {
            return false;
        };
        if duration * value / 100.0 < rise {
            return false;
        }
        self.risen = true;
        self.clock.show(Hide::Arm);
        true
    }

    /// Called after the display asks the compositor to shrink the film
    /// into a smaller frame at the rise. The offer then draws as a
    /// column beside the film, from `landed` on. The corner card's hide
    /// timer is cancelled, because the column stays until the viewer
    /// presses a key or the file ends.
    pub fn step_back(&mut self) {
        self.stepped = true;
        self.column = Fade::default();
        self.clock.request_hide(Hide::Cancel);
        // A running card fade would tick and redraw frames while the
        // compositor moves the film. The column replaces the card, so the
        // card's fade jumps to its end.
        self.clock.fade_mut().finish();
    }

    /// Called when the film has reached its new frame. The column fades
    /// in, unless the viewer has already taken the offer.
    pub fn landed(&mut self) {
        if self.stepped && !self.waiting {
            self.column.to(1.0);
        }
    }

    /// Called when a back press returns the film to the full region. The
    /// column and the card disappear at once, before the film moves. The
    /// offer stays in the OSD, and the card does not rise again.
    pub fn restore(&mut self) {
        self.stepped = false;
        self.column = Fade::default();
        self.clock = Clock::default();
        self.clock.request_hide(Hide::Cancel);
    }

    /// Called when the compositor puts the film back in the full region
    /// without a request from the display. The offer returns to the corner
    /// card, which hides after the usual timeout.
    pub fn fall_back(&mut self) {
        self.stepped = false;
        self.column = Fade::default();
        if self.risen && !self.waiting {
            self.clock.show(Hide::Arm);
        }
    }

    pub fn stepped_back(&self) -> bool {
        self.stepped
    }

    /// The column's fade, which the frame loop steps while it is running.
    pub fn column(&self) -> &Fade {
        &self.column
    }

    pub fn column_mut(&mut self) -> &mut Fade {
        &mut self.column
    }

    /// The size of the box for the offer's picture: the column's while the
    /// film is stepped back, and the card's otherwise. The decoder scales
    /// the picture to this box.
    pub fn art_box(&self) -> Size {
        match self.stepped {
            true => column::ART,
            false => Size::new(CARD_W, ART_H),
        }
    }

    /// The left edge of the column or the card, where the cast row must
    /// end.
    pub fn left(&self, canvas: &Canvas) -> f32 {
        match self.stepped {
            true => column::left(canvas),
            false => card_x(canvas),
        }
    }

    /// Whether there is an offer for the focus to stop on.
    pub fn available(&self) -> bool {
        self.offer.is_some()
    }

    pub fn waiting(&self) -> bool {
        self.waiting
    }

    /// The offer's art reference, which the display decodes into the art
    /// box. `None` when the offer has no art.
    pub fn art(&self) -> Option<&str> {
        self.offer.as_ref()?.art.as_deref()
    }

    /// Whether the offer draws as the card, risen or expanded. Only a
    /// select on the card takes the offer, so the first select on the
    /// chip expands it.
    pub fn showing_card(&self) -> bool {
        self.risen || self.expanded
    }

    pub fn expand(&mut self) {
        self.expanded = true;
    }

    /// Returns an expanded offer to the chip when the focus leaves the
    /// offer or the OSD hides. A risen card stays a card.
    pub fn collapse(&mut self) {
        self.expanded = false;
    }

    /// Records that the viewer selected the offer. The column, or the
    /// corner card when the film is not stepped back, fades out over
    /// `theme::HAND_OFF`. The display then waits for the operator to end
    /// this `Play` and start the next. Nothing in this type ends the wait.
    /// It ends when mpv quits or a back press ends the run.
    ///
    /// `osd` is the OSD's current fade value. A card that the OSD drew
    /// fades out on its own clock from that value, so it does not vanish
    /// at once.
    pub fn take(&mut self, osd: f32) {
        if self.offer.is_none() {
            return;
        }
        self.waiting = true;
        self.clock.request_hide(Hide::Cancel);
        let card = self.clock.fade_mut();
        if self.stepped {
            // `step_back` jumped the card's fade to its end. Setting it to
            // clear keeps the corner card hidden if the compositor
            // returns the film during the wait.
            card.to(0.0);
            card.finish();
            self.column.exit(theme::HAND_OFF);
            return;
        }
        card.to(card.value().max(osd));
        card.finish();
        card.exit(theme::HAND_OFF);
    }

    /// Clears the state of the previous offer.
    fn reset(&mut self) {
        self.risen = false;
        self.expanded = false;
        self.waiting = false;
        self.clock = Clock::default();
        self.clock.request_hide(Hide::Cancel);
        self.stepped = false;
        self.column = Fade::default();
    }

    /// The chip's text: `CHIP_WORD` followed by the offer's title.
    fn label(&self) -> String {
        let title = self
            .offer
            .as_ref()
            .and_then(|offer| offer.title.as_deref())
            .unwrap_or_default();
        format!("{CHIP_WORD}  {title}")
    }

    /// The panel behind the focused chip, sized to its label.
    fn chip_panel(&self, canvas: &Canvas) -> Rectangle {
        let width = measure(&self.label(), theme::type_scale::TINY) + 2.0 * CHIP_PAD_X;
        Rectangle::new(
            Point::new(canvas.right() + CHIP_PAD_X - width, CHIP_Y - CHIP_PAD_Y),
            Size::new(width, CHIP_H + 2.0 * CHIP_PAD_Y),
        )
    }

    /// The label, centered vertically on the panel.
    fn chip_line(&self, canvas: &Canvas, focused: bool) -> Line {
        Line::new(
            self.label(),
            Point::new(canvas.right(), self.chip_panel(canvas).center().y),
            Anchor::Right,
            theme::type_scale::TINY,
            if focused {
                theme::color::text()
            } else {
                theme::color::muted()
            },
        )
    }

    /// The card's rectangle, with its right edge on the right margin.
    fn card_box(&self, canvas: &Canvas) -> Rectangle {
        Rectangle::new(
            Point::new(card_x(canvas), CARD_TOP),
            Size::new(CARD_W, CARD_H),
        )
    }

    /// The rectangle at the top of the card that the art is drawn in.
    fn art_plate(&self, canvas: &Canvas) -> Rectangle {
        Rectangle::new(
            Point::new(card_x(canvas), CARD_TOP),
            Size::new(CARD_W, ART_H),
        )
    }

    /// The text rows of the card, each clipped to the width inside the
    /// padding: `reason`, `title`, `detail`, and the end time computed
    /// from the wall clock `now`.
    fn card_lines(&self, canvas: &Canvas, now: &ZonedDateTime) -> Vec<Line> {
        let Some(offer) = self.offer.as_ref() else {
            return Vec::new();
        };
        let x = card_x(canvas) + PAD_X;
        let room = CARD_W - 2.0 * PAD_X;
        let mut lines = Vec::new();
        let mut y = CARD_TOP + ART_H + PAD_Y;
        if let Some(reason) = offer.reason.as_deref() {
            lines.push(Line::new(
                clip(&reason.to_uppercase(), theme::type_scale::TINY, room),
                Point::new(x, y),
                Anchor::TopLeft,
                theme::type_scale::TINY,
                theme::color::muted(),
            ));
        }
        y += REASON_PITCH;
        if let Some(title) = offer.title.as_deref() {
            lines.push(Line::new(
                clip(title, theme::type_scale::LABEL, room),
                Point::new(x, y),
                Anchor::TopLeft,
                theme::type_scale::LABEL,
                theme::color::text(),
            ));
        }
        y += TITLE_PITCH;
        if let Some(detail) = offer.detail.as_deref() {
            lines.push(Line::new(
                clip(detail, theme::type_scale::SMALL, room),
                Point::new(x, y),
                Anchor::TopLeft,
                theme::type_scale::SMALL,
                theme::color::muted(),
            ));
        }
        y += DETAIL_PITCH;
        if let Some(ends) = ends_at(offer, now) {
            lines.push(Line::new(
                clip(&ends, theme::type_scale::SMALL, room),
                Point::new(x, y),
                Anchor::TopLeft,
                theme::type_scale::SMALL,
                theme::color::muted(),
            ));
        }
        lines
    }

    /// Where the art draws: centered on the art plate at the size the
    /// decoder returned, on the output pixel grid, and never past the left
    /// or top edge of the screen.
    fn art_bounds(&self, canvas: &Canvas, size: Size) -> Rectangle {
        let plate = self.art_plate(canvas);
        Rectangle::new(
            Point::new(
                canvas.snap(plate.center().x - size.width / 2.0).max(0.0),
                canvas.snap(plate.center().y - size.height / 2.0).max(0.0),
            ),
            size,
        )
    }

    fn chip(&self, brush: &mut Brush<'_>, focused: bool) {
        let canvas = brush.canvas();
        if focused {
            brush.panel_rounded(self.chip_panel(&canvas), CHIP_R);
        }
        brush.text(self.chip_line(&canvas, focused));
    }

    /// The card: the art above the text rows.
    fn card(&self, brush: &mut Brush<'_>, focused: bool, art: &dyn Art, now: &ZonedDateTime) {
        let canvas = brush.canvas();
        let box_ = self.card_box(&canvas);
        if focused {
            brush.panel(box_);
        } else {
            brush.rounded(box_, CARD_R, theme::at(theme::color::SHADOW, CARD_ALPHA));
        }
        brush.rect(
            self.art_plate(&canvas),
            theme::at(theme::color::SHADOW, theme::alpha::PANEL),
        );
        if let Some(size) = art.next() {
            art.draw(brush, self.art_bounds(&canvas, size));
        }
        for line in self.card_lines(&canvas, now) {
            brush.text(line);
        }
    }

    /// Whether the OSD draws the chip: there is an offer that has not
    /// risen or expanded, has not been taken, and the film is not stepped
    /// back.
    pub fn chip_showing(&self) -> bool {
        self.offer.is_some() && !self.waiting && !self.stepped && !self.showing_card()
    }

    /// Draws the offer as part of the OSD, as the chip before the rise and
    /// as the card after it, with the OSD's fade. The OSD draws nothing
    /// for a taken offer, which fades on its own clock, or for a
    /// stepped-back film, which has the column.
    pub fn draw(&self, brush: &mut Brush<'_>, focused: bool, art: &dyn Art, now: &ZonedDateTime) {
        if self.chip_showing() {
            self.chip(brush, focused);
        } else if self.offer.is_some() && !self.waiting && !self.stepped {
            self.card(brush, focused, art, now);
        }
    }

    /// Whether a select over the bare video takes the offer: the card has
    /// risen, the OSD is hidden, the offer is not taken yet, and the card
    /// has not finished fading out.
    pub fn risen_outside(&self, osd_visible: bool) -> bool {
        self.offer.is_some() && !osd_visible && self.risen && !self.waiting && self.fade().shown()
    }

    /// Draws the offer over the bare video with its own fade: the column
    /// beside a stepped-back film, the risen card until its hide timer
    /// runs out, and a taken card while it fades out. Nothing of the card
    /// stays after the fade, so no mark covers the rest of the film.
    /// `focused` draws the focus panel, and a taken card or column keeps
    /// that panel while it fades.
    pub fn draw_outside(
        &self,
        brush: &mut Brush<'_>,
        osd_visible: bool,
        focused: bool,
        art: &dyn Art,
        now: &ZonedDateTime,
    ) {
        if !self.draws_outside(osd_visible) {
            return;
        }
        if self.stepped {
            self.draw_column(brush, focused, art, now);
            return;
        }
        let focused = focused || self.waiting;
        brush.at_fade(self.fade().value(), |brush| {
            self.card(brush, focused, art, now);
        });
    }

    /// Whether the offer has anything to draw over the bare video. The
    /// frame loop uses it to skip the layer when the card is not visible.
    pub fn draws_outside(&self, osd_visible: bool) -> bool {
        if self.offer.is_none() {
            return false;
        }
        if self.stepped {
            return self.column.value() > 0.0;
        }
        if self.waiting {
            return self.fade().value() > 0.0;
        }
        !osd_visible && self.risen && self.fade().value() > 0.0
    }

    /// Draws the column with its own fade, whether or not the OSD is
    /// showing.
    fn draw_column(
        &self,
        brush: &mut Brush<'_>,
        focused: bool,
        art: &dyn Art,
        now: &ZonedDateTime,
    ) {
        let Some(offer) = self.offer.as_ref() else {
            return;
        };
        brush.at_fade(self.column.value(), |brush| {
            column::draw(brush, offer, focused || self.waiting, art, now);
        });
    }
}

/// The left edge of the card, where the cast row must end.
pub fn card_x(canvas: &Canvas) -> f32 {
    canvas.right() - CARD_W
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::canvas::ELLIPSIS;
    use serde_json::json;

    /// The block the sidecar sends for an episode, with the three text
    /// lines of the card.
    const OFFER: &str = r#"{"reason":"Next in Harbor Lights · S02",
        "title":"E05 · The Long Tide",
        "detail":"Harbor Lights · S02 · 45 min",
        "remainingSeconds":2700}"#;

    /// Canvas coordinates from the design: the right margin and the left
    /// edges of the card and of its text.
    const RIGHT: f32 = 1824.0;
    const CARD_X: f32 = 1384.0;
    const TEXT_X: f32 = 1406.0;
    /// The row where the scrubber's own text starts, which the offer must
    /// stay above.
    const FLOOR: f32 = 844.0;

    fn film(duration: f64) -> Film {
        let mut film = Film::default();
        film.apply("duration", &json!(duration));
        film
    }

    /// An offer whose card has risen, as when the film reaches its credits.
    fn risen() -> UpNext {
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);
        assert!(upnext.on_percent(Some(98.0), &film(100.0), Some(97.0)));
        upnext
    }

    fn canvas() -> Canvas {
        Canvas::default()
    }

    /// 1970-01-01 22:30 UTC, the wall clock for most tests of the card's
    /// lines.
    fn evening() -> ZonedDateTime {
        crate::clock::at(22 * 3600 + 30 * 60)
    }

    #[test]
    fn no_offer_draws_nothing_and_offers_no_stop() {
        let upnext = UpNext::default();
        assert!(!upnext.available());
        assert!(!upnext.draws_outside(false));
        assert!(upnext.card_lines(&canvas(), &evening()).is_empty());
    }

    #[test]
    fn an_empty_block_is_no_offer() {
        for text in ["{}", "", "not json", "[]", r#"{"request":{"id":"x"}}"#] {
            let mut upnext = UpNext::default();
            upnext.receive(text);
            assert!(
                !upnext.available(),
                "the payload {text:?} makes the offer available, want unavailable"
            );
        }
    }

    /// An empty string counts as a missing field, and `remainingSeconds`
    /// alone is not an offer.
    #[test]
    fn a_block_of_empty_fields_is_no_offer() {
        for text in [
            r#"{"title":""}"#,
            r#"{"reason":"","title":"","detail":"","art":""}"#,
            r#"{"remainingSeconds":2700}"#,
        ] {
            let mut upnext = UpNext::default();
            upnext.receive(text);
            assert!(
                !upnext.available(),
                "the payload {text:?} makes the offer available, want unavailable"
            );
        }

        let mut upnext = UpNext::default();
        upnext.receive(r#"{"title":"","detail":"45 min"}"#);
        assert!(upnext.available());
        assert_eq!(upnext.label(), "Up next  ");
    }

    /// A block with any one of the four text fields is an offer.
    #[test]
    fn a_block_with_one_field_is_an_offer() {
        for text in [
            r#"{"reason":"Next up"}"#,
            r#"{"title":"E05"}"#,
            r#"{"detail":"45 min"}"#,
            r#"{"art":"/run/liken/art/next.bgra"}"#,
        ] {
            let mut upnext = UpNext::default();
            upnext.receive(text);
            assert!(
                upnext.available(),
                "the payload {text:?} leaves the offer unavailable, want available"
            );
        }
    }

    #[test]
    fn the_chip_reads_up_next_and_the_title_right_aligned() {
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);

        let line = upnext.chip_line(&canvas(), false);
        assert_eq!(line.content, "Up next  E05 \u{b7} The Long Tide");
        assert_eq!(line.at, Point::new(RIGHT, 847.0));
        assert_eq!(line.anchor, Anchor::Right);
        assert_eq!(line.size, theme::type_scale::TINY);
        assert_eq!(line.color, theme::color::muted());
    }

    #[test]
    fn the_focused_chip_uses_the_body_text_color() {
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);
        assert_eq!(
            upnext.chip_line(&canvas(), true).color,
            theme::color::text()
        );
    }

    /// The panel ends one padding past the right margin, and its width is
    /// the label's width plus the padding on both sides.
    #[test]
    fn the_chip_panel_is_as_wide_as_its_label() {
        let mut upnext = UpNext::default();
        upnext.receive(r#"{"title":"The Hound of the Baskervilles: Part Two"}"#);

        let panel = upnext.chip_panel(&canvas());
        let label = measure(
            "Up next  The Hound of the Baskervilles: Part Two",
            theme::type_scale::TINY,
        );
        assert!((panel.width - (label + 44.0)).abs() < 0.01);
        assert!((panel.x - (RIGHT + 22.0 - panel.width)).abs() < 0.01);
        assert_eq!(panel.y, 824.0);
        assert_eq!(panel.height, 46.0);
        assert_eq!(
            panel.center().y,
            upnext.chip_line(&canvas(), false).at.y,
            "the label is centered in the panel"
        );
    }

    #[test]
    fn the_chip_corner_radius_is_half_its_height() {
        assert_eq!(CHIP_R, 23.0);
        assert_eq!(CHIP_H, theme::type_scale::TINY);
    }

    /// A longer title moves the panel's left edge left by the added
    /// width, and the right edge does not move.
    #[test]
    fn the_chip_panel_grows_leftward_with_the_label() {
        let mut short = UpNext::default();
        short.receive(r#"{"title":"E05"}"#);
        let mut long = UpNext::default();
        long.receive(r#"{"title":"E05 · The Long Tide"}"#);

        let (short, long) = (short.chip_panel(&canvas()), long.chip_panel(&canvas()));
        assert!(long.x < short.x);
        assert!((long.x + long.width - (short.x + short.width)).abs() < 0.01);
    }

    /// The chip ends above the row of the scrubber's time label.
    #[test]
    fn the_chip_clears_the_time_label_row() {
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);
        let panel = upnext.chip_panel(&canvas());
        assert!(panel.y + panel.height <= 870.0);
    }

    /// The card rises at whichever amount leaves less time. A four-hour film
    /// raises the card four minutes before its end, not at 4% of its
    /// length, and a short film raises it at 96%.
    #[test]
    fn a_film_raises_the_card_when_4_percent_or_240_seconds_remain() {
        let cases = [
            ("a 100 second film", 100.0, 96.0),
            ("a 90 minute film", 5400.0, 5184.0),
            ("a 100 minute film", 6000.0, 5760.0),
            ("a four hour film", 14400.0, 14160.0),
        ];
        for (name, duration, want) in cases {
            assert!((time_rise(duration) - want).abs() < 1e-9, "{name}");
        }
    }

    /// The card rises when the playhead reaches the second that
    /// `on_percent` receives.
    #[test]
    fn the_card_rises_at_its_second() {
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);
        assert!(!upnext.on_percent(Some(96.0), &film(100.0), Some(97.0)));
        assert!(!upnext.showing_card());
        assert!(upnext.on_percent(Some(97.0), &film(100.0), Some(97.0)));
        assert!(upnext.showing_card());
    }

    /// The card of an episode rises when 2.5% of the episode or two
    /// minutes remain, whichever is less.
    #[test]
    fn an_episode_raises_the_card_when_2_5_percent_or_120_seconds_remain() {
        let cases = [
            ("a 22 minute episode", 1320.0, 1287.0),
            ("a 45 minute episode", 2700.0, 2632.5),
            ("an 80 minute episode", 4800.0, 4680.0),
            ("a 100 minute episode", 6000.0, 5880.0),
        ];
        for (name, duration, want) in cases {
            assert!((series_rise(duration) - want).abs() < 1e-9, "{name}");
        }
    }

    /// The watched rule from `watched.go` in `library-operator` and
    /// `media-browser/src/catalog/progress/watched.rs`: a work counts as
    /// watched when 5% of it or five minutes remain, whichever is less.
    fn watched_line(duration: f64) -> f64 {
        duration - (duration * 0.05).min(300.0)
    }

    /// A viewer who takes the offer leaves the work, so a card that rose
    /// before the watched line would leave the work unwatched. Both time
    /// rules rise at or after that line, at every length.
    #[test]
    fn neither_rule_raises_the_card_before_the_work_counts_as_watched() {
        let durations = [
            60.0, 600.0, 1320.0, 2700.0, 4800.0, 5400.0, 6000.0, 7200.0, 10800.0, 14400.0,
        ];
        for duration in durations {
            let watched = watched_line(duration);
            assert!(
                time_rise(duration) >= watched,
                "time_rise for a film of {duration} s is below {watched}, want at least that"
            );
            assert!(
                series_rise(duration) >= watched,
                "series_rise for an episode of {duration} s is below {watched}, want at least that"
            );
        }
    }

    /// A work with no duration, an update with no number, and a missing
    /// rise point never raise the card.
    #[test]
    fn a_work_with_no_length_never_raises_the_card() {
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);
        assert!(!upnext.on_percent(Some(99.0), &Film::default(), Some(97.0)));
        assert!(!upnext.on_percent(Some(99.0), &film(0.0), Some(97.0)));
        assert!(!upnext.on_percent(None, &film(100.0), Some(97.0)));
        assert!(!upnext.on_percent(Some(99.0), &film(100.0), None));
        assert!(!upnext.showing_card());
    }

    #[test]
    fn a_seek_back_keeps_the_card_and_the_hide_timer_arms_once() {
        let mut upnext = risen();
        assert_eq!(upnext.take_hide(), Hide::Arm);
        assert!(!upnext.on_percent(Some(20.0), &film(100.0), Some(97.0)));
        assert!(upnext.showing_card());
        assert_eq!(upnext.take_hide(), Hide::Keep);
    }

    /// Credits in the second half of the film set the rise point to their
    /// start, whether that is earlier or later than the time rule.
    #[test]
    fn credits_raise_the_card_at_their_start() {
        let cases = [
            ("credits before the four minutes", 5400.0, 89.9, 90.0),
            ("credits inside the last four minutes", 5950.0, 99.0, 99.2),
        ];
        let long = film(6000.0);
        for (name, credits, before, at) in cases {
            let mut upnext = UpNext::default();
            upnext.receive(OFFER);
            assert!(
                !upnext.on_percent(Some(before), &long, Some(credits)),
                "{name}"
            );
            assert!(upnext.on_percent(Some(at), &long, Some(credits)), "{name}");
            assert!(upnext.showing_card(), "{name}");
        }
    }

    #[test]
    fn a_card_raised_by_the_credits_stays_after_a_seek_back() {
        let long = film(6000.0);
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);
        assert!(upnext.on_percent(Some(90.0), &long, Some(5400.0)));
        assert!(!upnext.on_percent(Some(10.0), &long, Some(5400.0)));
        assert!(upnext.showing_card());
    }

    #[test]
    fn a_position_with_no_offer_raises_nothing() {
        let mut upnext = UpNext::default();
        assert!(!upnext.on_percent(Some(99.0), &film(100.0), Some(97.0)));
    }

    #[test]
    fn the_card_box_ends_at_the_right_margin_above_the_scrubber() {
        let upnext = risen();
        let box_ = upnext.card_box(&canvas());
        assert_eq!(box_.x, CARD_X);
        assert_eq!(box_.y, 338.0);
        assert_eq!(box_.width, 440.0);
        assert_eq!(box_.height, 458.0);
        assert_eq!(box_.x + box_.width, RIGHT);
        assert!(box_.y + box_.height <= FLOOR);
    }

    #[test]
    fn the_art_plate_covers_the_top_of_the_card() {
        let upnext = risen();
        let plate = upnext.art_plate(&canvas());
        assert_eq!(plate.x, CARD_X);
        assert_eq!(plate.y, 338.0);
        assert_eq!(plate.width, 440.0);
        assert_eq!(plate.height, 248.0);
    }

    /// The first row starts one padding below the art, and each row
    /// starts one pitch below the row above it.
    #[test]
    fn the_card_stacks_its_four_lines_under_the_art() {
        let upnext = risen();
        let lines = upnext.card_lines(&canvas(), &evening());

        assert_eq!(lines.len(), 4);
        assert_eq!(lines[0].content, "NEXT IN HARBOR LIGHTS \u{b7} S02");
        assert_eq!(lines[0].at, Point::new(TEXT_X, 602.0));
        assert_eq!(lines[0].size, theme::type_scale::TINY);
        assert_eq!(lines[0].color, theme::color::muted());

        assert_eq!(lines[1].content, "E05 \u{b7} The Long Tide");
        assert_eq!(lines[1].at, Point::new(TEXT_X, 642.0));
        assert_eq!(lines[1].size, theme::type_scale::LABEL);
        assert_eq!(lines[1].color, theme::color::text());

        assert_eq!(lines[2].content, "Harbor Lights \u{b7} S02 \u{b7} 45 min");
        assert_eq!(lines[2].at, Point::new(TEXT_X, 696.0));
        assert_eq!(lines[2].size, theme::type_scale::SMALL);
        assert_eq!(lines[2].color, theme::color::muted());

        assert_eq!(lines[3].content, "45m \u{b7} ends at 11:15 pm");
        assert_eq!(lines[3].at, Point::new(TEXT_X, 738.0));
        assert_eq!(lines[3].size, theme::type_scale::SMALL);
        assert_eq!(lines[3].color, theme::color::muted());
        for line in &lines {
            assert_eq!(line.anchor, Anchor::TopLeft);
        }
    }

    /// A block with one line draws only that line, on its own row.
    #[test]
    fn a_line_the_block_leaves_out_draws_nothing() {
        let mut upnext = UpNext::default();
        upnext.receive(r#"{"title":"E05"}"#);
        let lines = upnext.card_lines(&canvas(), &evening());
        assert_eq!(lines.len(), 1);
        assert_eq!(lines[0].at.y, 642.0);
    }

    /// The line is the remaining seconds as hours and minutes, then the
    /// wall clock plus those seconds in the clock's format. The time wraps
    /// past midnight, and under a minute the line is the end time alone.
    #[test]
    fn the_end_time_reads_the_wall_clock_plus_the_seconds_that_remain() {
        let cases = [
            (
                "an evening",
                22 * 3600 + 30 * 60,
                4320,
                "1h 12m \u{b7} ends at 11:42 pm",
            ),
            (
                "past midnight",
                23 * 3600 + 50 * 60,
                2700,
                "45m \u{b7} ends at 12:35 am",
            ),
            (
                "a morning",
                9 * 3600 + 5 * 60,
                1800,
                "30m \u{b7} ends at 9:35 am",
            ),
            ("whole hours", 20 * 3600, 7200, "2h \u{b7} ends at 10:00 pm"),
            ("a part minute", 12 * 3600 + 59, 30, "Ends at 12:01 pm"),
            ("nothing left", 13 * 3600, 0, "Ends at 1:00 pm"),
        ];
        for (name, now, remaining, want) in cases {
            let mut upnext = UpNext::default();
            upnext.receive(&format!(
                r#"{{"title":"E05","remainingSeconds":{remaining}}}"#
            ));
            let lines = upnext.card_lines(&canvas(), &crate::clock::at(now));
            assert_eq!(
                lines.last().map(|line| line.content.as_str()),
                Some(want),
                "{name}"
            );
        }
    }

    /// The card reads the clock on every draw, so the end time changes
    /// when the minute changes while the card is showing.
    #[test]
    fn the_end_time_follows_the_minute() {
        let upnext = risen();
        let later = crate::clock::at(22 * 3600 + 31 * 60);
        assert_eq!(
            upnext.card_lines(&canvas(), &later)[3].content,
            "45m \u{b7} ends at 11:16 pm"
        );
    }

    /// A block with no `remainingSeconds`, or with a negative or
    /// non-number value, draws no end time, and the card keeps its size.
    #[test]
    fn an_offer_with_no_seconds_draws_no_end_time() {
        for text in [
            r#"{"title":"E05","detail":"45 min"}"#,
            r#"{"title":"E05","detail":"45 min","remainingSeconds":-5}"#,
            r#"{"title":"E05","detail":"45 min","remainingSeconds":"2700"}"#,
        ] {
            let mut upnext = UpNext::default();
            upnext.receive(text);
            let lines = upnext.card_lines(&canvas(), &evening());
            assert_eq!(lines.len(), 2, "card lines for {text:?}, want 2");
            assert!(
                lines.iter().all(|line| !line.content.starts_with("Ends")),
                "a card line for {text:?} starts with Ends, want none"
            );
            assert_eq!(
                upnext.card_box(&canvas()).height,
                458.0,
                "card height for {text:?}"
            );
        }
    }

    /// A line too long for the card is cut and ends with an ellipsis, and
    /// it fits the width that the padding leaves.
    #[test]
    fn a_long_line_clips_to_the_card() {
        let room = CARD_W - 2.0 * PAD_X;
        let long = "The Hound of the Baskervilles: Part Two, Extended Edition";
        let clipped = clip(long, theme::type_scale::LABEL, room);

        assert!(clipped.ends_with(ELLIPSIS));
        assert!(clipped.len() < long.len());
        assert!(long.starts_with(clipped.trim_end_matches(ELLIPSIS)));
        assert!(measure(&clipped, theme::type_scale::LABEL) <= room);
    }

    #[test]
    fn a_line_that_fits_is_not_cut() {
        let room = CARD_W - 2.0 * PAD_X;
        assert_eq!(clip("E05", theme::type_scale::LABEL, room), "E05");
        assert_eq!(clip("", theme::type_scale::LABEL, room), "");
    }

    /// A clip cuts between characters, not bytes, so it never leaves half
    /// of a multibyte character.
    #[test]
    fn a_clip_cuts_on_a_whole_glyph() {
        let clipped = clip(
            "\u{b7}\u{b7}\u{b7}\u{b7}\u{b7}\u{b7}\u{b7}\u{b7}\u{b7}\u{b7}",
            theme::type_scale::LABEL,
            40.0,
        );
        assert!(clipped.ends_with(ELLIPSIS));
        assert!(
            clipped
                .chars()
                .all(|glyph| glyph == '\u{b7}' || glyph == '\u{2026}'),
            "the clipped text {clipped:?} has a glyph other than a middle dot or an ellipsis"
        );
    }

    /// When the room is narrower than the ellipsis, the result is the
    /// ellipsis alone.
    #[test]
    fn a_line_with_no_room_at_all_reads_as_the_ellipsis() {
        assert_eq!(
            clip("E05 the Long Tide", theme::type_scale::LABEL, 1.0),
            ELLIPSIS
        );
    }

    /// After the rise, the card shows over the bare video until its hide
    /// timer ends, and then nothing of it is drawn.
    #[test]
    fn the_card_shows_over_the_bare_video_and_then_draws_nothing() {
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);
        assert!(!upnext.draws_outside(false));

        assert!(upnext.on_percent(Some(98.0), &film(100.0), Some(97.0)));
        while upnext.fade().running() {
            upnext.fade_mut().step();
        }
        assert!(upnext.draws_outside(false));
        assert!(!upnext.draws_outside(true));

        upnext.hide();
        while upnext.fade().running() {
            upnext.fade_mut().step();
        }
        assert!(!upnext.draws_outside(false));
    }

    /// An expanded chip belongs to the OSD, so it is never drawn over the
    /// bare video.
    #[test]
    fn an_expansion_never_draws_over_the_bare_video() {
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);
        upnext.expand();
        assert!(upnext.showing_card());
        assert!(!upnext.draws_outside(false));
    }

    /// Runs the card's fade and the column's fade to their ends, and
    /// returns the number of ticks.
    fn ticks(upnext: &mut UpNext) -> usize {
        let mut ticks = 0;
        while upnext.fade().running() || upnext.column().running() {
            upnext.fade_mut().step();
            upnext.column_mut().step();
            ticks += 1;
            assert!(
                ticks < 1000,
                "the fade or the column still runs after {ticks} ticks, want stopped"
            );
        }
        ticks
    }

    /// Taking the offer fades the corner card out over `theme::HAND_OFF`,
    /// 18 ticks, whether the OSD is showing or not, and then the offer
    /// draws nothing.
    #[test]
    fn a_take_fades_the_corner_card_out() {
        let mut upnext = risen();
        settle(&mut upnext);
        upnext.take(0.0);

        assert!(upnext.waiting());
        assert_eq!(upnext.take_hide(), Hide::Cancel);
        assert!(upnext.draws_outside(false));
        assert!(upnext.draws_outside(true));
        assert_eq!(ticks(&mut upnext), 18);
        assert!(!upnext.draws_outside(false));
        assert!(!upnext.draws_outside(true));
    }

    /// A card drawn by the OSD moves out of the OSD when the viewer takes
    /// the offer, and fades out from the OSD's fade value.
    #[test]
    fn a_take_inside_the_osd_fades_from_the_osd_fade_value() {
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);
        upnext.expand();
        assert_eq!(upnext.fade().value(), 0.0);

        upnext.take(1.0);
        assert!(upnext.draws_outside(true));
        upnext.fade_mut().step();
        assert!(upnext.fade().value() > 0.99);
        assert_eq!(ticks(&mut upnext), 17);
        assert!(!upnext.draws_outside(true));
    }

    #[test]
    fn a_take_with_no_offer_records_no_wait() {
        let mut upnext = UpNext::default();
        upnext.take(1.0);
        assert!(!upnext.waiting());
    }

    /// An expansion lasts until the focus leaves the offer or the OSD
    /// hides.
    #[test]
    fn an_expansion_collapses_back_to_the_chip() {
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);
        upnext.expand();
        assert!(upnext.showing_card());
        upnext.collapse();
        assert!(!upnext.showing_card());
    }

    /// A risen card stays a card, so `collapse` does not return it to a
    /// chip.
    #[test]
    fn a_collapse_does_not_lower_a_risen_card() {
        let mut upnext = risen();
        upnext.collapse();
        assert!(upnext.showing_card());
    }

    #[test]
    fn a_new_offer_replaces_the_offer_and_its_state() {
        let mut upnext = risen();
        upnext.expand();
        upnext.take(0.0);

        upnext.receive(r#"{"title":"E06 The Turning"}"#);
        assert!(upnext.available());
        assert!(!upnext.showing_card());
        assert!(!upnext.waiting());
        assert_eq!(upnext.fade().value(), 0.0);
        assert_eq!(
            upnext.card_lines(&canvas(), &evening())[0].content,
            "E06 The Turning"
        );
    }

    /// The first playlist report is the item already playing, so it does
    /// not clear the offer. A report of a different item does.
    #[test]
    fn a_new_item_clears_the_offer_and_the_first_report_does_not() {
        let mut upnext = risen();
        upnext.on_playlist_pos(Some(0));
        assert!(upnext.available());
        upnext.on_playlist_pos(Some(0));
        assert!(upnext.available());
        upnext.on_playlist_pos(Some(1));
        assert!(!upnext.available());
        assert!(!upnext.showing_card());
    }

    #[test]
    fn a_playlist_update_with_no_number_keeps_the_offer() {
        let mut upnext = risen();
        upnext.on_playlist_pos(None);
        upnext.on_playlist_pos(Some(0));
        upnext.on_playlist_pos(None);
        assert!(upnext.available());
    }

    /// The art is centered on the plate, on the output pixel grid.
    #[test]
    fn the_art_is_centered_on_the_art_plate() {
        let upnext = risen();
        let bounds = upnext.art_bounds(&canvas(), Size::new(300.0, 248.0));
        assert_eq!(bounds.x, CARD_X + (440.0 - 300.0) / 2.0);
        assert_eq!(bounds.y, 338.0);
        assert_eq!(bounds.width, 300.0);
        assert_eq!(bounds.height, 248.0);
    }

    /// A picture larger than the screen does not start left of or above
    /// the screen.
    #[test]
    fn the_art_never_draws_off_the_screen() {
        let upnext = risen();
        let bounds = upnext.art_bounds(&canvas(), Size::new(4000.0, 4000.0));
        assert_eq!(bounds.x, 0.0);
        assert_eq!(bounds.y, 0.0);
    }

    /// On a wider screen the card keeps its right edge on the margin of
    /// that screen.
    #[test]
    fn the_card_follows_the_margin_of_the_screen() {
        let wide = Canvas::for_output(Size::new(2560.0, 1080.0));
        let upnext = risen();
        assert_eq!(
            upnext.card_box(&wide).x + CARD_W,
            wide.width() - theme::MARGIN_X
        );
        assert_eq!(upnext.chip_line(&wide, false).at.x, wide.width() - 96.0);
    }

    /// A risen card whose film has stepped back, with every fade finished.
    fn stepped() -> UpNext {
        let mut upnext = risen();
        upnext.step_back();
        settle(&mut upnext);
        upnext
    }

    fn settle(upnext: &mut UpNext) {
        while upnext.fade().running() || upnext.column().running() {
            upnext.fade_mut().step();
            upnext.column_mut().step();
        }
    }

    /// The column stays until a key press or the end of the file, so
    /// `step_back` cancels the hide timer that the rise armed.
    #[test]
    fn the_step_back_cancels_the_hide_timer() {
        let mut upnext = risen();
        upnext.step_back();
        assert_eq!(upnext.take_hide(), Hide::Cancel);
    }

    /// No fade runs while the compositor moves the film, so the display
    /// draws no frames.
    #[test]
    fn no_fade_runs_while_the_film_steps_back() {
        let mut upnext = risen();
        upnext.step_back();
        assert!(!upnext.fade().running());
        assert!(!upnext.column().running());
    }

    /// The column draws nothing until `landed`, whether the OSD is showing
    /// or not.
    #[test]
    fn the_column_waits_until_the_film_has_landed() {
        let mut upnext = stepped();
        assert!(!upnext.draws_outside(false));
        assert!(!upnext.draws_outside(true));

        upnext.landed();
        settle(&mut upnext);
        assert!(upnext.draws_outside(false));
        assert!(upnext.draws_outside(true));
    }

    #[test]
    fn a_select_over_the_bare_video_takes_the_offer_from_the_column() {
        let mut upnext = stepped();
        upnext.landed();
        settle(&mut upnext);
        assert!(upnext.risen_outside(false));
    }

    #[test]
    fn a_film_in_the_full_frame_shows_no_column() {
        let mut upnext = risen();
        upnext.landed();
        settle(&mut upnext);
        assert_eq!(upnext.column().value(), 0.0);
    }

    /// A back press returns the film, and the column and the card
    /// disappear at once. The offer stays in the OSD.
    #[test]
    fn a_restore_removes_the_column_and_the_card_at_once() {
        let mut upnext = stepped();
        upnext.landed();
        settle(&mut upnext);

        upnext.restore();
        assert!(!upnext.stepped_back());
        assert!(!upnext.draws_outside(false));
        assert!(!upnext.risen_outside(false));
        assert!(upnext.available());
        assert!(upnext.showing_card());
    }

    /// When the compositor returns the film by itself, the corner card
    /// shows until its hide timer runs out.
    #[test]
    fn a_fall_back_shows_the_corner_card_until_its_hide_timer() {
        let mut upnext = stepped();
        upnext.take_hide();
        upnext.fall_back();
        assert!(!upnext.stepped_back());
        assert_eq!(upnext.take_hide(), Hide::Arm);
        settle(&mut upnext);
        assert!(upnext.draws_outside(false));
    }

    #[test]
    fn the_art_box_follows_the_step_back() {
        let mut upnext = risen();
        assert_eq!(upnext.art_box(), Size::new(440.0, 248.0));
        assert_eq!(upnext.left(&canvas()), CARD_X);
        upnext.step_back();
        assert_eq!(upnext.art_box(), Size::new(704.0, 396.0));
        assert_eq!(upnext.left(&canvas()), 1120.0);
    }

    /// Taking the offer fades the column out over `theme::HAND_OFF`. The
    /// corner card stays hidden, so nothing draws when the fade ends.
    #[test]
    fn a_take_fades_the_column_out() {
        let mut upnext = stepped();
        upnext.landed();
        settle(&mut upnext);
        upnext.take(1.0);

        assert!(upnext.waiting());
        assert!(upnext.stepped_back(), "the film stays in its frame");
        assert_eq!(upnext.fade().value(), 0.0);
        assert!(upnext.draws_outside(false));
        assert_eq!(ticks(&mut upnext), 18);
        assert!(!upnext.draws_outside(false));
        assert!(!upnext.draws_outside(true));
    }

    /// When the compositor returns the film during the wait, the corner
    /// card does not return.
    #[test]
    fn a_fall_back_during_the_wait_brings_no_card_back() {
        let mut upnext = stepped();
        upnext.landed();
        settle(&mut upnext);
        upnext.take(1.0);
        upnext.take_hide();

        upnext.fall_back();
        assert_eq!(upnext.take_hide(), Hide::Keep);
        settle(&mut upnext);
        assert!(!upnext.draws_outside(false));
        assert!(!upnext.draws_outside(true));
    }

    #[test]
    fn the_column_stays_hidden_when_the_film_lands_after_a_take() {
        let mut upnext = stepped();
        upnext.take(0.0);
        upnext.landed();
        settle(&mut upnext);
        assert_eq!(upnext.column().value(), 0.0);
        assert!(!upnext.draws_outside(false));
    }

    #[test]
    fn a_new_offer_clears_the_stepped_back_state() {
        let mut upnext = stepped();
        upnext.receive(OFFER);
        assert!(!upnext.stepped_back());
    }
}
