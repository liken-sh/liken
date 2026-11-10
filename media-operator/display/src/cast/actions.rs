//! The actions on one cast card. A select on a focused card opens a panel
//! in the track chooser's style above the row, so the card it belongs to
//! stays in view. The panel captures every press until it closes, as a
//! chooser does: up and down move between its rows, select takes the row,
//! and back closes it.
//!
//! "Next scene" seeks to the start of the person's next scene. A scene is
//! the run of appearances the row itself uses: a gap of at most `LINGER`
//! between two spans of one person does not end it. So the row never offers
//! a span of the scene the viewer is already in, and the seek lands where
//! the person arrives in the row again. A person who does not appear again
//! reads "No later scene", and a select cannot take that row.

use serde_json::json;

use super::{Cast, HEADING_Y, LINGER, Spans};
use crate::canvas::Brush;
use crate::chooser;
use crate::film::Film;
use crate::focus::Action;
use crate::ipc::Command;
use crate::scrubber;

/// The panel's baseline: a little above the row's heading, so the panel
/// covers no card and the heading still reads as the row's.
const BOTTOM: f32 = HEADING_Y - 12.0;

/// The rows the panel offers, top to bottom.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Row {
    Next,
}

const ROWS: [Row; 1] = [Row::Next];

/// The open panel: the person it acts on, by the key the spans file names
/// them under, and the row it stands on. The person stays the panel's even
/// when they leave the row while it is open.
#[derive(Debug, Clone, PartialEq)]
pub struct Actions {
    key: String,
    at: usize,
}

impl Spans {
    /// The start of this person's next scene after `second`, or nothing when
    /// they do not appear again. The walk follows the person's spans in time
    /// order, and a span starts a new scene when it starts more than `LINGER`
    /// after the end of the person's span before it.
    fn next_scene(&self, key: &str, second: f64) -> Option<f64> {
        let mut last: Option<f64> = None;
        for span in self
            .spans
            .iter()
            .filter(|span| span.people.iter().any(|k| k == key))
        {
            let new = last.is_none_or(|end| span.start - end > LINGER);
            if span.start > second && new {
                return Some(span.start);
            }
            last = Some(span.end);
        }
        None
    }
}

impl Actions {
    fn new(key: String, next: Option<f64>) -> Self {
        let mut actions = Self { key, at: 0 };
        actions.at = actions.first(next);
        actions
    }

    /// Whether a select can take one row now.
    fn enabled(row: Row, next: Option<f64>) -> bool {
        match row {
            Row::Next => next.is_some(),
        }
    }

    /// The first row a select can take, or the top row when none can.
    fn first(&self, next: Option<f64>) -> usize {
        ROWS.iter()
            .position(|row| Self::enabled(*row, next))
            .unwrap_or(0)
    }

    /// The row the panel stands on. A row that stopped taking a select
    /// while the panel was open passes the mark to the first that does.
    fn here(&self, next: Option<f64>) -> usize {
        match Self::enabled(ROWS[self.at], next) {
            true => self.at,
            false => self.first(next),
        }
    }

    /// Move to the nearest row in one direction that a select can take, and
    /// hold at the ends of the list.
    fn step(&mut self, direction: i64, next: Option<f64>) {
        let mut at = self.here(next) as i64;
        loop {
            at += direction;
            let Some(row) = usize::try_from(at).ok().and_then(|at| ROWS.get(at)) else {
                return;
            };
            if Self::enabled(*row, next) {
                self.at = at as usize;
                return;
            }
        }
    }

    fn labels(next: Option<f64>) -> Vec<String> {
        ROWS.iter()
            .map(|row| match (row, next) {
                (Row::Next, Some(start)) => {
                    format!("Next scene \u{00B7} {}", scrubber::timecode(start))
                }
                (Row::Next, None) => "No later scene".to_string(),
            })
            .collect()
    }

    fn panel(&self, next: Option<f64>) -> chooser::Panel {
        let mut panel = chooser::panel_at(&Self::labels(next), self.here(next), BOTTOM);
        for (row, drawn) in ROWS.iter().zip(panel.rows.iter_mut()) {
            drawn.enabled = Self::enabled(*row, next);
            drawn.selected &= drawn.enabled;
        }
        panel
    }
}

impl Cast {
    /// Whether the actions panel is open and captures the presses.
    pub fn acting(&self) -> bool {
        self.actions.is_some()
    }

    /// Open the panel on the focused card. A row with nobody in it opens
    /// nothing.
    pub fn act(&mut self, film: &Film) {
        let row = self.row(film);
        if row.is_empty() {
            return;
        }
        let (focus, _) = self.place(&row);
        let key = row[focus].key.to_string();
        let next = self.next_scene(&key, film);
        self.actions = Some(Actions::new(key, next));
    }

    /// Close the panel. The focus stays on the card that opened it.
    pub fn close(&mut self) {
        self.actions = None;
    }

    fn next_scene(&self, key: &str, film: &Film) -> Option<f64> {
        self.spans.as_ref()?.next_scene(key, film.position?)
    }

    /// One press while the panel is open. A select on "Next scene" seeks to
    /// the scene and closes the panel, so the row shows the scene the seek
    /// lands in. left and right do nothing, and back is the router's.
    pub fn handle(&mut self, action: Action, film: &Film) -> Vec<Command> {
        let Some(actions) = self.actions.as_ref() else {
            return Vec::new();
        };
        let next = self.next_scene(&actions.key, film);
        let Some(actions) = self.actions.as_mut() else {
            return Vec::new();
        };
        match action {
            Action::Up => actions.step(-1, next),
            Action::Down => actions.step(1, next),
            Action::Select => {
                let row = ROWS[actions.here(next)];
                if !Actions::enabled(row, next) {
                    return Vec::new();
                }
                let commands = match (row, next) {
                    (Row::Next, Some(start)) => {
                        vec![vec![json!("seek"), json!(start), json!("absolute+exact")]]
                    }
                    (Row::Next, None) => Vec::new(),
                };
                self.close();
                return commands;
            }
            _ => {}
        }
        Vec::new()
    }

    /// The top edge of the open panel, or nothing while it is closed.
    pub fn actions_top(&self) -> Option<f32> {
        self.actions
            .as_ref()
            .map(|_| chooser::top_at(ROWS.len(), BOTTOM))
    }

    /// The open panel, as the chooser draws it.
    pub fn draw_actions(&self, brush: &mut Brush<'_>, film: &Film) {
        let Some(actions) = self.actions.as_ref() else {
            return;
        };
        let next = self.next_scene(&actions.key, film);
        chooser::paint(brush, actions.panel(next));
    }
}

#[cfg(test)]
mod tests {
    use super::super::tests::{paused_at, scene};
    use super::*;
    use crate::theme;
    use iced::{Point, Rectangle, Size};

    /// The open panel as it draws at one moment of the film.
    fn panel(cast: &Cast, film: &Film) -> chooser::Panel {
        let actions = cast.actions.as_ref().expect("the panel is open");
        actions.panel(cast.next_scene(&actions.key, film))
    }

    /// One person, a, whose spans gap by less than `LINGER` inside a scene
    /// and by more between scenes: a scene from 0 to 20, another from 40 to
    /// 50, and one last span alone at 70.
    fn scenes() -> Cast {
        scene(&[
            (0.0, 5.0, &["a", "b"]),
            (12.0, 20.0, &["a"]),
            (40.0, 50.0, &["a"]),
            (70.0, 72.0, &["a"]),
        ])
    }

    /// Each case is the playhead and the start "Next scene" seeks to. A span
    /// that starts after the playhead inside the same scene, such as the
    /// one at 12 for a playhead at 8, is the scene the viewer is in, so the
    /// next scene starts at 40.
    #[test]
    fn the_next_scene_starts_where_the_person_arrives_again() {
        let cast = scenes();
        let cases = [
            (2.0, Some(40.0)),
            (8.0, Some(40.0)),
            (15.0, Some(40.0)),
            (45.0, Some(70.0)),
            (55.0, Some(70.0)),
            (71.0, None),
        ];
        for (second, want) in cases {
            assert_eq!(
                cast.next_scene("a", &paused_at(second)),
                want,
                "at {second}"
            );
        }
    }

    /// A person who lingers in the row after their span ended, and whose
    /// next span starts more than `LINGER` after it, arrives in a new scene
    /// there, the way the row would show them arrive.
    #[test]
    fn a_lingering_person_arrives_again_after_the_linger() {
        let cast = scene(&[(0.0, 5.0, &["a"]), (17.0, 20.0, &["a"])]);
        assert_eq!(cast.people(&paused_at(8.0)).len(), 1);
        assert_eq!(cast.next_scene("a", &paused_at(8.0)), Some(17.0));
    }

    /// A select on "Next scene" seeks to the scene's start and closes the
    /// panel.
    #[test]
    fn a_select_on_next_scene_seeks_and_closes() {
        let mut cast = scenes();
        let film = paused_at(8.0);
        cast.act(&film);
        assert!(cast.acting());
        assert_eq!(
            cast.handle(Action::Select, &film),
            vec![vec![json!("seek"), json!(40.0), json!("absolute+exact")]]
        );
        assert!(!cast.acting());
    }

    /// With no later scene the row reads so, and a select on it does
    /// nothing and leaves the panel open.
    #[test]
    fn no_later_scene_is_a_row_a_select_cannot_take() {
        let mut cast = scenes();
        let film = paused_at(71.0);
        cast.act(&film);
        let panel = panel(&cast, &film);
        assert_eq!(panel.rows[0].label, "No later scene");
        assert!(!panel.rows[0].enabled);
        assert!(!panel.rows[0].selected);
        assert!(cast.handle(Action::Select, &film).is_empty());
        assert!(cast.acting());
    }

    /// The label names the start as the scrubber names a position.
    #[test]
    fn the_label_reads_the_start_as_the_scrubber_does() {
        assert_eq!(
            Actions::labels(Some(1994.0)),
            vec!["Next scene \u{00B7} 33:14"]
        );
        assert_eq!(
            Actions::labels(Some(3994.0)),
            vec!["Next scene \u{00B7} 1:06:34"]
        );
    }

    /// The panel acts on the focused card's person.
    #[test]
    fn the_panel_acts_on_the_focused_person() {
        let mut cast = scene(&[(0.0, 5.0, &["a", "b"]), (30.0, 35.0, &["b"])]);
        let film = paused_at(2.0);
        cast.step(1, &film);
        cast.act(&film);
        assert_eq!(cast.actions.as_ref().unwrap().key, "b");
        assert_eq!(
            cast.handle(Action::Select, &film),
            vec![vec![json!("seek"), json!(30.0), json!("absolute+exact")]]
        );
    }

    /// The panel stands above the row's heading, as wide as the chooser.
    #[test]
    fn the_panel_stands_above_the_row() {
        let mut cast = scenes();
        let film = paused_at(8.0);
        cast.act(&film);
        assert_eq!(
            panel(&cast, &film).shape,
            Rectangle::new(Point::new(theme::MARGIN_X, 534.0), Size::new(1180.0, 106.0))
        );
    }

    /// A row with nobody in it opens nothing.
    #[test]
    fn an_empty_row_opens_nothing() {
        let mut cast = scenes();
        cast.act(&paused_at(100.0));
        assert!(!cast.acting());
    }
}
