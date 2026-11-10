//! The chooser panel. A control opens one to pick a track. It draws a
//! vertical list on a dark plate, bottom-anchored above the control strip.
//! The focused row reads bright and has the accent underline under its text,
//! the same mark the control strip's focused stop takes, and the row of the
//! track that plays now has a dot before it. It is the one element that
//! captures input, so it draws on top of every region.

use iced::widget::canvas::Path;
use iced::{Point, Rectangle, Size};

use crate::canvas::{Anchor, Brush, Line, clip, measure};
use crate::theme;

const X: f32 = theme::MARGIN_X;
const W: f32 = 1180.0;
const ROW_H: f32 = 58.0;
const PAD: f32 = 24.0;
/// The panel grows upward from this baseline, so it floats over the regions
/// above the strip.
const BOTTOM: f32 = theme::PANEL_BOTTOM;
/// A subtitle list can run to dozens of tracks, more than fit on screen. The
/// panel shows a window of rows around the selection, so the current entry
/// stays in view however long the list.
const MAX_ROWS: usize = 8;

// The widest a row draws before it is clipped: the panel inside its
// padding. The toolkit measures the run.
const MAX_WIDTH: f32 = W - 2.0 * PAD;

/// The dot before the row of the track that plays now. It is centred in the
/// panel's left padding and on the label's line box, so it marks the row
/// without moving the label. The focus can move off the playing track, so
/// the dot and the underline are separate marks.
const DOT_R: f32 = 4.0;
const DOT_X: f32 = X + PAD / 2.0;

/// One panel of rows, and where each row draws.
#[derive(Debug, Clone, PartialEq)]
pub struct Panel {
    pub shape: Rectangle,
    pub rows: Vec<Row>,
}

/// One row of the list: the label as it draws, the row it draws on, whether
/// it is the entry the chooser stands on, and whether it is the track that
/// plays now.
#[derive(Debug, Clone, PartialEq)]
pub struct Row {
    pub label: String,
    pub y: f32,
    pub selected: bool,
    pub playing: bool,
}

impl Row {
    /// The underline under the focused row's label, as wide as the label
    /// draws. A row the focus is not on has none. The bar ends inside the
    /// row's own height, above the next row.
    pub fn underline(&self) -> Option<Rectangle> {
        self.selected.then(|| {
            theme::underline(
                X + PAD,
                self.y + theme::type_scale::LABEL,
                measure(&self.label, theme::type_scale::LABEL).min(MAX_WIDTH),
            )
        })
    }

    /// The centre of the dot before the row, when the row is the track that
    /// plays now.
    pub fn dot(&self) -> Option<Point> {
        self.playing
            .then(|| Point::new(DOT_X, self.y + theme::type_scale::LABEL / 2.0))
    }
}

/// The panel one list draws as, at the entry it stands on, above the
/// control strip, with the entry that plays now dotted.
pub fn panel(entries: &[String], selected: usize, playing: Option<usize>) -> Panel {
    let visible = entries.len().min(MAX_ROWS);
    let first = first(entries.len(), selected);

    let top = top(entries.len());
    let height = BOTTOM - top;
    let row0 = top + PAD;

    Panel {
        shape: Rectangle::new(Point::new(X, top), Size::new(W, height)),
        rows: (first..first + visible)
            .map(|at| Row {
                label: clip(&entries[at], theme::type_scale::LABEL, MAX_WIDTH),
                y: row0 + (at - first) as f32 * ROW_H,
                selected: at == selected,
                playing: playing == Some(at),
            })
            .collect(),
    }
}

/// The first entry of the window of rows a list of `len` entries shows.
fn first(len: usize, selected: usize) -> usize {
    if len > MAX_ROWS {
        selected.saturating_sub(MAX_ROWS / 2).min(len - MAX_ROWS)
    } else {
        0
    }
}

/// The top edge of the panel a list of `len` entries draws as above the
/// control strip.
pub fn top(len: usize) -> f32 {
    BOTTOM - (len.min(MAX_ROWS) as f32 * ROW_H + PAD * 2.0)
}

/// Draw one list: the plate, the underline under the focused row, the dot
/// before the playing row, and every row's label. The focused row's label
/// reads bright and the others muted. The plate has no border, because a
/// border in the accent would draw a second focus mark around the underline.
pub fn draw(brush: &mut Brush<'_>, entries: &[String], selected: usize, playing: Option<usize>) {
    let panel = panel(entries, selected, playing);
    brush.plate(panel.shape);
    for row in panel.rows {
        if let Some(bar) = row.underline() {
            brush.rect(bar, theme::color::focus());
        }
        let color = if row.selected {
            theme::color::text()
        } else {
            theme::color::muted()
        };
        if let Some(centre) = row.dot() {
            brush.shape(&Path::circle(centre, DOT_R), color);
        }
        brush.text(Line::new(
            row.label,
            Point::new(X + PAD, row.y),
            Anchor::TopLeft,
            theme::type_scale::LABEL,
            color,
        ));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn entries(count: usize) -> Vec<String> {
        (1..=count).map(|at| format!("Track {at}")).collect()
    }

    /// A list that fits draws every entry, and the panel grows upward from the
    /// baseline it hangs on.
    #[test]
    fn a_list_that_fits_draws_every_entry() {
        let panel = panel(&entries(3), 1, None);
        assert_eq!(
            panel.shape,
            Rectangle::new(Point::new(96.0, 654.0), Size::new(1180.0, 222.0))
        );
        assert_eq!(panel.rows.len(), 3);
        assert_eq!(panel.rows[0].y, 678.0);
        assert_eq!(panel.rows[1].y, 736.0);
        assert_eq!(panel.rows[2].y, 794.0);
        assert!(panel.rows[1].selected);
        assert!(!panel.rows[0].selected);
    }

    /// One entry draws one row, and the panel is one row plus its padding.
    #[test]
    fn one_entry_draws_one_row() {
        let panel = panel(&entries(1), 0, None);
        assert_eq!(panel.shape.height, 106.0);
        assert_eq!(panel.shape.y, 770.0);
        assert_eq!(panel.rows.len(), 1);
        assert_eq!(panel.rows[0].y, 794.0);
    }

    /// A longer list shows a window of eight rows, and the window follows the
    /// selection to the end of the list and no further.
    #[test]
    fn a_longer_list_shows_a_window_around_the_selection() {
        let list = entries(20);
        assert_eq!(panel(&list, 0, None).rows[0].label, "Track 1");
        assert_eq!(panel(&list, 3, None).rows[0].label, "Track 1");
        assert_eq!(panel(&list, 4, None).rows[0].label, "Track 1");
        assert_eq!(panel(&list, 5, None).rows[0].label, "Track 2");
        assert_eq!(panel(&list, 19, None).rows[0].label, "Track 13");
        assert_eq!(panel(&list, 19, None).rows.len(), 8);
        assert!(panel(&list, 19, None).rows[7].selected);
        assert_eq!(panel(&list, 19, None).shape.height, 512.0);
        assert_eq!(panel(&list, 19, None).shape.y, 364.0);
    }

    /// A row too wide for the panel ends in an ellipsis, and what is left plus
    /// the mark fits inside the panel's own padding.
    #[test]
    fn a_row_too_wide_for_the_panel_ends_in_an_ellipsis() {
        let long = "Director's commentary with the cast, the crew, every last one of their friends, and the caterers who fed them (ENG)";
        let clipped = panel(&[long.to_string()], 0, None).rows[0].label.clone();
        assert!(clipped.ends_with(crate::canvas::ELLIPSIS));
        assert!(clipped.chars().count() < long.chars().count());
        assert!(crate::canvas::measure(&clipped, theme::type_scale::LABEL) <= MAX_WIDTH);
        assert!(crate::canvas::measure(long, theme::type_scale::LABEL) > MAX_WIDTH);
    }

    /// A row that fits draws as it is, marks and all.
    #[test]
    fn a_row_that_fits_draws_as_it_is() {
        let row = |label: &str| panel(&[label.to_string()], 0, None).rows[0].label.clone();
        assert_eq!(row("English (ENG)"), "English (ENG)");
        assert_eq!(row(""), "");
        assert_eq!(row("\u{6771}\u{4EAC} (JPN)"), "\u{6771}\u{4EAC} (JPN)");
    }

    /// The focused row has the shared underline under its label: as wide as
    /// the label, one gap under the label's line box, and inside the row, so
    /// it ends above the next row's label. No other row has one.
    #[test]
    fn the_focused_row_is_underlined() {
        let panel = panel(&entries(3), 1, None);
        let row = &panel.rows[1];
        let bar = row.underline().expect("the focused row is underlined");
        assert_eq!(
            bar,
            Rectangle::new(
                Point::new(120.0, 780.0),
                Size::new(measure("Track 2", theme::type_scale::LABEL), 4.0),
            )
        );
        assert!(bar.y + bar.height <= panel.rows[2].y);
        assert_eq!(panel.rows[0].underline(), None);
        assert_eq!(panel.rows[2].underline(), None);
    }

    /// The row of the entry that plays now has a dot in the panel's left
    /// padding, on the label's middle, wherever the focus is. A list with no
    /// entry playing has no dot.
    #[test]
    fn the_playing_row_is_dotted() {
        let panel = panel(&entries(3), 1, Some(2));
        assert_eq!(panel.rows[2].dot(), Some(Point::new(108.0, 814.0)));
        assert_eq!(panel.rows[0].dot(), None);
        assert_eq!(panel.rows[1].dot(), None);
        assert!(
            super::panel(&entries(3), 1, None)
                .rows
                .iter()
                .all(|row| row.dot().is_none())
        );
    }

    /// In a list longer than the window, the dot follows its entry and not
    /// its place on screen.
    #[test]
    fn the_dot_follows_its_entry_through_the_window() {
        let list = entries(20);
        let dotted = |selected| {
            panel(&list, selected, Some(10))
                .rows
                .into_iter()
                .find(|row| row.playing)
                .map(|row| row.label)
        };
        assert_eq!(dotted(10), Some("Track 11".to_string()));
        assert_eq!(dotted(14), Some("Track 11".to_string()));
        assert_eq!(dotted(0), None);
        assert_eq!(dotted(19), None);
    }
}
