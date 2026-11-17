//! The side boxes of the spine's rows: what has an edge to or from the
//! workload, each in its own band's row, with callers on the left and
//! what the workload reaches on the right.  A column draws at most
//! `MOST_SIDE` boxes and then one box in place of the rest, so a row
//! stays short however many workloads call this one.  A fold of only one
//! box saves no room, so the column draws that box instead.

use super::{Band, Look, SectionBox};
use crate::map::section::layout::Side;
use crate::map::section::lines::Style;
use crate::map::section::sentence;
use transect_graph::edges::{Endpoint, Provenance};
use transect_graph::links::Part;

/// The most side boxes a column draws one by one.
pub const MOST_SIDE: usize = 3;

/// One box beside the spine and the line that joins it to the spine.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SideBox {
    pub band: Band,
    pub side: Side,
    pub boxed: SectionBox,
    /// The line's label: its edge kind.
    pub label: String,
    pub style: Style,
    /// Whether the edge runs from the side box into the spine, as a
    /// caller's does, so the arrow points at the spine.
    pub inward: bool,
    /// The machine whose box the line joins, for a device a pod holds
    /// there; any other side box joins the workload.
    pub machine: Option<String>,
    /// The machine's name, for the box of a machine that the workload is
    /// configured to reach.  When one of the workload's pods runs there,
    /// the spine already draws the machine, so the line goes to that box
    /// instead.
    pub names: Option<String>,
    /// The edge's source when the API did not give the edge, which the
    /// line shows on hover.
    pub title: Option<String>,
    /// The name that a folded box gives this box.  A section view may
    /// draw several boxes with one name, so it is an object's kind and
    /// name, or else the box's title.
    pub kinded: Option<String>,
    /// The graph endpoint that this box draws, by which `ties` finds a
    /// tie to another side box.
    pub end: Option<Endpoint>,
}

/// A line between two side boxes in the right column, for an edge
/// between the endpoints that they draw, such as a device to the radio
/// it pairs through.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Tie {
    pub from: Endpoint,
    pub to: Endpoint,
    pub label: String,
    pub style: Style,
    /// Where the edge came from, which the line shows on hover.
    pub title: Option<String>,
}

impl SideBox {
    pub fn new(band: Band, side: Side, boxed: SectionBox, label: &str, style: Style) -> Self {
        Self {
            band,
            side,
            boxed,
            label: label.into(),
            style,
            inward: false,
            machine: None,
            names: None,
            title: None,
            kinded: None,
            end: None,
        }
    }

    /// The box with `end` as the graph endpoint that it draws.
    pub fn standing(self, end: Endpoint) -> Self {
        Self {
            end: Some(end),
            ..self
        }
    }

    /// The box with the name that a fold gives it: `object`'s kind and
    /// name.
    pub fn of(self, object: &transect_graph::edges::Object) -> Self {
        let kinded = transect_graph::edges::kind_and_name(&object.kind, &object.name);
        Self {
            kinded: Some(kinded),
            ..self
        }
    }

    /// The name that a fold gives it.
    fn folded_name(&self) -> &str {
        self.kinded.as_deref().unwrap_or(&self.boxed.title)
    }

    /// The box with its line titled by the source that `provenance` gives
    /// for the edge, unless the API gave the edge.
    pub fn from(self, provenance: &Provenance) -> Self {
        let title = (*provenance != Provenance::Kubernetes).then(|| provenance.title());
        Self { title, ..self }
    }

    pub fn inward(self) -> Self {
        Self {
            inward: true,
            ..self
        }
    }

    pub fn on(self, machine: Option<String>) -> Self {
        Self { machine, ..self }
    }

    pub fn naming(self, machine: &str) -> Self {
        Self {
            names: Some(machine.into()),
            ..self
        }
    }
}

/// The boxes of `sides` in `band`'s row on `side`, in the order given,
/// with every one past the first `MOST_SIDE` folded into one box that
/// names them, when there are at least two to fold.  The folded box is
/// joined to the spine in the same way as the first box that it replaces.
pub fn column(sides: &[SideBox], band: &Band, side: Side) -> Vec<SideBox> {
    let all: Vec<&SideBox> = sides
        .iter()
        .filter(|s| s.band == *band && s.side == side)
        .collect();
    if all.len() <= MOST_SIDE + 1 {
        return all.into_iter().cloned().collect();
    }
    let (shown, rest) = all.split_at(MOST_SIDE);
    let mut column: Vec<SideBox> = shown.iter().map(|&s| s.clone()).collect();
    let names: Vec<&str> = rest.iter().map(|s| s.folded_name()).collect();
    let linked = rest.iter().map(|s| Part {
        text: s.folded_name().into(),
        href: s.boxed.href.clone(),
    });
    let folded = SectionBox::new(
        sentence::more_title(rest.len()),
        vec![names.join(", ")],
        Look::Plain,
    )
    .with_hint(names.join("\n"))
    .with_names(linked.collect());
    column.push(SideBox {
        boxed: folded,
        end: None,
        ..rest[0].clone()
    });
    column
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_graph::edges::Object;

    fn caller(name: &str) -> SideBox {
        let boxed = SectionBox::new(name.into(), vec!["Deployment".into()], Look::Plain);
        SideBox::new(
            Band::Layer("apps".into()),
            Side::Left,
            boxed,
            "depends on",
            Style::Dashed,
        )
    }

    fn titles(column: &[SideBox]) -> Vec<(&str, &[String])> {
        column
            .iter()
            .map(|s| (s.boxed.title.as_str(), s.boxed.lines.as_slice()))
            .collect()
    }

    #[rstest]
    #[case::three(3, vec!["caller-0", "caller-1", "caller-2"])]
    #[case::four_draw_the_fourth(4, vec!["caller-0", "caller-1", "caller-2", "caller-3"])]
    #[case::five_fold_two(5, vec!["caller-0", "caller-1", "caller-2", "+2 more"])]
    #[case::six_fold_past_three(6, vec!["caller-0", "caller-1", "caller-2", "+3 more"])]
    fn folds_a_column_past_three_when_it_saves_two(
        #[case] count: usize,
        #[case] expected: Vec<&str>,
    ) {
        let sides: Vec<SideBox> = (0..count).map(|i| caller(&format!("caller-{i}"))).collect();
        let found = column(&sides, &Band::Layer("apps".into()), Side::Left);
        let found: Vec<&str> = titles(&found).into_iter().map(|(t, _)| t).collect();
        assert_eq!(found, expected);
    }

    #[rstest]
    fn the_folded_box_names_what_it_stands_for() {
        let sides: Vec<SideBox> = (0..5).map(|i| caller(&format!("caller-{i}"))).collect();
        let found = column(&sides, &Band::Layer("apps".into()), Side::Left);
        assert_eq!(found[3].boxed.lines, vec!["caller-3, caller-4".to_string()]);
    }

    #[rstest]
    fn the_folded_box_names_each_object_with_its_kind() {
        let player = Object::new("example.com", "Player", Some("den"), "den");
        let remote = Object::new("example.com", "Remote", Some("den"), "den");
        let mut sides: Vec<SideBox> = (0..3).map(|i| caller(&format!("caller-{i}"))).collect();
        sides.push(caller("den").of(&player));
        sides.push(caller("den").of(&remote));
        let found = column(&sides, &Band::Layer("apps".into()), Side::Left);
        assert_eq!(
            found[3].boxed.lines,
            vec!["Player den, Remote den".to_string()]
        );
    }

    #[rstest]
    fn keeps_each_column_to_its_band_and_side() {
        let other = SideBox {
            side: Side::Right,
            ..caller("database")
        };
        let sides = vec![caller("web"), other];
        let found = column(&sides, &Band::Layer("apps".into()), Side::Right);
        assert_eq!(
            titles(&found),
            vec![("database", &["Deployment".to_string()][..])]
        );
    }
}
