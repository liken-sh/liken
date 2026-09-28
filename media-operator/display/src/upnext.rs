//! The up-next offer: the work the Play names as the one that follows it.
//! The command sidecar sends the block as the next script-message.
//! This module draws the block as a chip while the film plays, and as a card
//! near the end of it. Focus routes a select on it.

use iced::{Color, Point, Rectangle, Size};
use serde_json::Value;

use crate::canvas::{Anchor, Brush, Canvas, Line, clip, measure};
use crate::fade::{Clock, Fade, Hide};
use crate::film::Film;
use crate::theme;

/// The card's box in canvas pixels. It is 440 wide with its right edge on the
/// margin, and its bottom edge stays above the row where the scrubber draws
/// the time label, so the card never covers the scrubber.
pub const CARD_W: f32 = 440.0;
const CARD_TOP: f32 = 380.0;
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
const DETAIL_H: f32 = 42.0;
const CARD_H: f32 = ART_H + PAD_Y + REASON_PITCH + TITLE_PITCH + DETAIL_H + PAD_Y;

/// The chip's row, above the right end of the bar and above the time label.
pub(crate) const CHIP_Y: f32 = 806.0;
pub(crate) const CHIP_H: f32 = 34.0;
pub(crate) const CHIP_PAD_X: f32 = 22.0;
pub(crate) const CHIP_PAD_Y: f32 = 6.0;

/// The two amounts of a work that may remain when the card rises: a share
/// of its length, and a number of seconds. The rule takes whichever leaves
/// less time, so the seconds apply only to a work over 100 minutes. It is
/// the shape of the watched rule, which `library-operator` states in
/// `watched.go` and in `media-browser/src/catalog/progress.rs`, at three
/// fifths of that rule's two amounts. So both caps begin to apply at the
/// same length, and the card never rises before the work counts as watched.
/// The two rules differ because the card marks the credits and the watched
/// rule marks the end of the story.
const RISE_PERCENT: f64 = 3.0;
const RISE_SECONDS: f64 = 180.0;

/// The second at which the time rule raises the card: the length less the
/// smaller of the two amounts. The marks cap a rise after a scene at this
/// point, so a card never rises later than the time rule puts it.
pub(crate) fn time_rise(duration: f64) -> f64 {
    duration - (duration * RISE_PERCENT / 100.0).min(RISE_SECONDS)
}

/// The card draws at this fraction of its alpha while it waits for the next
/// work to start.
const WAIT_FADE: f32 = 0.6;

/// The words the chip and the waiting card draw. The block carries the three
/// lines of the card and nothing for these two states, so they are spelled
/// here.
const CHIP_WORD: &str = "UP NEXT";
const WAIT_WORD: &str = "Starting";

// The seam the decode fills. The card asks for the offer's picture where
// it draws the art plate, the decode answers with the size it draws at, and
// the card hands back the box to draw it in.
pub trait Art {
    fn next(&self) -> Option<Size>;

    fn draw(&self, brush: &mut Brush<'_>, bounds: Rectangle);
}

/// The block the sidecar sent. A block with none of the three lines and no
/// art is not an offer.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Offer {
    pub reason: Option<String>,
    pub title: Option<String>,
    pub detail: Option<String>,
    pub art: Option<String>,
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
        };
        (offer != Self::default()).then_some(offer)
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
    /// `credits` is where the item's marks place the rise: the start of the
    /// credits in its second half, or the point after a scene that follows
    /// them. `Marks::credits` states the rule. The time that remains decides
    /// only for an item with no such mark.
    ///
    /// It answers whether the card rose, because that is the one push of this
    /// property that draws anything.
    pub fn on_percent(&mut self, value: Option<f64>, film: &Film, credits: Option<f64>) -> bool {
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
        let reached = match credits {
            Some(start) => duration * value / 100.0 >= start,
            None => {
                let remaining = duration * (100.0 - value) / 100.0;
                remaining <= (duration * RISE_PERCENT / 100.0).min(RISE_SECONDS)
            }
        };
        if !reached {
            return false;
        }
        self.risen = true;
        self.clock.show(Hide::Arm);
        true
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

    /// take records that focus broadcast the ask. The card then waits for the
    /// operator to end this Play and start the next one. Nothing here ends the
    /// wait: the film ends, or a back press ends the run.
    pub fn take(&mut self) {
        if self.offer.is_none() {
            return;
        }
        self.waiting = true;
        self.clock.ask(Hide::Cancel);
    }

    /// A new offer, or the loss of one, drops every state the last one had.
    fn reset(&mut self) {
        self.risen = false;
        self.expanded = false;
        self.waiting = false;
        self.clock = Clock::default();
        self.clock.ask(Hide::Cancel);
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

    fn chip_line(&self, canvas: &Canvas, focused: bool) -> Line {
        Line::new(
            self.label(),
            Point::new(canvas.right(), CHIP_Y),
            Anchor::TopRight,
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

    /// The card's three lines, each clipped to the width inside the pad.
    /// detail is the third line, because the wait replaces it with a word of
    /// its own.
    fn card_lines(&self, canvas: &Canvas, detail: Option<&str>, detail_color: Color) -> Vec<Line> {
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
        if let Some(detail) = detail {
            lines.push(Line::new(
                clip(detail, theme::type_scale::SMALL, room),
                Point::new(x, y),
                Anchor::TopLeft,
                theme::type_scale::SMALL,
                detail_color,
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
            brush.panel(self.chip_panel(&canvas));
        }
        brush.text(self.chip_line(&canvas, focused));
    }

    /// The card: the art over the three lines the block spells.
    fn card(
        &self,
        brush: &mut Brush<'_>,
        focused: bool,
        detail: Option<&str>,
        detail_color: Color,
        art: &dyn Art,
    ) {
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
        for line in self.card_lines(&canvas, detail, detail_color) {
            brush.text(line);
        }
    }

    /// Draw the offer as part of the OSD, the chip before the rise and the
    /// card after it, at the OSD's own fade. The waiting card is not part of
    /// the OSD, so it does not fade out with it.
    pub fn draw(&self, brush: &mut Brush<'_>, focused: bool, art: &dyn Art) {
        if self.offer.is_none() || self.waiting {
            return;
        }
        if self.showing_card() {
            self.card(brush, focused, self.detail(), theme::color::muted(), art);
        } else {
            self.chip(brush, focused);
        }
    }

    /// Draw what the offer draws over the bare video, on a clock of its own:
    /// the risen card for its few seconds, and the dimmed card for the whole
    /// wait. Once the card fades, nothing remains of it, because a mark at the
    /// screen edge would draw over the film for the rest of it. Before the
    /// rise, a hidden OSD draws nothing for the offer.
    pub fn draw_outside(&self, brush: &mut Brush<'_>, osd_visible: bool, art: &dyn Art) {
        if !self.draws_outside(osd_visible) {
            return;
        }
        let (fade, detail, color) = if self.waiting {
            (WAIT_FADE, Some(WAIT_WORD), theme::color::fill())
        } else {
            (self.fade().value(), self.detail(), theme::color::muted())
        };
        brush.at_fade(fade, |brush| {
            self.card(brush, false, detail, color, art);
        });
    }

    /// Whether the offer has anything to draw over the bare video, so the
    /// frame loop draws no layer for a card that is not there.
    pub fn draws_outside(&self, osd_visible: bool) -> bool {
        if self.offer.is_none() {
            return false;
        }
        if self.waiting {
            return true;
        }
        !osd_visible && self.risen && self.fade().value() > 0.0
    }

    fn detail(&self) -> Option<&str> {
        self.offer
            .as_ref()
            .and_then(|offer| offer.detail.as_deref())
    }
}

fn card_x(canvas: &Canvas) -> f32 {
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
        "detail":"Harbor Lights · S02 · 45 min"}"#;

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
        assert!(upnext.on_percent(Some(98.0), &film(100.0), None));
        upnext
    }

    fn canvas() -> Canvas {
        Canvas::default()
    }

    #[test]
    fn no_offer_draws_nothing_and_offers_no_stop() {
        let upnext = UpNext::default();
        assert!(!upnext.available());
        assert!(!upnext.draws_outside(false));
        assert!(
            upnext
                .card_lines(&canvas(), None, theme::color::muted())
                .is_empty()
        );
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
        assert_eq!(line.at, Point::new(RIGHT, 806.0));
        assert_eq!(line.anchor, Anchor::TopRight);
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
    /// hour film raises the card three minutes from its end and not at three
    /// percent of its length.
    #[test]
    fn a_long_film_raises_the_card_three_minutes_from_its_end() {
        let long = film(4.0 * 60.0 * 60.0);
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);

        assert!(!upnext.on_percent(Some(97.0), &long, None));
        assert!(!upnext.showing_card());
        assert!(upnext.on_percent(Some(98.75), &long, None));
        assert!(upnext.showing_card());
    }

    #[test]
    fn a_short_film_raises_the_card_at_ninety_seven_percent() {
        let short = film(100.0);
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);

        assert!(!upnext.on_percent(Some(96.0), &short, None));
        assert!(upnext.on_percent(Some(97.0), &short, None));
    }

    /// A work with no length, and a push that carries no number, raise
    /// nothing.
    #[test]
    fn a_work_with_no_length_never_raises_the_card() {
        let mut upnext = UpNext::default();
        upnext.receive(OFFER);
        assert!(!upnext.on_percent(Some(99.0), &Film::default(), None));
        assert!(!upnext.on_percent(Some(99.0), &film(0.0), None));
        assert!(!upnext.on_percent(None, &film(100.0), None));
        assert!(!upnext.showing_card());
    }

    /// The rise holds after it happens, so a seek back does not take the card
    /// down, and it arms the hide window once.
    #[test]
    fn the_rise_holds_and_arms_one_hide_window() {
        let mut upnext = risen();
        assert_eq!(upnext.take_hide(), Hide::Arm);
        assert!(!upnext.on_percent(Some(20.0), &film(100.0), None));
        assert!(upnext.showing_card());
        assert_eq!(upnext.take_hide(), Hide::Keep);
    }

    /// Credits in the second half move the rise to their start, whether that
    /// is earlier or later than the time that remains would put it.
    #[test]
    fn credits_raise_the_card_at_their_start() {
        let cases = [
            ("credits before the three minutes", 5400.0, 89.9, 90.0),
            ("credits inside the last three minutes", 5950.0, 99.0, 99.2),
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
        assert!(!upnext.on_percent(Some(99.0), &film(100.0), None));
    }

    #[test]
    fn the_card_holds_the_comps_box() {
        let upnext = risen();
        let box_ = upnext.card_box(&canvas());
        assert_eq!(box_.x, CARD_X);
        assert_eq!(box_.y, 380.0);
        assert_eq!(box_.width, 440.0);
        assert_eq!(box_.height, 416.0);
        assert_eq!(box_.x + box_.width, RIGHT);
        assert!(box_.y + box_.height <= FLOOR);
    }

    #[test]
    fn the_art_plate_covers_the_top_of_the_card() {
        let upnext = risen();
        let plate = upnext.art_plate(&canvas());
        assert_eq!(plate.x, CARD_X);
        assert_eq!(plate.y, 380.0);
        assert_eq!(plate.width, 440.0);
        assert_eq!(plate.height, 248.0);
    }

    /// The three lines are cumulative: the art, a pad, then each line's own
    /// pitch.
    #[test]
    fn the_card_stacks_its_three_lines_under_the_art() {
        let upnext = risen();
        let lines = upnext.card_lines(&canvas(), upnext.detail(), theme::color::muted());

        assert_eq!(lines.len(), 3);
        assert_eq!(lines[0].content, "NEXT IN HARBOR LIGHTS \u{b7} S02");
        assert_eq!(lines[0].at, Point::new(TEXT_X, 644.0));
        assert_eq!(lines[0].size, theme::type_scale::TINY);
        assert_eq!(lines[0].color, theme::color::muted());

        assert_eq!(lines[1].content, "E05 \u{b7} The Long Tide");
        assert_eq!(lines[1].at, Point::new(TEXT_X, 684.0));
        assert_eq!(lines[1].size, theme::type_scale::LABEL);
        assert_eq!(lines[1].color, theme::color::text());

        assert_eq!(lines[2].content, "Harbor Lights \u{b7} S02 \u{b7} 45 min");
        assert_eq!(lines[2].at, Point::new(TEXT_X, 738.0));
        assert_eq!(lines[2].size, theme::type_scale::SMALL);
        assert_eq!(lines[2].color, theme::color::muted());
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
        let lines = upnext.card_lines(&canvas(), upnext.detail(), theme::color::muted());
        assert_eq!(lines.len(), 1);
        assert_eq!(lines[0].at.y, 684.0);
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

        assert!(upnext.on_percent(Some(98.0), &film(100.0), None));
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

    /// The waiting card draws over the video whether or not the OSD is up, and
    /// it draws nothing inside the OSD.
    #[test]
    fn the_waiting_card_draws_outside_the_osd_alone() {
        let mut upnext = risen();
        upnext.take();

        assert!(upnext.waiting());
        assert!(upnext.draws_outside(false));
        assert!(upnext.draws_outside(true));
        assert_eq!(upnext.take_hide(), Hide::Cancel);
    }

    /// The wait replaces the third line with a word of its own, in the accent.
    #[test]
    fn the_waiting_card_reads_starting_and_drops_the_detail() {
        let mut upnext = risen();
        upnext.take();

        let lines = upnext.card_lines(&canvas(), Some(WAIT_WORD), theme::color::fill());
        assert_eq!(lines[2].content, "Starting");
        assert_eq!(lines[2].color, theme::color::fill());
        assert_eq!(lines[1].content, "E05 \u{b7} The Long Tide");
    }

    /// take on a display with no offer records no wait.
    #[test]
    fn a_take_with_no_offer_records_no_wait() {
        let mut upnext = UpNext::default();
        upnext.take();
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
        upnext.take();

        upnext.receive(r#"{"title":"E06 The Turning"}"#);
        assert!(upnext.available());
        assert!(!upnext.showing_card());
        assert!(!upnext.waiting());
        assert_eq!(upnext.fade().value(), 0.0);
        assert_eq!(
            upnext.card_lines(&canvas(), None, theme::color::muted())[0].content,
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
        assert_eq!(bounds.y, 380.0);
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
}
