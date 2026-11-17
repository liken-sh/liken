//! The right side of a workload's drawing.  Solid lines lead to what
//! the workload runs as and holds, and dashed lines lead to what it may
//! call.

use super::reach::{GAP, RIGHT_W, RIGHT_X, TOP};
use super::route::across;
use super::{Drawing, Gate, HEAD, LABEL, PLAIN, WIDTH, height};
use transect_map::map::section_drawing::{Look, Point, Rect, SectionBox, Style};

/// The right side: what it runs as, what it holds, and what it may call.
#[derive(Debug)]
pub struct Right {
    pub runs_as: SectionBox,
    pub holds: Vec<SectionBox>,
    pub calls: Vec<SectionBox>,
    pub out: Out,
}

/// What limits its calls out.
#[derive(Debug)]
pub enum Out {
    /// No policy limits them, so the drawing shows any address as an
    /// open-ended line.
    Any(Gate),
    /// The policies that limit them, each as a box, and their gate.
    Limited(Vec<SectionBox>, Gate),
}

/// Draws the right side.  Solid lines lead to what the workload runs as
/// and holds, and dashed lines lead to what it may call.  When no
/// policy limits its calls out, an open-ended line leads to any
/// address.
pub fn right(drawing: &mut Drawing, right: Right, workload: Rect) {
    let mut y = TOP;
    drawing.text(RIGHT_X, 12, "runs as", HEAD);
    let mut solid: Vec<i32> = Vec::new();
    let h = height(&right.runs_as, RIGHT_W);
    let rect = drawing.place(
        right.runs_as,
        PLAIN,
        Rect {
            x: RIGHT_X,
            y,
            width: RIGHT_W,
            height: h,
        },
    );
    solid.push(rect.middle());
    y += h + 22;
    if !right.holds.is_empty() {
        drawing.text(RIGHT_X, y - 8, "holds", HEAD);
        y += 8;
        for boxed in right.holds {
            let h = height(&boxed, RIGHT_W);
            let class = match boxed.look {
                Look::Machine => "section-box section-box-machine ax-box-list",
                _ => PLAIN,
            };
            let rect = drawing.place(
                boxed,
                class,
                Rect {
                    x: RIGHT_X,
                    y,
                    width: RIGHT_W,
                    height: h,
                },
            );
            solid.push(rect.middle());
            y += h + GAP;
        }
        y += 14;
    }
    let source = Point {
        x: workload.right(),
        y: workload.y + 30,
    };
    for at in solid {
        let points = across(source, RIGHT_X - 24, Point { x: RIGHT_X, y: at });
        drawing.line(&points, Style::Solid, true, None);
    }
    let calls = Point {
        x: workload.right(),
        y: (workload.y + 60).min(workload.bottom() - 12),
    };
    // The boxes of what it may call start no higher than the line that
    // leaves the workload for them, so that line runs down its lane and
    // crosses none of the lines up to what it runs as and holds.
    y = y.max(calls.y);
    drawing.text(RIGHT_X, y - 8, "may call", HEAD);
    y += 8;
    let lane = RIGHT_X - 40;
    for boxed in right.calls {
        let h = height(&boxed, RIGHT_W);
        let rect = drawing.place(
            boxed,
            PLAIN,
            Rect {
                x: RIGHT_X,
                y,
                width: RIGHT_W,
                height: h,
            },
        );
        drawing.line(
            &across(
                calls,
                lane,
                Point {
                    x: RIGHT_X,
                    y: rect.middle(),
                },
            ),
            Style::Dashed,
            true,
            None,
        );
        y += h + GAP;
    }
    match right.out {
        Out::Any(gate) => {
            let at = y + 20;
            drawing.text(RIGHT_X, at - 12, "any address", LABEL);
            let points = [
                calls,
                Point {
                    x: lane,
                    y: calls.y,
                },
                Point { x: lane, y: at },
                Point {
                    x: WIDTH - 18,
                    y: at,
                },
            ];
            drawing.line(&points, Style::Dashed, false, Some(gate.title.clone()));
            drawing.dot(lane, (calls.y + at) / 2, &gate);
        }
        Out::Limited(boxes, gate) => {
            let mut first = None;
            for boxed in boxes {
                let h = height(&boxed, RIGHT_W);
                let rect = drawing.place(
                    boxed,
                    PLAIN,
                    Rect {
                        x: RIGHT_X,
                        y,
                        width: RIGHT_W,
                        height: h,
                    },
                );
                drawing.line(
                    &across(
                        calls,
                        lane,
                        Point {
                            x: RIGHT_X,
                            y: rect.middle(),
                        },
                    ),
                    Style::Dashed,
                    true,
                    None,
                );
                first.get_or_insert(rect.middle());
                y += h + GAP;
            }
            if let Some(first) = first {
                drawing.dot(lane, (calls.y + first) / 2, &gate);
            }
        }
    }
}
