// The series page's header: the logo or the title, the facts, the
// ratings, the focused episode's line and plot, and beside the text
// column the button row with an open episode's status line under it. The
// header draws no focus mark: `row_at` and `status_line` answer where the
// button row and the status line stand, and the browser's outline goes
// around the focused button or mark there.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Point, Rectangle};

use super::super::layout;
use super::super::{Focus, Series, Still, row, watch};
use super::{COLUMN, Page};
use crate::art::Art;
use crate::look;
use crate::views::icon::Icon;
use crate::views::stack::Stack;
use crate::views::{area, buttons, curtain, header, ratings, text, watch as line};

// The left edge of the button row: one gap past the right edge of the
// header's text column, in the room beside the column.
pub(crate) fn buttons_left(region: Rectangle, column: f32) -> f32 {
    region.x + column + layout::GAP
}

/// The top left of the button row at these bounds. The series' own row
/// draws beside the text column, level with the top of the plot, so the
/// header keeps the height it has on a series that holds no trailer. An
/// open episode's row draws higher, level with the ratings line, so the
/// row and its status line fit inside the header.
pub(crate) fn row_at(series: &Series, bounds: Rectangle) -> Point {
    let region = layout::header(bounds);
    let column = bounds.width * COLUMN;
    let left = buttons_left(region, column);
    match series.opened() {
        Some(_) => Point::new(left, region.y + layout::episode_row()),
        None => Point::new(left, plot_at(series, region, column).y),
    }
}

// Where the plot starts: under the logo, the facts line, the ratings, and
// the two lines of the focused episode, each a gap apart. A block that
// draws nothing takes no gap, the way the header's stack draws them.
fn plot_at(series: &Series, region: Rectangle, column: f32) -> Point {
    let mut stack = Stack::new(Point::new(region.x, region.y + layout::TOP), layout::GAP);
    stack.add(layout::LOGO_HEIGHT);
    let facts = text::measured_cut(&series.facts, look::FACTS, column);
    let lines = text::lines(&facts, look::FACTS, column).min(1);
    stack.add(text::height(lines, look::FACTS));
    stack.add(match series.ratings.is_empty() {
        true => 0.0,
        false => ratings::HEIGHT,
    });
    stack.add(text::height(1, look::FACTS));
    stack.add(text::height(1, look::FACTS));
    stack.at()
}

/// The status line under an open episode's row, with `focus` on the mark
/// that holds focus.
pub(crate) fn status_line<'a>(
    still: &Still,
    status: &'a watch::Status,
    marks: &'a [(Icon, &'static str)],
    focus: Option<usize>,
) -> line::Line<'a> {
    line::Line {
        share: watch::share(still.progress.as_ref()),
        status: &status.words,
        finished: status.finished,
        marks,
        focus,
        halo: true,
    }
}

/// The foot of the button row that starts at `at`, where the status line
/// under it starts.
pub(crate) fn under(at: Point) -> Point {
    Point::new(at.x, at.y + buttons::HEIGHT)
}

impl<A: Art> Page<'_, A> {
    // The header's blocks, from the logo down to the plot. Every block is
    // cut to its own lines, so a long title, a long line, or a long plot
    // never pushes the header past its fixed height. The focused
    // episode's line and plot stand in the place the series' plot takes on
    // a series whose episodes have not landed, and while a stripe holds
    // focus.
    pub(super) fn header(
        &self,
        frame: &mut canvas::Frame<Renderer>,
        store: &mut A,
        bounds: Rectangle,
        region: Rectangle,
        focus: Option<Focus>,
    ) {
        let series = self.series;
        // The column is a share of the whole frame's width, and not of the
        // content region's, so it ends inside the scrim's full shade.
        let column = bounds.width * COLUMN;
        let mut stack = Stack::new(Point::new(region.x, region.y + layout::TOP), layout::GAP);

        let title = area(stack.at().x, stack.at().y, column, layout::LOGO_HEIGHT);
        frame.with_clip(title, |frame| {
            header::title(
                frame,
                store,
                &header::Title {
                    library: &series.library,
                    logo: &series.logo,
                    name: &series.title,
                    at: stack.at(),
                    logo_box: (layout::LOGO_WIDTH, layout::LOGO_HEIGHT),
                    decode: curtain::logo_decode(bounds),
                    width: column,
                    size: look::HEAD_TITLE,
                    lifted: self.lifted,
                },
            );
        });
        stack.add(layout::LOGO_HEIGHT);

        // The facts line is one line, cut with an ellipsis where a long
        // list of genres runs past the column, so it never ends on a comma.
        let taken = text::block(
            frame,
            &text::measured_cut(&series.facts, look::FACTS, column),
            stack.at(),
            look::FACTS,
            look::muted(),
            column,
            1,
        );
        stack.add(taken);

        let taken = ratings::draw(frame, &series.ratings, stack.at());
        stack.add(taken);

        let (line, aired, plot) = match series.focused() {
            Some(still) => (
                still.facts.as_str(),
                still.aired.as_str(),
                still.plot.as_str(),
            ),
            None => ("", "", series.plot.as_str()),
        };
        // The episode's name is cut to one line with an ellipsis, and the
        // runtime and the air date take a line of their own under it, so
        // a long name never pushes the date out of the header.
        text::block(
            frame,
            &text::cut(line, look::FACTS, column),
            stack.at(),
            look::FACTS,
            look::text(),
            column,
            1,
        );
        stack.add(text::height(1, look::FACTS));
        text::block(
            frame,
            aired,
            stack.at(),
            look::FACTS,
            look::muted(),
            column,
            1,
        );
        stack.add(text::height(1, look::FACTS));
        text::block(
            frame,
            plot,
            stack.at(),
            look::PLOT,
            look::text(),
            column,
            layout::PLOT_LINES,
        );

        // An episode's own row draws in the same place, over the series'
        // row, while it holds focus.
        let beside = row_at(series, bounds);
        buttons::draw(frame, &row::Button::drawn(&series.buttons()), beside);
        let Some(still) = series.opened() else {
            return;
        };
        // The status line draws over the backdrop, past the scrim's full
        // shade, so its status draws over a halo. The bar is as wide as the
        // text column where the header has the room.
        let status = still.status();
        let marks = watch::Mark::drawn(&series.marks());
        let marked = match focus {
            Some(Focus::EpisodeMark(_, index)) => Some(index),
            _ => None,
        };
        line::draw(
            frame,
            &status_line(still, &status, &marks, marked),
            under(beside),
            column.min(region.x + region.width - beside.x),
        );
    }
}
