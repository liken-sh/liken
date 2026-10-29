//! The up-next offer: the work the Play names as the one that follows it.
//! The command sidecar sends the block as the next script-message.
//! This module draws the block as a chip while the film plays, and as a card
//! near the end of it. Focus routes a select on it.

use iced::{Point, Rectangle, Size};
use jiff::Zoned;
use serde_json::Value;

mod column;

use crate::canvas::{Anchor, Brush, Canvas, Line, clip, measure};
use crate::clock;
use crate::fade::{Clock, Fade, Hide};
use crate::film::Film;
use crate::theme;

/// The card's box in canvas pixels. It is 440 wide with its right edge on the
/// margin, and its bottom edge stays above the row where the scrubber draws
/// the time label, so the card never covers the scrubber. The card is 458
/// tall with four lines under the art, so its top is at 338 and its bottom
/// at 796.
pub const CARD_W: f32 = 440.0;
const CARD_TOP: f32 = 338.0;
pub const ART_H: f32 = 248.0;
const PAD_X: f32 = 22.0;
const PAD_Y: f32 = 16.0;
pub(crate) const CARD_R: f32 = 14.0;
/// The unfocused card's fill, dark enough to read the three lines over a
/// bright frame. The focused card takes the chooser panel.
pub(crate) const CARD_ALPHA: f32 = theme::opacity(0x54);
/// The drop from each line of the card to the next, at the type size each
/// line draws in.
const REASON_PITCH: f32 = 40.0;
const TITLE_PITCH: f32 = 54.0;
const DETAIL_PITCH: f32 = 42.0;
const ENDS_H: f32 = 42.0;
/// The card keeps the row of every line, the end time's too, whether or not
/// the block fills it, so one card never changes size between two offers.
const CARD_H: f32 = ART_H + PAD_Y + REASON_PITCH + TITLE_PITCH + DETAIL_PITCH + ENDS_H + PAD_Y;

/// The chip's row, above the right end of the bar and above the time label.
/// `CHIP_Y` is the top of the label's line box, and `CHIP_H` is that line
/// box, so the box around the label spans 800 to 846 and its pad is the same
/// above and below the line. The skip control takes the same metrics.
pub(crate) const CHIP_Y: f32 = 809.0;
pub(crate) const CHIP_H: f32 = theme::type_scale::TINY;
pub(crate) const CHIP_PAD_X: f32 = 22.0;
pub(crate) const CHIP_PAD_Y: f32 = 9.0;
/// A button rounds at half its height, so its ends are round and it reads
/// apart from a panel, which rounds at `CARD_R`.
pub(crate) const CHIP_R: f32 = (CHIP_H + 2.0 * CHIP_PAD_Y) / 2.0;

/// The two amounts of a film that may remain when the card rises, where its
/// marks place no credits: a share of its length, and a number of seconds.
/// The rule takes whichever leaves less time, so the seconds apply only to
/// a film over 100 minutes.
///
/// The rule has the shape of the watched rule, which `library-operator`
/// states in `watched.go` and in
/// `media-browser/src/catalog/progress/watched.rs`: five percent or 300
/// seconds, whichever is less. Each amount here is no larger than the
/// watched rule's amount of the same kind, so the smaller of the two is no
/// larger either, and the card never rises before the work counts as
/// watched. A person who takes the offer at the rise leaves the work, so a
/// card that rose earlier would leave a work unwatched. The two rules differ
/// because the card marks the credits and the watched rule marks the end of
/// the story.
const RISE_PERCENT: f64 = 4.0;
const RISE_SECONDS: f64 = 240.0;

/// The second at which the time rule raises the card: the length less the
/// smaller of the two amounts. The marks cap a rise after a scene at this
/// point, so a card never rises later than the time rule puts it.
pub(crate) fn time_rise(duration: f64) -> f64 {
    duration - (duration * RISE_PERCENT / 100.0).min(RISE_SECONDS)
}

/// The two amounts of an episode with no credits mark. An episode is short,
/// and the film rule's share of it ends too early, so the share is smaller,
/// and the seconds apply to an episode over 80 minutes. Each amount is no
/// larger than the watched rule's, for the reason the film rule states.
const SERIES_RISE_PERCENT: f64 = 2.5;
const SERIES_RISE_SECONDS: f64 = 120.0;

/// The second at which an episode's card rises: the length less the smaller
/// of the two series amounts.
pub(crate) fn series_rise(duration: f64) -> f64 {
    duration - (duration * SERIES_RISE_PERCENT / 100.0).min(SERIES_RISE_SECONDS)
}

/// The words the chip draws before the title. The block carries the three
/// lines of the card and nothing for the chip, so they are spelled here.
const CHIP_WORD: &str = "UP NEXT";

// The seam the decode fills. The card asks for the offer's picture where
// it draws the art plate, the decode answers with the size it draws at, and
// the card hands back the box to draw it in.
pub trait Art {
    fn next(&self) -> Option<Size>;

    fn draw(&self, brush: &mut Brush<'_>, bounds: Rectangle);
}

/// The words that start the card's last line, before the time.
const ENDS_WORDS: &str = "Ends at";

/// The block the sidecar sent. A block with none of the three lines and no
/// art is not an offer.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Offer {
    pub reason: Option<String>,
    pub title: Option<String>,
    pub detail: Option<String>,
    pub art: Option<String>,
    /// How long a play of the next work runs from where it starts. The card
    /// adds it to the wall clock when it draws, so the end time it reads is
    /// the time the next work would end if a person took the offer now.
    pub remaining: Option<i64>,
}

impl Offer {
    fn parse(text: &str) -> Option<Self> {
        let block = serde_json::from_str::<Value>(text).ok()?;
        // A field the sidecar sent empty is a field the block does not
        // carry, the way every presentation field reads, so a block of empty
        // strings is no offer.
        let word = |name: &str| {
            block
                .get(name)
                .and_then(Value::as_str)
                .filter(|word| !word.is_empty())
                .map(str::to_string)
        };
        let offer = Self {
            reason: word("reason"),
            title: word("title"),
            detail: word("detail"),
            art: word("art"),
            remaining: None,
        };
        // The seconds alone give the card nothing to name, so they do not
        // make a block an offer.
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

/// The offer and what has happened to it: whether the playhead has crossed the
/// rise, whether a select has grown the chip into the card, whether a select
/// has asked for the next work, and the card's own fade while the OSD is down.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct UpNext {
    offer: Option<Offer>,
    risen: bool,
    /// Whether a select on the chip has grown it into the card. The rise does
    /// the same on its own. This state lasts only until the focus leaves the
    /// stop or the OSD hides.
    expanded: bool,
    waiting: bool,
    clock: Clock,
    /// The last playlist position mpv reported. A change of item clears the
    /// offer, and the first report is not a change.
    at_item: Option<i64>,
    /// Whether the film stands stepped back, so the offer draws as the
    /// column beside it instead of the corner card.
    stepped: bool,
    /// The column's own fade. It rises when the film lands in its frame,
    /// because the display draws no frame while the compositor moves the
    /// film.
    column: Fade,
}

impl UpNext {
    /// The card's own fade, which the frame loop steps while it is moving.
    pub fn fade(&self) -> &Fade {
        self.clock.fade()
    }

    pub fn fade_mut(&mut self) -> &mut Fade {
        self.clock.fade_mut()
    }

    /// The hide window this state asks for, which the frame loop arms and
    /// cancels because it owns the timer.
    pub fn take_hide(&mut self) -> Hide {
        self.clock.take_hide()
    }

    /// The hide window ran out, so the card leaves on its own fade.
    pub fn hide(&mut self) {
        self.clock.hide();
    }

    /// Take the block as one JSON string. A block with none of the three lines
    /// and no art is not an offer, so an empty object clears the current one.
    pub fn receive(&mut self, text: &str) {
        self.reset();
        self.offer = Offer::parse(text);
    }

    /// The offer is for the item the Play started on, so a move to another
    /// item drops it. mpv reports the position once when the display observes
    /// it, and that first report names the item already playing, not a move.
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

    /// mpv sends percent-pos on every change, so the card rises from the
    /// property and the display runs no timer to watch for the crossing. The
    /// rise holds after it happens, so a seek back does not take the card down.
    ///
    /// `rise` is the second the item states for the card: where its marks
    /// place the credits, which `Marks::credits` states, or the item's time
    /// rule, `time_rise` for a film and `series_rise` for an episode. With
    /// no rise the card never rises.
    ///
    /// It answers whether the card rose, because that is the one push of this
    /// property that draws anything.
    pub fn on_percent(&mut self, value: Option<f64>, film: &Film, rise: Option<f64>) -> bool {
        let Some(value) = value else {
            return false;
        };
        if self.offer.is_none() || self.risen {
            return false;
        }
        // mpv states a percent only for a work whose length it has read, so a
        // work with no duration never raises the card.
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

    /// The film steps back at the rise, so the offer draws as the column
    /// beside it. The column waits for the landing, and the corner card's
    /// hide window is cancelled, because the column stays until a press or
    /// the end of the file.
    pub fn step_back(&mut self) {
        self.stepped = true;
        self.column = Fade::default();
        self.clock.ask(Hide::Cancel);
        // The card's own fade would tick, and redraw the frame, while the
        // compositor moves the film. The column draws in its place, so the
        // card's fade stands at its end at once.
        self.clock.fade_mut().finish();
    }

    /// The film reached its frame, so the column fades in. A taken offer
    /// has left, so its column does not come back.
    pub fn landed(&mut self) {
        if self.stepped && !self.waiting {
            self.column.to(1.0);
        }
    }

    /// back returned the film to the whole region, so the column and the
    /// card leave at once, before the film moves. The offer stays in the
    /// OSD, and the card does not rise again.
    pub fn restore(&mut self) {
        self.stepped = false;
        self.column = Fade::default();
        self.clock = Clock::default();
        self.clock.ask(Hide::Cancel);
    }

    /// The compositor put the film back on its own, because the connection
    /// failed or the arranger was refused. The offer falls back to the corner
    /// card for its usual window.
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

    /// The column's fade, which the frame loop steps while it is moving.
    pub fn column(&self) -> &Fade {
        &self.column
    }

    pub fn column_mut(&mut self) -> &mut Fade {
        &mut self.column
    }

    /// The box the offer's picture draws in: the column's while the film
    /// stands back, and the card's otherwise. The decode asks at this box.
    pub fn art_box(&self) -> Size {
        match self.stepped {
            true => column::ART,
            false => Size::new(CARD_W, ART_H),
        }
    }

    /// The left edge of what the offer draws on the right, which the cast row
    /// ends short of.
    pub fn left(&self, canvas: &Canvas) -> f32 {
        match self.stepped {
            true => column::left(canvas),
            false => card_x(canvas),
        }
    }

    /// The stop is present only while the Play carries an offer.
    pub fn available(&self) -> bool {
        self.offer.is_some()
    }

    pub fn waiting(&self) -> bool {
        self.waiting
    }

    /// The offer's art reference, which the display decodes into the card's
    /// own art box. An offer with no art is not one the card draws a picture
    /// for.
    pub fn art(&self) -> Option<&str> {
        self.offer.as_ref()?.art.as_deref()
    }

    /// The card is what a select acts on, so the display grows the chip into
    /// it first and takes the offer on the press after that.
    pub fn showing_card(&self) -> bool {
        self.risen || self.expanded
    }

    pub fn expand(&mut self) {
        self.expanded = true;
    }

    /// The offer falls back to the chip when the focus leaves the stop or the
    /// OSD hides, so a summon shows the small form again until the rise.
    pub fn collapse(&mut self) {
        self.expanded = false;
    }

    /// take records that focus broadcast the ask, and hands the screen off:
    /// the offer fades out over `theme::HAND_OFF` on the ease-in curve, the
    /// column beside a stepped-back film or the corner card. The display
    /// then waits for the operator to end this Play and start the next one,
    /// and nothing here ends the wait: mpv quits, or a back press ends the
    /// run.
    ///
    /// `osd` is the OSD's fade. A card the OSD drew leaves the OSD at the
    /// press and fades on its own clock, from the alpha it stood at, so it
    /// does not vanish at once.
    pub fn take(&mut self, osd: f32) {
        if self.offer.is_none() {
            return;
        }
        self.waiting = true;
        self.clock.ask(Hide::Cancel);
        let card = self.clock.fade_mut();
        if self.stepped {
            // The step back stood the card's fade at full under the column.
            // It stays clear, so a compositor that returns the film during
            // the wait shows no card.
            card.to(0.0);
            card.finish();
            self.column.exit(theme::HAND_OFF);
            return;
        }
        card.to(card.value().max(osd));
        card.finish();
        card.exit(theme::HAND_OFF);
    }

    /// A new offer, or the loss of one, drops every state the last one had.
    fn reset(&mut self) {
        self.risen = false;
        self.expanded = false;
        self.waiting = false;
        self.clock = Clock::default();
        self.clock.ask(Hide::Cancel);
        self.stepped = false;
        self.column = Fade::default();
    }

    /// The label the chip reads: the two words and the offer's own title.
    fn label(&self) -> String {
        let title = self
            .offer
            .as_ref()
            .and_then(|offer| offer.title.as_deref())
            .unwrap_or_default();
        format!("{CHIP_WORD}  {title}")
    }

    /// The panel behind the focused chip, which measures its own label.
    fn chip_panel(&self, canvas: &Canvas) -> Rectangle {
        let width = measure(&self.label(), theme::type_scale::TINY) + 2.0 * CHIP_PAD_X;
        Rectangle::new(
            Point::new(canvas.right() + CHIP_PAD_X - width, CHIP_Y - CHIP_PAD_Y),
            Size::new(width, CHIP_H + 2.0 * CHIP_PAD_Y),
        )
    }

    /// The label stands on the panel's middle row, so the pad above the
    /// caps and below them match, and the two cannot drift apart.
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

    /// The card's own box, which hangs off the right margin.
    fn card_box(&self, canvas: &Canvas) -> Rectangle {
        Rectangle::new(
            Point::new(card_x(canvas), CARD_TOP),
            Size::new(CARD_W, CARD_H),
        )
    }

    /// The plate the art draws on.
    fn art_plate(&self, canvas: &Canvas) -> Rectangle {
        Rectangle::new(
            Point::new(card_x(canvas), CARD_TOP),
            Size::new(CARD_W, ART_H),
        )
    }

    /// The card's lines, each clipped to the width inside the pad: the three
    /// the block spells, and the time the next work would end, read from the
    /// wall clock `now`.
    fn card_lines(&self, canvas: &Canvas, now: &Zoned) -> Vec<Line> {
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
        if let Some(seconds) = offer.remaining {
            let ends = now.saturating_add(jiff::Span::new().seconds(seconds));
            lines.push(Line::new(
                clip(
                    &format!("{ENDS_WORDS} {}", clock::reading(&ends)),
                    theme::type_scale::SMALL,
                    room,
                ),
                Point::new(x, y),
                Anchor::TopLeft,
                theme::type_scale::SMALL,
                theme::color::muted(),
            ));
        }
        lines
    }

    /// Where the art draws: the middle of the art plate, at the size the
    /// bridge answers with, on the output pixel grid and never off the screen.
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

    /// The card: the art over the three lines the block spells and the end
    /// time.
    fn card(&self, brush: &mut Brush<'_>, focused: bool, art: &dyn Art, now: &Zoned) {
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

    /// Draw the offer as part of the OSD, the chip before the rise and the
    /// card after it, at the OSD's own fade. A taken offer fades on its own
    /// clock outside the OSD, so the OSD draws nothing of it.
    pub fn draw(&self, brush: &mut Brush<'_>, focused: bool, art: &dyn Art, now: &Zoned) {
        if self.offer.is_none() || self.waiting || self.stepped {
            return;
        }
        if self.showing_card() {
            self.card(brush, focused, art, now);
        } else {
            self.chip(brush, focused);
        }
    }

    /// Whether the risen card is on the bare video and a select may still
    /// take it: from the rise until its fade out ends, and not once a select
    /// has asked for the next work.
    pub fn risen_outside(&self, osd_visible: bool) -> bool {
        self.offer.is_some() && !osd_visible && self.risen && !self.waiting && self.fade().shown()
    }

    /// Draw what the offer draws over the bare video, on a clock of its own:
    /// the risen card for its few seconds, and a taken card while it fades
    /// out. Once the card fades, nothing remains of it, because a mark at the
    /// screen edge would draw over the film for the rest of it. Before the
    /// rise, a hidden OSD draws nothing for the offer. `focused` draws the
    /// risen card in the focus panel, for the times a select takes it. A
    /// taken card keeps the panel the press left it in while it fades.
    pub fn draw_outside(
        &self,
        brush: &mut Brush<'_>,
        osd_visible: bool,
        focused: bool,
        art: &dyn Art,
        now: &Zoned,
    ) {
        if !self.draws_outside(osd_visible) {
            return;
        }
        if self.stepped {
            self.draw_column(brush, focused, art);
            return;
        }
        let focused = focused || self.waiting;
        brush.at_fade(self.fade().value(), |brush| {
            self.card(brush, focused, art, now);
        });
    }

    /// Whether the offer has anything to draw over the bare video, so the
    /// frame loop draws no layer for a card that is not there.
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

    /// The column beside the stepped-back film, on its own fade, over the
    /// bare video and over the OSD alike. A taken column keeps the panel the
    /// press left it in while it fades.
    fn draw_column(&self, brush: &mut Brush<'_>, focused: bool, art: &dyn Art) {
        let Some(offer) = self.offer.as_ref() else {
            return;
        };
        brush.at_fade(self.column.value(), |brush| {
            column::draw(brush, offer, focused || self.waiting, art);
        });
    }
}

/// The card's left edge, which the cast row ends short of while the card
/// stands.
pub fn card_x(canvas: &Canvas) -> f32 {
    canvas.right() - CARD_W
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::canvas::ELLIPSIS;
    use serde_json::json;

    /// The block the sidecar sends for an episode, with the three lines the
    /// card draws.
    const OFFER: &str = r#"{"reason":"Next in Harbor Lights · S02",
        "title":"E05 · The Long Tide",
        "detail":"Harbor Lights · S02 · 45 min",
        "remainingSeconds":2700}"#;

    /// The canvas numbers the design gives: the right margin, the card box,
    /// and the row the chip draws on.
    const RIGHT: f32 = 1824.0;
    const CARD_X: f32 = 1384.0;
    const TEXT_X: f32 = 1406.0;
    /// The row the scrubber's own text starts on, which the offer stays above.
    const FLOOR: f32 = 820.0;

    fn film(duration: f64) -> Film {
        let mut film = Film::default();
        film.apply("duration", &json!(duration));
        film
    }

    /// One offer that has risen, the way a film reaching its credits raises it.
    fn risen() -> UpNext {
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);
        assert!(upnext.on_percent(Some(98.0), &film(100.0), Some(97.0)));
        upnext
    }

    fn canvas() -> Canvas {
        Canvas::default()
    }

    /// 1970-01-01 22:30 UTC, the wall clock the card's lines read in most
    /// tests.
    fn evening() -> Zoned {
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
            assert!(!upnext.available(), "{text} offered a stop");
        }
    }

    /// A field the sidecar sent empty is a field the block does not carry, so
    /// a block of empty strings is no offer and adds no focus stop.
    #[test]
    fn a_block_of_empty_fields_is_no_offer() {
        for text in [
            r#"{"title":""}"#,
            r#"{"reason":"","title":"","detail":"","art":""}"#,
            r#"{"remainingSeconds":2700}"#,
        ] {
            let mut upnext = UpNext::default();
            upnext.receive(text);
            assert!(!upnext.available(), "{text} offered a stop");
        }

        let mut upnext = UpNext::default();
        upnext.receive(r#"{"title":"","detail":"45 min"}"#);
        assert!(upnext.available());
        assert_eq!(upnext.label(), "UP NEXT  ");
    }

    /// Any one of the four fields makes a block an offer.
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
            assert!(upnext.available(), "{text} offered no stop");
        }
    }

    #[test]
    fn the_chip_reads_up_next_and_the_title_right_aligned() {
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);

        let line = upnext.chip_line(&canvas(), false);
        assert_eq!(line.content, "UP NEXT  E05 \u{b7} The Long Tide");
        assert_eq!(line.at, Point::new(RIGHT, 823.0));
        assert_eq!(line.anchor, Anchor::Right);
        assert_eq!(line.size, theme::type_scale::TINY);
        assert_eq!(line.color, theme::color::muted());
    }

    /// The chip reads bright while the focus stands on it.
    #[test]
    fn the_focused_chip_reads_in_the_body_colour() {
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);
        assert_eq!(
            upnext.chip_line(&canvas(), true).color,
            theme::color::text()
        );
    }

    /// The panel ends one padding past the right margin, so its left edge is
    /// one padding inside the margin, less the label.
    #[test]
    fn the_chips_panel_measures_its_own_label() {
        let mut upnext = UpNext::default();
        upnext.receive(r#"{"title":"The Hobbit: The Desolation of Smaug"}"#);

        let panel = upnext.chip_panel(&canvas());
        let label = measure(
            "UP NEXT  The Hobbit: The Desolation of Smaug",
            theme::type_scale::TINY,
        );
        assert!((panel.width - (label + 44.0)).abs() < 0.01);
        assert!((panel.x - (RIGHT + 22.0 - panel.width)).abs() < 0.01);
        assert_eq!(panel.y, 800.0);
        assert_eq!(panel.height, 46.0);
        assert_eq!(
            panel.center().y,
            upnext.chip_line(&canvas(), false).at.y,
            "the label centres in the panel"
        );
    }

    /// A button rounds at half its height, so its ends are round and it
    /// reads apart from a panel.
    #[test]
    fn the_buttons_round_at_half_their_height() {
        assert_eq!(CHIP_R, 23.0);
        assert_eq!(CHIP_H, theme::type_scale::TINY);
    }

    /// A longer title moves the panel's left edge out by the width of what it
    /// gained, and the right edge holds.
    #[test]
    fn the_panel_grows_leftward_with_the_label() {
        let mut short = UpNext::default();
        short.receive(r#"{"title":"E05"}"#);
        let mut long = UpNext::default();
        long.receive(r#"{"title":"E05 · The Long Tide"}"#);

        let (short, long) = (short.chip_panel(&canvas()), long.chip_panel(&canvas()));
        assert!(long.x < short.x);
        assert!((long.x + long.width - (short.x + short.width)).abs() < 0.01);
    }

    /// The chip clears the row the scrubber's time label draws on.
    #[test]
    fn the_chip_clears_the_time_label_row() {
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);
        let panel = upnext.chip_panel(&canvas());
        assert!(panel.y + panel.height <= 846.0);
    }

    /// The rule takes whichever of the two amounts leaves less time, so a four
    /// hour film raises the card four minutes from its end and not at four
    /// percent of its length, and a short film raises it at 96 percent.
    #[test]
    fn a_film_raises_the_card_by_the_time_rule() {
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

    /// The card rises when the playhead reaches the second it is given.
    #[test]
    fn the_card_rises_at_its_second() {
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);
        assert!(!upnext.on_percent(Some(96.0), &film(100.0), Some(97.0)));
        assert!(!upnext.showing_card());
        assert!(upnext.on_percent(Some(97.0), &film(100.0), Some(97.0)));
        assert!(upnext.showing_card());
    }

    /// An episode's card rises when two and a half percent of it or two
    /// minutes remain, whichever is less.
    #[test]
    fn an_episode_raises_the_card_by_the_series_rule() {
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

    /// The watched rule of `library-operator`, which `watched.go` and
    /// `media-browser/src/catalog/progress/watched.rs` state: a work counts
    /// as watched when five percent of it or five minutes remain, whichever
    /// is less.
    fn watched_line(duration: f64) -> f64 {
        duration - (duration * 0.05).min(300.0)
    }

    /// A person who takes the offer leaves the work at the rise, so a card
    /// that rose before the watched line would leave the work unwatched.
    /// Both time rules rise at or after that line, at every length.
    #[test]
    fn neither_rule_raises_the_card_before_the_work_counts_as_watched() {
        let durations = [
            60.0, 600.0, 1320.0, 2700.0, 4800.0, 5400.0, 6000.0, 7200.0, 10800.0, 14400.0,
        ];
        for duration in durations {
            let watched = watched_line(duration);
            assert!(time_rise(duration) >= watched, "a film of {duration} s");
            assert!(
                series_rise(duration) >= watched,
                "an episode of {duration} s"
            );
        }
    }

    /// A work with no length, a push that carries no number, and a position
    /// with no rise raise nothing.
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

    /// The rise holds after it happens, so a seek back does not take the card
    /// down, and it arms the hide window once.
    #[test]
    fn the_rise_holds_and_arms_one_hide_window() {
        let mut upnext = risen();
        assert_eq!(upnext.take_hide(), Hide::Arm);
        assert!(!upnext.on_percent(Some(20.0), &film(100.0), Some(97.0)));
        assert!(upnext.showing_card());
        assert_eq!(upnext.take_hide(), Hide::Keep);
    }

    /// Credits in the second half move the rise to their start, whether that
    /// is earlier or later than the time that remains would put it.
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

    /// A rise the credits raised holds after a seek back, as every rise does.
    #[test]
    fn a_rise_at_the_credits_holds_after_a_seek_back() {
        let long = film(6000.0);
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);
        assert!(upnext.on_percent(Some(90.0), &long, Some(5400.0)));
        assert!(!upnext.on_percent(Some(10.0), &long, Some(5400.0)));
        assert!(upnext.showing_card());
    }

    /// An offer with no block raises nothing, whatever the position.
    #[test]
    fn a_position_with_no_offer_raises_nothing() {
        let mut upnext = UpNext::default();
        assert!(!upnext.on_percent(Some(99.0), &film(100.0), Some(97.0)));
    }

    #[test]
    fn the_card_holds_the_comps_box() {
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

    /// The four lines are cumulative: the art, a pad, then each line's own
    /// pitch.
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

        assert_eq!(lines[3].content, "Ends at 11:15 pm");
        assert_eq!(lines[3].at, Point::new(TEXT_X, 738.0));
        assert_eq!(lines[3].size, theme::type_scale::SMALL);
        assert_eq!(lines[3].color, theme::color::muted());
        for line in &lines {
            assert_eq!(line.anchor, Anchor::TopLeft);
        }
    }

    /// A block that spells one line draws that line alone, on the row it
    /// belongs to.
    #[test]
    fn a_line_the_block_leaves_out_draws_nothing() {
        let mut upnext = UpNext::default();
        upnext.receive(r#"{"title":"E05"}"#);
        let lines = upnext.card_lines(&canvas(), &evening());
        assert_eq!(lines.len(), 1);
        assert_eq!(lines[0].at.y, 642.0);
    }

    /// The end time is the wall clock at the draw plus the seconds that
    /// remain, in the clock's own format, and it rolls past midnight.
    #[test]
    fn the_end_time_reads_the_wall_clock_plus_the_seconds_that_remain() {
        let cases = [
            ("an evening", 22 * 3600 + 30 * 60, 4320, "Ends at 11:42 pm"),
            (
                "past midnight",
                23 * 3600 + 50 * 60,
                2700,
                "Ends at 12:35 am",
            ),
            ("a morning", 9 * 3600 + 5 * 60, 1800, "Ends at 9:35 am"),
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

    /// The draw reads the clock each time, so a card that stands while the
    /// minute turns reads the later end time.
    #[test]
    fn the_end_time_follows_the_minute() {
        let upnext = risen();
        let later = crate::clock::at(22 * 3600 + 31 * 60);
        assert_eq!(
            upnext.card_lines(&canvas(), &later)[3].content,
            "Ends at 11:16 pm"
        );
    }

    /// A block with no seconds, or seconds that are not a count, draws no
    /// end time, and the card keeps its size.
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
            assert_eq!(lines.len(), 2, "{text}");
            assert!(
                lines.iter().all(|line| !line.content.starts_with("Ends")),
                "{text}"
            );
            assert_eq!(upnext.card_box(&canvas()).height, 458.0, "{text}");
        }
    }

    /// A long line loses the glyphs it has no room for and ends on an
    /// ellipsis, inside the width the pad leaves.
    #[test]
    fn a_long_line_clips_to_the_card() {
        let room = CARD_W - 2.0 * PAD_X;
        let long = "The Hobbit: The Desolation of Smaug, Extended Edition";
        let clipped = clip(long, theme::type_scale::LABEL, room);

        assert!(clipped.ends_with(ELLIPSIS));
        assert!(clipped.len() < long.len());
        assert!(long.starts_with(clipped.trim_end_matches(ELLIPSIS)));
        assert!(measure(&clipped, theme::type_scale::LABEL) <= room);
    }

    #[test]
    fn a_line_that_fits_keeps_every_glyph() {
        let room = CARD_W - 2.0 * PAD_X;
        assert_eq!(clip("E05", theme::type_scale::LABEL, room), "E05");
        assert_eq!(clip("", theme::type_scale::LABEL, room), "");
    }

    /// A clip cuts on a character, not on a byte, so a multibyte glyph never
    /// leaves half of itself on screen.
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
                .all(|glyph| glyph == '\u{b7}' || glyph == '\u{2026}')
        );
    }

    /// A room narrower than the ellipsis leaves the ellipsis alone.
    #[test]
    fn a_line_with_no_room_at_all_reads_as_the_ellipsis() {
        assert_eq!(
            clip("E05 the Long Tide", theme::type_scale::LABEL, 1.0),
            ELLIPSIS
        );
    }

    /// The card shows itself with the OSD down for its few seconds, and once
    /// it fades nothing remains of it.
    #[test]
    fn the_card_shows_itself_with_the_osd_down_and_then_draws_nothing() {
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

    /// An expansion is part of the OSD, so it never draws over the bare video.
    #[test]
    fn an_expansion_never_draws_over_the_bare_video() {
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);
        upnext.expand();
        assert!(upnext.showing_card());
        assert!(!upnext.draws_outside(false));
    }

    /// Run the card's and the column's fades to their ends, and count the
    /// ticks that took.
    fn ticks(upnext: &mut UpNext) -> usize {
        let mut ticks = 0;
        while upnext.fade().running() || upnext.column().running() {
            upnext.fade_mut().step();
            upnext.column_mut().step();
            ticks += 1;
            assert!(ticks < 1000, "the offer never stopped moving");
        }
        ticks
    }

    /// A take fades the corner card out over the hand-off, 18 ticks, with
    /// the OSD up or down, and then the offer draws nothing.
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

    /// A card the OSD drew leaves the OSD at the press and fades out from
    /// the OSD's own alpha, so it does not vanish at once.
    #[test]
    fn a_take_inside_the_osd_fades_from_the_osds_alpha() {
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

    /// take on a display with no offer records no wait.
    #[test]
    fn a_take_with_no_offer_records_no_wait() {
        let mut upnext = UpNext::default();
        upnext.take(1.0);
        assert!(!upnext.waiting());
    }

    /// An expansion lasts until the focus leaves the stop or the OSD hides.
    #[test]
    fn an_expansion_collapses_back_to_the_chip() {
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);
        upnext.expand();
        assert!(upnext.showing_card());
        upnext.collapse();
        assert!(!upnext.showing_card());
    }

    /// A risen card holds the card form, so a collapse leaves it standing.
    #[test]
    fn a_collapse_never_takes_down_a_risen_card() {
        let mut upnext = risen();
        upnext.collapse();
        assert!(upnext.showing_card());
    }

    /// A replay replaces the offer and drops every state the last one had.
    #[test]
    fn a_replay_replaces_the_offer_and_its_state() {
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

    /// The first playlist report names the item already playing, so it is not
    /// a move, and a later one clears the offer.
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
    fn a_playlist_push_with_no_number_moves_nothing() {
        let mut upnext = risen();
        upnext.on_playlist_pos(None);
        upnext.on_playlist_pos(Some(0));
        upnext.on_playlist_pos(None);
        assert!(upnext.available());
    }

    /// The art draws in the middle of the plate, on the output pixel grid.
    #[test]
    fn the_art_centres_on_the_art_plate() {
        let upnext = risen();
        let bounds = upnext.art_bounds(&canvas(), Size::new(300.0, 248.0));
        assert_eq!(bounds.x, CARD_X + (440.0 - 300.0) / 2.0);
        assert_eq!(bounds.y, 338.0);
        assert_eq!(bounds.width, 300.0);
        assert_eq!(bounds.height, 248.0);
    }

    /// A picture wider than the plate hangs off neither edge of the screen.
    #[test]
    fn the_art_never_draws_off_the_screen() {
        let upnext = risen();
        let bounds = upnext.art_bounds(&canvas(), Size::new(4000.0, 4000.0));
        assert_eq!(bounds.x, 0.0);
        assert_eq!(bounds.y, 0.0);
    }

    /// The card holds its right edge on a wider screen, so the offer measures
    /// against the screen that is there.
    #[test]
    fn the_card_follows_the_screens_own_margin() {
        let wide = Canvas::for_output(Size::new(2560.0, 1080.0));
        let upnext = risen();
        assert_eq!(
            upnext.card_box(&wide).x + CARD_W,
            wide.width() - theme::MARGIN_X
        );
        assert_eq!(upnext.chip_line(&wide, false).at.x, wide.width() - 96.0);
    }

    /// A risen card whose film stepped back, with every fade at its end.
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

    /// The column stays until a press or the end of the file, so the step
    /// back cancels the window the rise armed.
    #[test]
    fn the_step_back_cancels_the_cards_window() {
        let mut upnext = risen();
        upnext.step_back();
        assert_eq!(upnext.take_hide(), Hide::Cancel);
    }

    /// Nothing ticks while the compositor moves the film, so the display
    /// draws no frame then.
    #[test]
    fn nothing_on_the_offer_moves_while_the_film_steps_back() {
        let mut upnext = risen();
        upnext.step_back();
        assert!(!upnext.fade().running());
        assert!(!upnext.column().running());
    }

    /// The display draws no frame while the film moves, so the column draws
    /// nothing until the film lands, with the OSD up or down.
    #[test]
    fn the_column_waits_for_the_landing() {
        let mut upnext = stepped();
        assert!(!upnext.draws_outside(false));
        assert!(!upnext.draws_outside(true));

        upnext.landed();
        settle(&mut upnext);
        assert!(upnext.draws_outside(false));
        assert!(upnext.draws_outside(true));
    }

    /// The column stands where the card's select goes, so a select at the
    /// bare video still takes the offer.
    #[test]
    fn the_column_takes_select_at_the_bare_video() {
        let mut upnext = stepped();
        upnext.landed();
        settle(&mut upnext);
        assert!(upnext.risen_outside(false));
    }

    #[test]
    fn the_landing_of_a_film_at_the_default_shows_no_column() {
        let mut upnext = risen();
        upnext.landed();
        settle(&mut upnext);
        assert_eq!(upnext.column().value(), 0.0);
    }

    /// back returns the film and takes the column and the card down at
    /// once, and the offer stays in the OSD.
    #[test]
    fn a_restore_takes_the_column_and_the_card_down_at_once() {
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

    /// The compositor returned the film on its own, so the corner card
    /// shows for its usual window.
    #[test]
    fn a_fall_back_shows_the_corner_card_for_its_window() {
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

    /// A take on the column fades the column out over the hand-off, and the
    /// corner card stays clear under it, so nothing draws once it ends.
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

    /// The compositor that returns the film during the wait brings no
    /// corner card back.
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

    /// A take before the film lands leaves the column clear when the
    /// landing comes.
    #[test]
    fn a_landing_during_the_wait_shows_no_column() {
        let mut upnext = stepped();
        upnext.take(0.0);
        upnext.landed();
        settle(&mut upnext);
        assert_eq!(upnext.column().value(), 0.0);
        assert!(!upnext.draws_outside(false));
    }

    #[test]
    fn a_new_offer_drops_the_step_back() {
        let mut upnext = stepped();
        upnext.receive(OFFER);
        assert!(!upnext.stepped_back());
    }
}
