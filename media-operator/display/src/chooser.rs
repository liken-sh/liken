//! The panel that a track control opens so the viewer can pick a track: a
//! list on a dark plate above the control strip. The focused row is bright
//! and underlined in the accent color, as the focused stop of the strip is,
//! and the row of the playing track has a dot before it. The panel takes
//! all input while it is open, so it draws on top of everything else.

use iced::widget::canvas::Path;
use iced::{Point, Rectangle, Size};

use crate::canvas::{Anchor, Brush, Line, clip, measure};
use crate::theme;

const X: f32 = theme::MARGIN_X;
const W: f32 = 1180.0;
const ROW_H: f32 = 58.0;
const PAD: f32 = 24.0;
/// The panel grows upward from this edge, over the regions above the
/// control strip.
const BOTTOM: f32 = theme::PANEL_BOTTOM;
/// A subtitle list can have more tracks than fit on screen, so the panel
/// shows at most this many rows and scrolls them with the focus.
const MAX_ROWS: usize = 8;

/// The widest a label can draw before `clip` shortens it.
const MAX_WIDTH: f32 = W - 2.0 * PAD;

/// The dot before the playing row, centred in the panel's left padding so
/// it does not move the label. The focus can move off the playing track, so
/// the dot is a separate mark from the underline.
const DOT_R: f32 = 4.0;
const DOT_X: f32 = X + PAD / 2.0;

/// The panel's plate and its visible rows.
#[derive(Debug, Clone, PartialEq)]
pub struct Panel {
    pub shape: Rectangle,
    pub rows: Vec<Row>,
}

/// One visible row. `label` is clipped to fit, `y` is the top edge,
/// `selected` means it has the focus, and `playing` means it is the playing
/// track.
#[derive(Debug, Clone, PartialEq)]
pub struct Row {
    pub label: String,
    pub y: f32,
    pub selected: bool,
    pub playing: bool,
}

impl Row {
    /// The underline under the focused row's label, as wide as the label,
    /// or `None` for a row without the focus.
    pub fn underline(&self) -> Option<Rectangle> {
        self.selected.then(|| {
            theme::underline(
                X + PAD,
                self.y + theme::type_scale::LABEL,
                measure(&self.label, theme::type_scale::LABEL).min(MAX_WIDTH),
            )
        })
    }

    /// The centre of the dot before the row, or `None` when the row is not
    /// the playing track.
    pub fn dot(&self) -> Option<Point> {
        self.playing
            .then(|| Point::new(DOT_X, self.y + theme::type_scale::LABEL / 2.0))
    }
}

/// The panel for a list of entries, with the focus on `selected` and a dot
/// on the entry `playing`.
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

/// The index of the first visible entry. It keeps `selected` near the
/// middle of the visible rows, and stops at the end of the list.
fn first(len: usize, selected: usize) -> usize {
    if len > MAX_ROWS {
        selected.saturating_sub(MAX_ROWS / 2).min(len - MAX_ROWS)
    } else {
        0
    }
}

/// The top edge of the panel for a list of `len` entries.
pub fn top(len: usize) -> f32 {
    BOTTOM - (len.min(MAX_ROWS) as f32 * ROW_H + PAD * 2.0)
}

/// Draw the panel. The plate has no border, because an accent border would
/// be a second focus mark besides the underline.
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

    /// A list that fits draws every entry, and the panel grows upward from
    /// its bottom edge.
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

    #[test]
    fn one_entry_draws_one_row() {
        let panel = panel(&entries(1), 0, None);
        assert_eq!(panel.shape.height, 106.0);
        assert_eq!(panel.shape.y, 770.0);
        assert_eq!(panel.rows.len(), 1);
        assert_eq!(panel.rows[0].y, 794.0);
    }

    /// A list longer than `MAX_ROWS` shows eight rows that follow the focus
    /// and stop at the end of the list.
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

    /// A label too wide for the panel ends in an ellipsis, and the clipped
    /// label fits inside the panel's padding.
    #[test]
    fn a_row_too_wide_for_the_panel_ends_in_an_ellipsis() {
        let long = "Director's commentary with the cast, the crew, every last one of their friends, and the caterers who fed them (ENG)";
        let clipped = panel(&[long.to_string()], 0, None).rows[0].label.clone();
        assert!(clipped.ends_with(crate::canvas::ELLIPSIS));
        assert!(clipped.chars().count() < long.chars().count());
        assert!(crate::canvas::measure(&clipped, theme::type_scale::LABEL) <= MAX_WIDTH);
        assert!(crate::canvas::measure(long, theme::type_scale::LABEL) > MAX_WIDTH);
    }

    #[test]
    fn a_row_that_fits_draws_as_it_is() {
        let row = |label: &str| panel(&[label.to_string()], 0, None).rows[0].label.clone();
        assert_eq!(row("English (ENG)"), "English (ENG)");
        assert_eq!(row(""), "");
        assert_eq!(row("\u{6771}\u{4EAC} (JPN)"), "\u{6771}\u{4EAC} (JPN)");
    }

    /// Only the focused row is underlined, and the underline ends above the
    /// next row's label.
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

    /// The playing row has a dot wherever the focus is, and a list with no
    /// playing entry has none.
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

    /// The dot stays with its entry as the rows scroll.
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
