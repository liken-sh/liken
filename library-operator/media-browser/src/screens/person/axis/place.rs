// Where each part of the axis goes on the screen. Every place is measured
// from the line: a rectangle one pixel tall across the content region,
// whose `y` is the line's own. A year is a stretch of the line from its
// start to the start of the next year, so a credit's mark sits in the
// middle of its year and a decade tick on the year's first day.

use iced_winit::core::{Point, Rectangle};

use super::Axis;
use crate::look;
use crate::views::{area, text};

/// The size of one credit's dot.
pub const DOT: f32 = 12.0;

/// The space between the centers of two dots of one year.
pub const STEP: f32 = 15.0;

// How far over the line the center of a year's first dot is.
const FIRST_ROW: f32 = 14.0;

/// How tall the tallest year's bar is.
pub const BARS: f32 = 80.0;

// The focus underline of a year: how far under the line its middle is,
// and how long it is before the stroke's ends. The bar is the focus
// stroke's width tall and reaches half of it past each end, so it runs
// from 5 to 9 under the line, clear of the bright part of the line at 1
// over to 2 under, and it is 20 long, a dot and a stroke's width at each
// side. A fixed length stays as visible on a career of eighty years,
// where a year is narrower than a dot, as on one of ten.
const UNDERLINE_DOWN: f32 = 7.0;
const UNDERLINE: f32 = DOT + look::MARK;

// The space between a bar and the bars of the years beside it, and the
// inset of a bar's fills inside its frame.
const BAR_GAP: f32 = 3.0;
const BAR_INSET: f32 = 2.0;

// Where the decade ticks start over the line, and how tall they are; and
// the same for the taller tick of a death.
const TICK_UP: f32 = 4.0;
const TICK: f32 = 9.0;
const DEATH_UP: f32 = 6.0;
const DEATH_TICK: f32 = 18.0;

// How far under the line the tick labels start, and how far over it the
// words at the left end start.
const TICK_LABELS: f32 = 20.0;
const LEFT_WORDS: f32 = 28.0;

// How far over the line the label over the focused year starts. It sits
// over a stack of five dots and over the tallest bar, so no mark covers
// its words.
const LABEL: f32 = 121.0;

// How far under the line the mark of a focused poster's year starts in
// the bar mode, and how tall it is.
const BAR_MARK_DOWN: f32 = 5.0;
const BAR_MARK: f32 = 3.0;

/// How tall a running series' bar is, and how tall the bright part of the
/// line is.
pub const RUN: f32 = 4.0;
pub const WINDOW: f32 = 3.0;

impl Axis {
    // How many years the line spans.
    fn across(&self) -> f32 {
        (self.end - self.start + 1) as f32
    }

    /// The `x` of a point in time on the line, in years.
    pub fn x(&self, line: Rectangle, year: f32) -> f32 {
        line.x + (year - self.start as f32) / self.across() * line.width
    }

    /// The `x` of the middle of one year, where its marks are.
    pub fn middle(&self, line: Rectangle, year: i64) -> f32 {
        self.x(line, year as f32 + 0.5)
    }

    /// The width of one year on the line.
    pub fn year_width(&self, line: Rectangle) -> f32 {
        line.width / self.across()
    }

    /// The focus underline of one year with credits, centred on the
    /// year's dots, as the flat rectangle that the browser's focus stroke
    /// draws as a bar. Every year's underline is one length, so a press
    /// left or right moves the bar across and never changes its length.
    pub fn underline(&self, line: Rectangle, index: usize) -> Rectangle {
        let year = self
            .years
            .get(index)
            .map(|year| year.year)
            .unwrap_or(self.start);
        area(
            self.middle(line, year) - UNDERLINE / 2.0,
            line.y + UNDERLINE_DOWN,
            UNDERLINE,
            0.0,
        )
    }

    /// The center of the `stacked`th dot of one year, counted from the
    /// line up.
    pub fn dot(&self, line: Rectangle, year: i64, stacked: usize) -> Point {
        Point::new(
            self.middle(line, year),
            line.y - FIRST_ROW - stacked as f32 * STEP,
        )
    }

    /// The frame of one year's bar, and the two fills inside it: the
    /// credits everyone has seen at the bottom, and over them the credits
    /// some have seen. Every bar is on one scale, the tallest year at
    /// `BARS`.
    pub fn bar(&self, line: Rectangle, index: usize) -> [Rectangle; 3] {
        let year = &self.years[index];
        let scale = |count: usize| count as f32 / self.tallest.max(1) as f32 * BARS;
        let left = self.x(line, year.year as f32) + BAR_GAP;
        let width = (self.year_width(line) - 2.0 * BAR_GAP).max(1.0);
        let height = scale(year.credits.len());
        let everyone = scale(year.everyone);
        let some = scale(year.some);
        let inner = (width - 2.0 * BAR_INSET).max(1.0);
        [
            area(left, line.y - height, width, height),
            area(left + BAR_INSET, line.y - everyone, inner, everyone),
            area(left + BAR_INSET, line.y - everyone - some, inner, some),
        ]
    }

    /// The mark under one year's bar while the poster of one of its credits
    /// holds focus: as wide as the bar, under the bright part of the line
    /// and over the decades' years.
    pub fn bar_mark(&self, line: Rectangle, index: usize) -> Rectangle {
        let [frame, _, _] = self.bar(line, index);
        area(frame.x, line.y + BAR_MARK_DOWN, frame.width, BAR_MARK)
    }

    /// One decade tick, at the first day of its year.
    pub fn tick(&self, line: Rectangle, year: i64) -> Rectangle {
        area(self.x(line, year as f32), line.y - TICK_UP, 1.0, TICK)
    }

    /// The tick of a death inside the span, in the middle of its year.
    pub fn death_tick(&self, line: Rectangle, year: i64) -> Rectangle {
        area(self.middle(line, year), line.y - DEATH_UP, 1.0, DEATH_TICK)
    }

    /// The `y` the tick labels start at.
    pub fn tick_labels(line: Rectangle) -> f32 {
        line.y + TICK_LABELS
    }

    /// The `y` the words at the left end start at: "born", or the death
    /// of a person who died before the axis starts.
    pub fn left_words(line: Rectangle) -> f32 {
        line.y - LEFT_WORDS
    }

    /// The band words this wide draw in when they are centered on `x`,
    /// at this `y`, moved inside the line where they would run past one of
    /// its ends. The label over a year near an edge stays on the screen
    /// this way, and so does the first decade's year.
    pub fn centered(line: Rectangle, x: f32, y: f32, width: f32, size: f32) -> Rectangle {
        let left = (x - width / 2.0)
            .min(line.x + line.width - width)
            .max(line.x);
        area(left, y, width, text::height(1, size))
    }

    /// The band of the label over the focused year, for words this wide,
    /// centered on the year and kept inside the line.
    pub fn label_band(&self, line: Rectangle, middle: f32, width: f32) -> Rectangle {
        Self::centered(line, middle, line.y - LABEL, width, look::CAPTION)
    }

    /// The bright part of the line, from the first day of the first year
    /// to the last day of the last year.
    pub fn window(&self, line: Rectangle, years: (i64, i64)) -> (f32, f32) {
        (
            self.x(line, years.0 as f32),
            self.x(line, years.1 as f32 + 1.0),
        )
    }

    /// The bar of a series that runs from this year to the end of the axis,
    /// at the height of the first row of dots.
    pub fn run(&self, line: Rectangle, from: i64) -> Rectangle {
        let left = self.x(line, from.max(self.start) as f32);
        let right = line.x + line.width;
        area(
            left,
            line.y - FIRST_ROW - RUN / 2.0,
            (right - left).max(0.0),
            RUN,
        )
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::screens::person::life::Life;
    use crate::screens::person::tests::careers::career;

    // The line of the final boards: the content region's width at the
    // `y` the page draws it at.
    const LINE: Rectangle = Rectangle {
        x: 96.0,
        y: 457.0,
        width: 1728.0,
        height: 1.0,
    };

    // The career of the boards' first comp, born 1970, with the axis
    // from 1970 to 2026.
    fn mara() -> Axis {
        let years = [1985, 1986, 1988, 1991, 1991, 2025];
        Axis::of(&career(&years), &Life::of("1970", "", []), 2026).expect("an axis")
    }

    fn near(one: f32, other: f32) -> bool {
        (one - other).abs() < 0.05
    }

    #[test]
    fn a_year_takes_its_share_of_the_line() {
        let axis = mara();
        assert!(
            near(axis.year_width(LINE), 30.32),
            "{}",
            axis.year_width(LINE)
        );
        assert!(near(axis.tick(LINE, 1980).x, 399.16));
        assert!(near(axis.x(LINE, 2027.0), LINE.x + LINE.width));
    }

    // The underline of 1991 is centred on its dots and draws, through the
    // focus stroke, as a bar from 5 to 9 under the line: under the bright
    // part of the line, which ends 2 under it, and 20 long on every year.
    #[test]
    fn the_underline_is_centred_on_the_year_under_the_bright_part() {
        let axis = mara();
        let underline = axis.underline(LINE, 3);
        assert!(near(underline.center_x(), axis.middle(LINE, 1991)));
        assert_eq!(underline.height, 0.0);
        let top = underline.y - look::MARK / 2.0;
        assert_eq!(top, LINE.y + 5.0);
        assert_eq!(underline.y + look::MARK / 2.0, LINE.y + 9.0);
        assert!(top > LINE.y - 1.0 + WINDOW, "{underline:?}");
        assert_eq!(underline.width + look::MARK, 20.0);
        assert_eq!(axis.underline(LINE, 0).width, underline.width);
    }

    #[test]
    fn dots_of_one_year_stack_upward_from_the_line() {
        let axis = mara();
        let first = axis.dot(LINE, 1991, 0);
        assert!(near(first.x, 747.79), "{first:?}");
        assert_eq!(first.y, 443.0);
        assert_eq!(axis.dot(LINE, 1991, 1).y, 443.0 - STEP);
        let label = axis.label_band(LINE, 0.0, 100.0);
        assert!(axis.dot(LINE, 1991, 4).y - DOT / 2.0 > label.y + label.height);
    }

    #[test]
    fn a_bar_stands_on_the_line_and_fills_from_the_bottom() {
        let mut axis = mara();
        axis.tallest = 4;
        axis.years[3].everyone = 1;
        axis.years[3].some = 1;
        let [frame, everyone, some] = axis.bar(LINE, 3);
        assert_eq!(frame.y + frame.height, LINE.y);
        assert_eq!(frame.height, BARS / 2.0);
        assert_eq!(everyone.height, BARS / 4.0);
        assert_eq!(some.y + some.height, everyone.y);
        let label = axis.label_band(LINE, 0.0, 100.0);
        assert!(frame.y > label.y + label.height);
    }

    #[test]
    fn the_label_stays_inside_the_line_near_either_end() {
        let axis = mara();
        let left = axis.label_band(LINE, LINE.x + 4.0, 200.0);
        assert_eq!(left.x, LINE.x);
        let right = axis.label_band(LINE, LINE.x + LINE.width, 200.0);
        assert_eq!(right.x + right.width, LINE.x + LINE.width);
        let middle = axis.label_band(LINE, 900.0, 200.0);
        assert_eq!(middle.x, 800.0);
        assert_eq!(middle.y, 336.0);
    }

    // The mark of a focused poster's year in the bar mode is a short
    // stroke under the bar, between the line and the decades' years, so
    // it touches neither the bar nor the bright part of the line.
    #[test]
    fn the_mark_of_a_year_stands_under_its_bar() {
        let axis = mara();
        let [frame, _, _] = axis.bar(LINE, 3);
        let mark = axis.bar_mark(LINE, 3);
        assert_eq!(mark.x, frame.x);
        assert_eq!(mark.width, frame.width);
        assert!(mark.y > LINE.y + WINDOW, "{mark:?}");
        assert!(mark.y + mark.height < Axis::tick_labels(LINE), "{mark:?}");
    }

    #[test]
    fn the_window_spans_whole_years() {
        let axis = mara();
        let (left, right) = axis.window(LINE, (1991, 1997));
        assert!(near(left, 732.63), "{left}");
        assert!(near(right - left, 212.21), "{right}");
    }
}
