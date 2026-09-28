//! The chooser panel. A control opens one to pick a track. It draws a vertical
//! list, bottom-anchored above the control strip, with the focused entry
//! marked. It is the one element that captures input, so it draws on top of
//! every region.

use iced::{Point, Rectangle, Size};

use crate::canvas::{Anchor, Brush, Line, clip};
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

/// One panel of rows, and where each row draws.
#[derive(Debug, Clone, PartialEq)]
pub struct Panel {
    pub shape: Rectangle,
    pub rows: Vec<Row>,
}

/// One row of the list: the label as it draws, the row it draws on, and
/// whether it is the entry the chooser stands on.
#[derive(Debug, Clone, PartialEq)]
pub struct Row {
    pub label: String,
    pub y: f32,
    pub selected: bool,
}

/// The panel one list draws as, at the entry it stands on.
pub fn panel(entries: &[String], selected: usize) -> Panel {
    let visible = entries.len().min(MAX_ROWS);
    let first = if entries.len() > MAX_ROWS {
        selected
            .saturating_sub(MAX_ROWS / 2)
            .min(entries.len() - MAX_ROWS)
    } else {
        0
    };

    let height = visible as f32 * ROW_H + PAD * 2.0;
    let top = BOTTOM - height;
    let row0 = top + PAD;

    Panel {
        shape: Rectangle::new(Point::new(X, top), Size::new(W, height)),
        rows: (first..first + visible)
            .map(|at| Row {
                label: clip(&entries[at], theme::type_scale::LABEL, MAX_WIDTH),
                y: row0 + (at - first) as f32 * ROW_H,
                selected: at == selected,
            })
            .collect(),
    }
}

pub fn draw(brush: &mut Brush<'_>, entries: &[String], selected: usize) {
    let panel = panel(entries, selected);
    brush.panel(panel.shape);
    for row in panel.rows {
        if row.selected {
            brush.rounded(
                Rectangle::new(
                    Point::new(X + PAD / 2.0, row.y - 6.0),
                    Size::new(W - PAD, ROW_H - 4.0),
                ),
                8.0,
                theme::at(theme::color::fill(), theme::alpha::HIGHLIGHT),
            );
        }
        brush.text(Line::new(
            row.label,
            Point::new(X + PAD, row.y),
            Anchor::TopLeft,
            theme::type_scale::LABEL,
            if row.selected {
                theme::color::SHADOW
            } else {
                theme::color::muted()
            },
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
        let panel = panel(&entries(3), 1);
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
        let panel = panel(&entries(1), 0);
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
        assert_eq!(panel(&list, 0).rows[0].label, "Track 1");
        assert_eq!(panel(&list, 3).rows[0].label, "Track 1");
        assert_eq!(panel(&list, 4).rows[0].label, "Track 1");
        assert_eq!(panel(&list, 5).rows[0].label, "Track 2");
        assert_eq!(panel(&list, 19).rows[0].label, "Track 13");
        assert_eq!(panel(&list, 19).rows.len(), 8);
        assert!(panel(&list, 19).rows[7].selected);
        assert_eq!(panel(&list, 19).shape.height, 512.0);
        assert_eq!(panel(&list, 19).shape.y, 364.0);
    }

    /// A row too wide for the panel ends in an ellipsis, and what is left plus
    /// the mark fits inside the panel's own padding.
    #[test]
    fn a_row_too_wide_for_the_panel_ends_in_an_ellipsis() {
        let long = "Director's commentary with the cast, the crew, every last one of their friends, and the caterers who fed them (ENG)";
        let clipped = panel(&[long.to_string()], 0).rows[0].label.clone();
        assert!(clipped.ends_with(crate::canvas::ELLIPSIS));
        assert!(clipped.chars().count() < long.chars().count());
        assert!(crate::canvas::measure(&clipped, theme::type_scale::LABEL) <= MAX_WIDTH);
        assert!(crate::canvas::measure(long, theme::type_scale::LABEL) > MAX_WIDTH);
    }

    /// A row that fits draws as it is, marks and all.
    #[test]
    fn a_row_that_fits_draws_as_it_is() {
        let row = |label: &str| panel(&[label.to_string()], 0).rows[0].label.clone();
        assert_eq!(row("English (ENG)"), "English (ENG)");
        assert_eq!(row(""), "");
        assert_eq!(row("\u{6771}\u{4EAC} (JPN)"), "\u{6771}\u{4EAC} (JPN)");
    }
}
