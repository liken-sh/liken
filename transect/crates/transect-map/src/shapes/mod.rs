//! Draws a part of a sketch in its shape. Every shape is drawn by this
//! module and `part.html`, and every state by `shapes.css`, so nothing
//! here or there names a shape: a shape states numbers and materials,
//! and the drawing turns them into a part.
//!
//! A part is a CSS size container. Its body has the shape's
//! proportions, its outline and pieces are placed absolutely in units of
//! its width, and its text is one block in the text region. The classes
//! of a part say its fit, its alignment, and its display, and the
//! sketch adds the class of its state.

mod css;
pub mod specimen;
pub mod text;

use transect_resources::shapes::{Art, Edge, Piece, Place, Port, Shape};

pub use specimen::ShapesPage;

/// How far up and to the right each copy of a stack's body stands, in
/// design units.
const STACK_STEP: f64 = 8.0;

/// The most copies of its outline that a part that stands for a stack
/// draws behind it. A larger stack draws as many, and its label counts
/// its parts.
pub const STACK_COPIES: u8 = 2;

/// The drawings of a stack of `shape`, with each number of copies from
/// one to `STACK_COPIES`, in `pose` otherwise.
pub fn stacked(shape: &Shape, pose: Pose) -> Vec<Drawing> {
    let copies = 1..=STACK_COPIES;
    copies
        .map(|stack| Drawing::of(shape, Pose { stack, ..pose }))
        .collect()
}

/// A part's drawing, as the values that `part.html` writes.
#[derive(Clone, Debug, PartialEq)]
pub struct Drawing {
    /// The part's classes, each a fixed word.
    pub class: String,
    /// The part's custom properties, which `shapes.css` reads, so the
    /// stylesheet can override a size.
    pub style: String,
    /// The text region's insets, as custom properties.
    pub text: String,
    /// Whether the part has an axis, the line on which a row of parts
    /// aligns.
    pub axis: bool,
    /// The copies of a stack, the body, and the pieces, in the order
    /// that they are drawn.
    pub layers: Vec<Layer>,
    /// The mark of each port's span, when the drawing marks them.
    pub ports: Vec<Layer>,
}

/// The body, a copy of it, a piece, or the mark of a port.
#[derive(Clone, Debug, PartialEq)]
pub struct Layer {
    pub class: String,
    pub style: String,
    pub art: Option<Path>,
}

/// A piece drawn as an SVG path.
#[derive(Clone, Debug, PartialEq)]
pub struct Path {
    /// The value of `viewBox`.
    pub view: String,
    /// The path data, which the parser checked.
    pub data: String,
    /// The value of the path's `transform`, for a piece of a shape on
    /// its side.
    pub transform: Option<String>,
}

/// How a part of a shape is drawn beyond its shape.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Pose {
    /// How many copies of the body stand behind the part.
    pub stack: u8,
    /// Whether the drawing marks the span of each port.
    pub ports: bool,
}

impl Drawing {
    /// The drawing of a part of `shape`. A shape that lies on its side
    /// is drawn from its `turned` definition.
    pub fn of(shape: &Shape, pose: Pose) -> Self {
        let mut class = vec![
            "shape-part".to_string(),
            format!("shape-fit-{}", shape.text.fit.word()),
            format!("shape-align-{}", shape.text.align.word()),
        ];
        if let Some(display) = shape.display {
            class.push(format!("shape-display-{}", display.word()));
        }
        if shape.size.share.is_some() {
            class.push("shape-share".into());
        }
        if shape.size.design.is_none() {
            class.push("shape-follow".into());
        }
        Self {
            class: class.join(" "),
            style: style(shape, pose.stack),
            text: text(shape),
            axis: shape.axis.is_some(),
            layers: layers(shape, pose.stack),
            ports: match pose.ports {
                true => shape.ports.iter().map(|p| port(p, pose.stack)).collect(),
                false => Vec::new(),
            },
        }
    }
}

/// The part's custom properties: the size of a design unit, the
/// proportions of its body, its narrowest and widest widths, the room
/// above it, and how far its axis is below the middle of its design.
/// A part that grows taller for its text grows equally above and below
/// its axis, so `shapes.css` places the axis from the middle. The copies
/// of a stack rise and move right of the body, so a stack keeps that
/// room above it and at its right, inside its area, where no neighbor
/// covers them.
fn style(shape: &Shape, stack: u8) -> String {
    let [narrowest, widest] = shape.size.width;
    let top = match shape.size.share {
        Some(share) => format!("min({share}cqi, {widest}px)"),
        None => format!("{widest}px"),
    };
    let mut style = vec![format!("--w-min: {narrowest}px"), format!("--w-top: {top}")];
    match shape.size.design {
        Some([width, height]) => {
            style.push(format!("--u: calc(100cqi / {width})"));
            style.push(format!("--ratio: {width} / {height}"));
        }
        None => style.push("--u: 1px".into()),
    }
    let lift = STACK_STEP * f64::from(stack);
    let above = shape.above() + lift;
    if above > 0.0 {
        style.push(format!("--room-above: {above}"));
    }
    if lift > 0.0 {
        style.push(format!("--stack-room: {lift}"));
    }
    if let (Some(axis), Some([_, height])) = (shape.axis, shape.size.design) {
        style.push(format!("--axis-shift: {}", axis - height / 2.0));
    }
    style.join("; ")
}

/// The text region's insets, in design units, which `shapes.css` turns
/// into its padding: top, right, bottom, and left, each moved clear of
/// a piece that the text keeps clear of. `--source-bottom` is the
/// bottom inset that the shape states. The line that names the field a
/// part was read from stands there, under such a piece, as a speaker's
/// source line stands under its cone.
fn text(shape: &Shape) -> String {
    let [top, right, bottom, left] = shape.inset();
    let stated = shape.text.inset[Edge::Bottom.index()];
    format!(
        "--text-top: {top}; --text-right: {right}; --text-bottom: {bottom}; --text-left: {left}; --source-bottom: {stated}"
    )
}

fn layers(shape: &Shape, stack: u8) -> Vec<Layer> {
    let outline = &shape.outline;
    let body = |class: &str, place: &Place| Layer {
        class: format!("{class} shape-draw-{}", outline.draw.word()),
        style: format!("{}; {}", css::place(place), css::round(&outline.round)),
        art: None,
    };
    let copies = (1..=stack).rev().map(|n| {
        let lifted = css::lifted(&outline.at, STACK_STEP * f64::from(n));
        body("shape-outline shape-copy", &lifted)
    });
    let mut layers: Vec<Layer> = copies.collect();
    layers.push(body("shape-outline", &outline.at));
    let [width, height] = shape.size.design.unwrap_or_default();
    let body = outline.at.rect(width, height);
    layers.extend(shape.pieces.iter().map(|one| {
        let at = one.at.rect(width, height);
        let inside = at.left >= body.left
            && at.right <= body.right
            && at.top >= body.top
            && at.bottom <= body.bottom;
        let mut layer = piece(one);
        if !inside {
            layer.class.push_str(" shape-outside");
        }
        layer
    }));
    layers
}

fn piece(piece: &Piece) -> Layer {
    let behind = if piece.behind { " shape-behind" } else { "" };
    let class = format!("shape-piece shape-draw-{}{behind}", piece.draw.word());
    let place = css::place(&piece.at);
    match &piece.art {
        None => Layer {
            class,
            style: format!("{place}; {}", css::round(&piece.round)),
            art: None,
        },
        Some(art) => Layer {
            class,
            style: place,
            art: Some(path(art)),
        },
    }
}

/// A piece's path. On a shape that lies on its side, the path turns a
/// quarter turn inside its view, as the place of the piece did.
fn path(art: &Art) -> Path {
    let [width, height] = art.view;
    Path {
        view: format!("0 0 {width} {height}"),
        data: art.path.as_str().to_string(),
        transform: art.turned.then(|| format!("matrix(0 -1 1 0 0 {height})")),
    }
}

/// The mark of a port's span, for the page that shows each shape. The
/// copies of a stack stand above and to the right of the part, so a
/// port on the top or the right edge moves out to the last copy.
fn port(port: &Port, stack: u8) -> Layer {
    let lift = STACK_STEP * f64::from(stack);
    let (from, size) = (port.from, port.to - port.from);
    let style = match port.edge {
        Edge::Left => format!("left: 0; top: {from}%; height: {size}%"),
        Edge::Right => format!(
            "left: calc(100% + var(--u) * {lift}); top: calc({from}% - var(--u) * {lift}); height: {size}%"
        ),
        Edge::Top => format!(
            "top: calc(var(--u) * -{lift}); left: calc({from}% + var(--u) * {lift}); width: {size}%"
        ),
        Edge::Bottom => format!("top: 100%; left: {from}%; width: {size}%"),
    };
    Layer {
        class: format!("shape-port shape-port-{}", port.edge.word()),
        style,
        art: None,
    }
}

#[cfg(test)]
mod tests;
