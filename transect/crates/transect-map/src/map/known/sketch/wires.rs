//! The wires of a sketch, as the page draws them. A wire joins the
//! facing edges of two roles' areas, with an arrow at `to` and the words
//! of `carries` on it. Its state follows the roles it joins, so a wire
//! reads the same way in every sketch:
//!
//! - A wire between two roles that have parts is solid.
//! - A wire with `via` runs through the role between when that role has
//!   a part, as two wires. When it has none, the wire runs straight past
//!   the role's area.
//! - A run that meets an expected role with no part is dashed.
//! - A run that meets a role the page leaves out is not drawn.
//!
//! The page lays the grid out in the browser, so `wires.js` measures the
//! ports and draws each path. Here each end gets its edge, and its place
//! among the runs that meet the same edge of the same area, in the order
//! of their other ends along that edge. Each wire is also a sentence
//! that names the parts as the drawing names them, and its source.

use transect_graph::links::Reference;
use transect_resources::knowledge::Elevation;
use transect_resources::shapes::Edge;

/// Whether a role that a sketch places has parts on the page.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Presence {
    /// The role draws one or more parts.
    Parts,
    /// The role is expected and draws its empty place.
    Empty,
    /// The role is not drawn.
    Absent,
}

/// How the page draws one role, as a wire names it.
#[derive(Clone, Debug, PartialEq)]
pub struct Placed {
    pub presence: Presence,
    /// The role's parts as the drawing names them, each linked to its
    /// page, or what its empty place says, such as "no Lamp".
    pub names: Vec<Reference>,
    /// The field that the role's parts are read from.
    pub path: String,
}

/// One wire of the elevation as the page draws it: one run, or two
/// through a role that stands between.
#[derive(Clone, Debug, PartialEq)]
pub struct SketchWire {
    pub carries: String,
    pub runs: Vec<Run>,
    /// The wire as a sentence, with the parts as the drawing names them:
    /// words, and each part linked to its page.
    pub sentence: Vec<Reference>,
    /// The fields that the ends were read from, and the knowledge that
    /// states the wire.
    pub source: String,
}

/// One line that the page draws between two areas.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Run {
    pub from: WireEnd,
    pub to: WireEnd,
}

/// One end of a run: the area it meets, the edge of the area, and its
/// place among the runs that meet that edge.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct WireEnd {
    pub area: String,
    /// The role of the area is expected and has no part.
    pub empty: bool,
    pub edge: Edge,
    /// The end's place along the edge, from the left or the top.
    pub slot: usize,
    /// How many runs meet that edge of that area.
    pub of: usize,
}

impl Run {
    /// Whether the run meets a role with no part, so it is dashed.
    pub fn dashed(&self) -> bool {
        self.from.empty || self.to.empty
    }

    /// The class of the run's path: solid, or dashed when it meets a
    /// role with no part, as the ring draws its lines.
    pub fn class(&self) -> &'static str {
        match self.dashed() {
            true => "section-path section-path-dashed",
            false => "section-path",
        }
    }
}

impl SketchWire {
    /// The sentence as plain text.
    pub fn text(&self) -> String {
        self.sentence
            .iter()
            .map(|piece| piece.text.as_str())
            .collect()
    }

    /// What a reader sees while pointing at the wire: the sentence and
    /// its source.
    pub fn title(&self) -> String {
        format!("{}, {}", self.text(), self.source)
    }

    /// Whether a run of the wire meets a role with no part.
    pub fn dashed(&self) -> bool {
        self.runs.iter().any(Run::dashed)
    }
}

/// The wires of `elevation` that the page draws, where `placed` says how
/// the page draws each role, and `knowledge` names the `KindKnowledge`
/// that states them.
pub fn wires(
    elevation: &Elevation,
    knowledge: &str,
    placed: impl Fn(&str) -> Placed,
) -> Vec<SketchWire> {
    let grid = &elevation.grid;
    let mut wires: Vec<SketchWire> = Vec::new();
    for one in &elevation.wires {
        let (from, to) = (one.from.as_str(), one.to.as_str());
        let through = one
            .via
            .as_deref()
            .filter(|via| placed(via).presence == Presence::Parts);
        let stops: Vec<&str> = [Some(from), through, Some(to)]
            .into_iter()
            .flatten()
            .collect();
        let drawn: Vec<&str> = stops
            .into_iter()
            .filter(|name| placed(name).presence != Presence::Absent)
            .collect();
        let pairs = drawn.windows(2).filter_map(|pair| {
            let (from_edge, to_edge) = grid.facing(pair[0], pair[1])?;
            let end = |area: &str, edge| WireEnd {
                area: area.into(),
                empty: placed(area).presence == Presence::Empty,
                edge,
                slot: 0,
                of: 1,
            };
            Some(Run {
                from: end(pair[0], from_edge),
                to: end(pair[1], to_edge),
            })
        });
        let runs: Vec<Run> = pairs.collect();
        if runs.is_empty() {
            continue;
        }
        let names: Vec<Placed> = drawn.iter().map(|name| placed(name)).collect();
        wires.push(SketchWire {
            carries: one.carries.clone(),
            sentence: sentence(&one.carries, &names),
            source: source(&names, knowledge),
            runs,
        });
    }
    spread(&mut wires, elevation);
    wires
}

/// "grain from Node a through Sifter b to Bin c", with each
/// part a link, for the roles that a wire passes, in order.
fn sentence(carries: &str, stops: &[Placed]) -> Vec<Reference> {
    let word = |text: &str| Reference {
        text: text.into(),
        href: None,
    };
    let mut pieces = vec![word(carries)];
    let last = stops.len().saturating_sub(1);
    for (at, stop) in stops.iter().enumerate() {
        pieces.push(word(match at {
            0 => " from ",
            at if at == last => " to ",
            _ => " through ",
        }));
        pieces.extend(listed(&stop.names));
    }
    pieces
}

/// "read from status.a, status.b, and status.c, as the KindKnowledge k
/// states", with the fields of the roles that a wire passes, in order.
fn source(stops: &[Placed], knowledge: &str) -> String {
    let fields: Vec<Reference> = stops
        .iter()
        .map(|stop| Reference {
            text: stop.path.clone(),
            href: None,
        })
        .collect();
    let fields: String = listed(&fields).iter().map(|p| p.text.as_str()).collect();
    format!("read from {fields}, as the KindKnowledge {knowledge} states")
}

/// `names` joined as a list in a sentence: "a", "a and b", or "a, b, and
/// c".
pub(super) fn listed(names: &[Reference]) -> Vec<Reference> {
    let word = |text: &str| Reference {
        text: text.into(),
        href: None,
    };
    let mut pieces = Vec::new();
    for (at, name) in names.iter().enumerate() {
        if at > 0 {
            pieces.push(word(match (names.len(), at + 1 == names.len()) {
                (2, _) => " and ",
                (_, true) => ", and ",
                _ => ", ",
            }));
        }
        pieces.push(name.clone());
    }
    pieces
}

/// One end of a run, with what orders it among the ends that meet the
/// same edge of the same area.
struct Meeting {
    area: String,
    edge: Edge,
    /// The other end's row and column for a side, or its column and row
    /// for the top or the bottom, then the run's place in the list.
    order: (usize, usize, usize),
    at: (usize, usize),
    from: bool,
}

/// Gives each end its place among the ends that meet the same edge of
/// the same area, in the order of their other ends along that edge: by
/// row for a side, and by column for the top or the bottom.
fn spread(wires: &mut [SketchWire], elevation: &Elevation) {
    let grid = &elevation.grid;
    let meeting = |end: &WireEnd, other: &WireEnd, at: (usize, usize), place: usize, from| {
        let rows = grid.rows_of(&other.area).unwrap_or_default();
        let columns = grid.columns_of(&other.area).unwrap_or_default();
        let (first, second) = match end.edge {
            Edge::Left | Edge::Right => (rows.start, columns.start),
            Edge::Top | Edge::Bottom => (columns.start, rows.start),
        };
        Meeting {
            area: end.area.clone(),
            edge: end.edge,
            order: (first, second, place),
            at,
            from,
        }
    };
    let runs = wires.iter().enumerate().flat_map(|(wire, w)| {
        w.runs
            .iter()
            .enumerate()
            .map(move |(run, r)| ((wire, run), r))
    });
    let meetings: Vec<Meeting> = runs
        .enumerate()
        .flat_map(|(place, (at, r))| {
            [
                meeting(&r.from, &r.to, at, place, true),
                meeting(&r.to, &r.from, at, place, false),
            ]
        })
        .collect();
    for one in &meetings {
        let same = meetings
            .iter()
            .filter(|m| (&m.area, m.edge) == (&one.area, one.edge));
        let (before, of) = same.fold((0, 0), |(before, of), m| {
            (before + usize::from(m.order < one.order), of + 1)
        });
        let run = &mut wires[one.at.0].runs[one.at.1];
        let end = match one.from {
            true => &mut run.from,
            false => &mut run.to,
        };
        end.slot = before;
        end.of = of;
    }
}

#[cfg(test)]
mod tests;
