//! A wire says what passes from one role to another in an elevation,
//! such as the picture from a machine to its screen. The author places
//! the roles in the sketch, so the grid in the YAML shows the order of
//! the stages, and `transect` draws each wire between the facing edges
//! of the two roles' areas. A wire with `via` says that a third role may
//! stand between the two: the page draws the wire through it when it
//! has a part, and past it when it has none.

use serde::Deserialize;

use super::grid::Grid;
use crate::shapes::Shape;

/// One wire of an elevation. The parser guarantees that `from`, `to`,
/// and `via` are roles that the sketch places, that no two of them are
/// the same role, and that `carries` has words.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Wire {
    pub from: String,
    pub to: String,
    pub via: Option<String>,
    /// What the wire carries, such as "picture". The page writes it on
    /// the wire.
    pub carries: String,
}

/// One entry of an elevation's `wires`, as written in the document.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Written {
    from: String,
    to: String,
    via: Option<String>,
    carries: String,
}

/// The wires of `written`, checked against the roles that `grid`
/// places. The error names the first fault and the wire it is in.
pub fn read(written: Vec<Written>, grid: &Grid) -> Result<Vec<Wire>, String> {
    let names = grid.names();
    let mut wires: Vec<Wire> = Vec::new();
    for (at, written) in written.into_iter().enumerate() {
        let fault = |text: String| format!("wires[{at}]: {text}");
        let ends = [Some(&written.from), Some(&written.to), written.via.as_ref()];
        if let Some(name) = ends
            .into_iter()
            .flatten()
            .find(|n| !names.contains(&n.as_str()))
        {
            return Err(fault(format!(
                "{name} is not a role that the sketch places"
            )));
        }
        if written.from == written.to {
            return Err(fault(format!("the wire joins {} to itself", written.from)));
        }
        if let Some(via) = &written.via
            && (*via == written.from || *via == written.to)
        {
            return Err(fault(format!("via is {via}, which is an end of the wire")));
        }
        if written.carries.trim().is_empty() {
            return Err(fault("carries has no words".into()));
        }
        let wire = Wire {
            from: written.from,
            to: written.to,
            via: written.via,
            carries: written.carries.trim().into(),
        };
        if wires
            .iter()
            .any(|w| (&w.from, &w.to) == (&wire.from, &wire.to))
        {
            return Err(fault(format!(
                "an earlier wire already runs from {} to {}",
                wire.from, wire.to
            )));
        }
        wires.push(wire);
    }
    Ok(wires)
}

impl Wire {
    /// The pairs of roles that the page can join: the two ends, and with
    /// `via`, each end to the role between them.
    pub fn pairs(&self) -> Vec<(&str, &str)> {
        let (from, to) = (self.from.as_str(), self.to.as_str());
        match self.via.as_deref() {
            Some(via) => vec![(from, via), (via, to), (from, to)],
            None => vec![(from, to)],
        }
    }

    /// The fault of the first pair that meets a role whose shape has no
    /// port on the facing edge, where `shape_of` gives the shape that
    /// each role is drawn in.
    fn unported(&self, grid: &Grid, shape_of: impl Fn(&str) -> Option<Shape>) -> Option<String> {
        for (a, b) in self.pairs() {
            let (from, to) = grid.facing(a, b)?;
            for (name, edge) in [(a, from), (b, to)] {
                let shape = shape_of(name)?;
                if !shape.ports.iter().any(|port| port.edge == edge) {
                    return Some(format!(
                        "the wire from {} to {} meets {name}, and its shape {} has no port on its {} edge",
                        self.from,
                        self.to,
                        shape.name,
                        edge.word()
                    ));
                }
            }
        }
        None
    }
}

/// Leaves out of `wires` each wire that meets a role whose shape has no
/// port on the facing edge, where `shape_of` gives the shape that each
/// role is drawn in, and returns the fault of each. The shapes are found
/// after the knowledge loads, so this runs then and not in `read`.
pub fn leave_unported(
    wires: &mut Vec<Wire>,
    grid: &Grid,
    shape_of: impl Fn(&str) -> Option<Shape>,
) -> Vec<String> {
    let mut faults = Vec::new();
    wires.retain(|wire| match wire.unported(grid, &shape_of) {
        Some(fault) => {
            faults.push(format!("{fault}, so the wire is not drawn"));
            false
        }
        None => true,
    });
    faults
}

#[cfg(test)]
mod tests;
