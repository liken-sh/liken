//! Reads one `Shape` document and checks it against the grammar of
//! plan 17. A document with a field the form does not have, such as a
//! color, or a value outside its range is refused with the first fault,
//! so a definition that loads draws the same way as the library's.

use std::collections::BTreeMap;

use serde::Deserialize;
use serde_yaml::Value;

use super::length::{self, MAX_NUMBER, number, percent};
use super::place::{self, Edge, Place};
use super::{
    Align, Art, Clear, Display, Fit, Material, Origin, Outline, Path, Piece, Port, Shape, Size,
    Text,
};
use crate::resources::DocId;

/// A shape has at most this many pieces.
const MAX_PIECES: usize = 12;

/// How far below its bottom edge a piece may reach, in pixels at the
/// shape's widest width. The gap under a row of the sketch has room for
/// it.
const MAX_BELOW: f64 = 20.0;

const MAX_PATH: usize = 1000;

#[derive(Deserialize)]
struct Document {
    metadata: Metadata,
    spec: Spec,
}

#[derive(Deserialize)]
struct Metadata {
    name: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Spec {
    summary: String,
    size: WrittenSize,
    axis: Option<Value>,
    #[serde(default)]
    outline: WrittenOutline,
    #[serde(default)]
    text: WrittenText,
    display: Option<Display>,
    #[serde(default)]
    pieces: Vec<WrittenPiece>,
    #[serde(default)]
    joins: Vec<Edge>,
    #[serde(default)]
    ports: BTreeMap<Edge, Vec<Value>>,
    #[serde(default)]
    turns: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WrittenSize {
    design: Option<Vec<Value>>,
    width: Vec<Value>,
    share: Option<Value>,
    follow: Option<Follow>,
}

/// What a part's width follows, when it has no design size.
#[derive(Deserialize)]
#[serde(rename_all = "lowercase")]
enum Follow {
    Text,
}

#[derive(Default, Deserialize)]
#[serde(deny_unknown_fields)]
struct WrittenOutline {
    draw: Option<Material>,
    round: Option<Value>,
    at: Option<place::Written>,
}

#[derive(Default, Deserialize)]
#[serde(deny_unknown_fields)]
struct WrittenText {
    inset: Option<Vec<Value>>,
    align: Option<Align>,
    fit: Option<Fit>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WrittenPiece {
    name: String,
    draw: Option<Material>,
    at: place::Written,
    round: Option<Value>,
    #[serde(default)]
    behind: bool,
    view: Option<Vec<Value>>,
    path: Option<String>,
    clear: Option<Edge>,
}

/// Reads an author's `Shape` document, whose name is `<shape>.<group>`.
pub fn parse(doc: DocId, value: Value) -> Result<Shape, String> {
    read(value, Origin::Document(doc))
}

/// Reads a `Shape` document from `origin`. The library's shapes have
/// bare names, and an author's have the author's group.
pub fn read(value: Value, origin: Origin) -> Result<Shape, String> {
    let document: Document = serde_yaml::from_value(value).map_err(|error| error.to_string())?;
    let name = document.metadata.name;
    let named = match origin {
        Origin::Library => bare(&name),
        Origin::Document(_) => grouped(&name),
    };
    if !named {
        return Err(match origin {
            Origin::Library => format!("the library's shape {name:?} is not a bare name"),
            Origin::Document(_) => format!(
                "the name {name:?} is not <shape>.<group>, a lower-case letter followed by lower-case letters, digits, and hyphens, then a dot and a group that the author owns. Only the library's shapes have bare names"
            ),
        });
    }
    check(name, origin, document.spec)
}

fn check(name: String, origin: Origin, spec: Spec) -> Result<Shape, String> {
    let summary = plain(spec.summary, "summary", 200)?;
    let size = size(&spec.size)?;
    let follows = size.design.is_none();
    // A part that follows its text has no proportions: its body is as
    // tall as its text. So it cannot cut its text, which would leave it
    // no height, take a share of the sketch, or lie on its side.
    let proportioned = !spec.pieces.is_empty()
        || spec.axis.is_some()
        || spec.size.share.is_some()
        || spec.text.fit == Some(Fit::Cut)
        || spec.turns;
    if follows && proportioned {
        return Err(
            "a shape whose width follows its text has no size.design, axis, share, or pieces, does not cut its text, and does not turn"
                .into(),
        );
    }
    // A turned shape's axis would be a vertical line, which no row
    // aligns on, so a shape that turns keeps the default axis.
    if spec.turns && spec.axis.is_some() {
        return Err("a shape that turns states no axis".into());
    }
    let [width, height] = size.design.unwrap_or([size.width[1], 0.0]);
    let axis = match (&spec.axis, follows) {
        (_, true) => None,
        (None, false) => Some(height / 2.0),
        (Some(axis), false) => Some(number(axis, "axis", 0.0, height)?),
    };
    let bound = Bound {
        width,
        height,
        below: MAX_BELOW * width / size.width[1],
    };
    let outline = Outline {
        draw: spec.outline.draw.unwrap_or_default(),
        round: length::round(spec.outline.round.as_ref(), "outline.round")?,
        at: match spec.outline.at {
            None => Place::whole(),
            Some(at) => bound.check(Place::read(at, "outline.at")?, "outline.at")?,
        },
    };
    let text = text(spec.text, width.max(height))?;
    if spec.pieces.len() > MAX_PIECES {
        return Err(format!("pieces has more than {MAX_PIECES} pieces"));
    }
    let pieces = spec.pieces.into_iter().enumerate();
    let pieces = pieces.map(|(at, written)| piece(written, &format!("pieces[{at}]"), &bound));
    let pieces = pieces.collect::<Result<Vec<_>, String>>()?;
    let mut joins = spec.joins;
    joins.sort();
    if joins.windows(2).any(|pair| pair[0] == pair[1]) {
        return Err("joins names an edge more than once".into());
    }
    let ports = spec.ports.into_iter().map(|(edge, span)| port(edge, &span));
    let ports = ports.collect::<Result<Vec<_>, String>>()?;
    Ok(Shape {
        name,
        origin,
        summary,
        size,
        axis,
        outline,
        text,
        display: spec.display,
        pieces,
        joins,
        ports,
        turns: spec.turns,
    })
}

fn size(written: &WrittenSize) -> Result<Size, String> {
    let width = pair(Some(&written.width), "size.width", 16.0, 2000.0)?;
    if width[0] > width[1] {
        return Err("size.width is [narrowest, widest], and the first is greater".into());
    }
    let design = match (&written.follow, &written.design) {
        (Some(Follow::Text), None) => None,
        (Some(Follow::Text), Some(_)) => {
            return Err("a shape whose width follows its text has no size.design".into());
        }
        (None, design) => Some(pair(design.as_ref(), "size.design", 1.0, MAX_NUMBER)?),
    };
    let share = written.share.as_ref();
    let share = share.map(|share| percent(share, "size.share", 1.0, 100.0));
    Ok(Size {
        design,
        width,
        share: share.transpose()?,
    })
}

fn text(written: WrittenText, larger: f64) -> Result<Text, String> {
    let inset = match &written.inset {
        None => [12.0, 14.0, 12.0, 14.0],
        Some(inset) => {
            let read = inset
                .iter()
                .map(|side| number(side, "text.inset", 0.0, larger));
            let read = read.collect::<Result<Vec<f64>, String>>()?;
            read.try_into()
                .map_err(|_| "text.inset is [top, right, bottom, left]".to_string())?
        }
    };
    Ok(Text {
        inset,
        align: written.align.unwrap_or_default(),
        fit: written.fit.unwrap_or_default(),
    })
}

fn piece(written: WrittenPiece, what: &str, bound: &Bound) -> Result<Piece, String> {
    let name = plain(written.name, &format!("{what}.name"), 40)?;
    let at = bound.check(Place::read(written.at, &format!("{what}.at"))?, what)?;
    let art = match (written.view, written.path) {
        (None, None) => None,
        (Some(view), Some(path)) => Some(Art {
            view: pair(Some(&view), &format!("{what}.view"), 0.001, MAX_NUMBER)?,
            path: self::path(path, &format!("{what}.path"))?,
            turned: false,
        }),
        _ => {
            return Err(format!(
                "{what} has a view and no path, or a path and no view"
            ));
        }
    };
    let clear = written
        .clear
        .map(|edge| clear(edge, &at, what))
        .transpose()?;
    Ok(Piece {
        name,
        draw: written.draw.unwrap_or_default(),
        at,
        round: length::round(written.round.as_ref(), &format!("{what}.round"))?,
        behind: written.behind,
        art,
        clear,
    })
}

/// The reach of a piece that the text keeps clear of on `edge`. The
/// text region's edge moves by a fixed number of units, so the piece's
/// offset from that edge and its size across it must be plain numbers.
fn clear(edge: Edge, at: &Place, what: &str) -> Result<Clear, String> {
    let near = matches!(edge, Edge::Top | Edge::Left);
    let found = at.crossing(edge).offset_and_size(near);
    let (offset, size) = found.ok_or_else(|| {
        format!(
            "{what} keeps the text clear of its {} edge, and does not state its distance from that edge and its size as numbers of design units",
            edge.word()
        )
    })?;
    Ok(Clear {
        edge,
        reach: offset + size,
    })
}

fn port(edge: Edge, span: &[Value]) -> Result<Port, String> {
    let what = format!("ports.{}", edge.word());
    let [from, to] = <[&Value; 2]>::try_from(span.iter().collect::<Vec<_>>())
        .map_err(|_| format!("{what} is [from, to]"))?;
    let (from, to) = (
        percent(from, &what, 0.0, 100.0)?,
        percent(to, &what, 0.0, 100.0)?,
    );
    if from > to {
        return Err(format!("{what} is [from, to], and the first is greater"));
    }
    Ok(Port { edge, from, to })
}

/// How far a piece may lie outside its part, at the design size.
struct Bound {
    width: f64,
    height: f64,
    /// The design units of `MAX_BELOW` at the widest width.
    below: f64,
}

impl Bound {
    fn check(&self, at: Place, what: &str) -> Result<Place, String> {
        let rect = at.rect(self.width, self.height);
        let inside = rect.left >= -self.width
            && rect.right <= 2.0 * self.width
            && rect.top >= -self.height
            && rect.bottom <= self.height + self.below;
        match inside {
            true => Ok(at),
            false => Err(format!(
                "{what} lies further outside the part than one design width to a side, one design height above, or {MAX_BELOW} pixels below"
            )),
        }
    }
}

fn pair(value: Option<&Vec<Value>>, what: &str, low: f64, high: f64) -> Result<[f64; 2], String> {
    let fault = || format!("{what} is a list of two numbers from {low} to {high}");
    let value = value.ok_or_else(fault)?;
    let read = value.iter().map(|n| number(n, what, low, high));
    let read = read.collect::<Result<Vec<f64>, String>>()?;
    read.try_into().map_err(|_| fault())
}

fn plain(text: String, what: &str, cap: usize) -> Result<String, String> {
    let count = text.chars().count();
    let control = text.chars().any(char::is_control);
    match (1..=cap).contains(&count) && !control && !text.trim().is_empty() {
        true => Ok(text),
        false => Err(format!(
            "{what} is not plain text of 1 to {cap} characters with no control characters"
        )),
    }
}

fn path(text: String, what: &str) -> Result<Path, String> {
    let allowed = |c: char| "MLHVCQAZmlhvcqaz0123456789.-, ".contains(c);
    let starts = text.starts_with(['M', 'm']);
    match starts && text.len() <= MAX_PATH && text.chars().all(allowed) {
        true => Ok(Path(text)),
        false => Err(format!(
            "{what} is not path data: it starts with M, and has only the commands M, L, H, V, C, Q, A, and Z, digits, points, minus signs, commas, and spaces, in at most {MAX_PATH} characters"
        )),
    }
}

/// Whether `name` is a library shape's name: a lower-case letter, then
/// up to 39 lower-case letters, digits, and hyphens.
fn bare(name: &str) -> bool {
    let mut chars = name.chars();
    let first = chars.next().is_some_and(|c| c.is_ascii_lowercase());
    let rest = chars.all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-');
    first && rest && name.len() <= 40
}

/// Whether `name` is an author's shape's name, `<shape>.<group>`, where
/// the group is a DNS name of two labels or more, as an API group is.
fn grouped(name: &str) -> bool {
    let Some((shape, group)) = name.split_once('.') else {
        return false;
    };
    let label = |label: &str| {
        let edges = label.starts_with(|c: char| c.is_ascii_alphanumeric())
            && label.ends_with(|c: char| c.is_ascii_alphanumeric());
        let inner = label
            .chars()
            .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-');
        edges && inner && label.len() <= 63
    };
    let labels: Vec<&str> = group.split('.').collect();
    bare(shape) && labels.len() >= 2 && labels.iter().all(|l| label(l)) && group.len() <= 253
}
