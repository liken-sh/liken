//! A shape that `turns` lies on its side in an area wider than it is
//! tall, as a center speaker lies under a screen. The quarter turn moves
//! each place, inset, port, join, and corner: the top edge becomes the
//! left edge, and the left edge the bottom.

use super::length::Corner;
use super::{Art, Clear, Outline, Piece, Port, Round, Shape, Size};
use crate::shapes::place::Edge;

impl Shape {
    /// The shape on its side.
    pub fn turned(&self) -> Self {
        let Size {
            design,
            width,
            share,
        } = self.size.clone();
        let design = design.map(|[w, h]| [h, w]);
        // The part keeps the same range of heights on its side as it had
        // standing, so its widths scale with its proportions.
        let ratio = design.map_or(1.0, |[w, h]| w / h);
        let [t, r, b, l] = self.text.inset;
        Self {
            size: Size {
                design,
                width: width.map(|w| (w * ratio).round()),
                share,
            },
            // A shape that turns states no axis, so its axis is the
            // middle of its height on its side too.
            axis: design.map(|[_, h]| h / 2.0),
            outline: Outline {
                at: self.outline.at.turned(),
                round: turned(self.outline.round),
                ..self.outline.clone()
            },
            text: super::Text {
                inset: [r, b, l, t],
                ..self.text.clone()
            },
            pieces: self.pieces.iter().map(piece).collect(),
            joins: self.joins.iter().map(|edge| edge.turned()).collect(),
            ports: self.ports.iter().map(port).collect(),
            ..self.clone()
        }
    }
}

fn piece(piece: &Piece) -> Piece {
    Piece {
        at: piece.at.turned(),
        round: turned(piece.round),
        art: piece.art.as_ref().map(|art| Art {
            view: [art.view[1], art.view[0]],
            turned: true,
            ..art.clone()
        }),
        clear: piece.clear.map(|clear| Clear {
            edge: clear.edge.turned(),
            ..clear
        }),
        ..piece.clone()
    }
}

/// The corners after a quarter turn: the top-right corner becomes the
/// top-left, and each radius changes axis.
fn turned(round: Round) -> Round {
    let [top_left, top_right, bottom_right, bottom_left] = round.map(Corner::turned);
    [top_right, bottom_right, bottom_left, top_left]
}

/// A span along the top edge, measured from the left, lies along the
/// left edge after the turn, measured up from the bottom. So a span of
/// the top or the bottom edge reverses.
fn port(port: &Port) -> Port {
    let edge = port.edge.turned();
    match port.edge {
        Edge::Top | Edge::Bottom => Port {
            edge,
            from: 100.0 - port.to,
            to: 100.0 - port.from,
        },
        Edge::Left | Edge::Right => Port { edge, ..*port },
    }
}
