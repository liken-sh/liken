//! An account's drawing.  From left to right, it shows what runs as the
//! account, the account, the roles it is bound to, what the roles
//! grant, and what follows from the grants.  Each step of a chain is a
//! box below the step before it.

use super::route::across;
use super::{Drawing, HEAD, LABEL, NONE, PLAIN, height};
use transect_map::map::section_drawing::{Point, Rect, SectionBox, Style};

const RUNS: (i32, i32) = (20, 180);
const ACCOUNT: (i32, i32) = (240, 230);
const ROLES: (i32, i32) = (520, 250);
const GRANTS: (i32, i32) = (820, 210);
const FOLLOWS: (i32, i32) = (1066, 204);
const TOP: i32 = 28;
const GAP: i32 = 20;
/// The room a labelled line takes between two steps of a chain.
const STEP: i32 = 30;

/// The boxes that [`draw`] lays out.
#[derive(Debug)]
pub struct Scene {
    /// The first column's heading: "runs as it", or "in it" for a group.
    pub runs_head: &'static str,
    pub runs: Vec<Placing>,
    pub account: SectionBox,
    pub roles: Vec<Placing>,
    pub grants: Vec<Grant>,
    pub follows: Vec<Follow>,
}

/// A box and the class it is drawn with.
#[derive(Debug)]
pub struct Placing {
    pub boxed: SectionBox,
    pub class: &'static str,
}

impl Placing {
    pub fn plain(boxed: SectionBox) -> Self {
        Self {
            boxed,
            class: PLAIN,
        }
    }

    pub fn none(boxed: SectionBox) -> Self {
        Self { boxed, class: NONE }
    }
}

/// What a role grants, with the places in the scene's roles of the
/// roles whose lines lead to it.
#[derive(Debug)]
pub struct Grant {
    pub boxed: SectionBox,
    pub roles: Vec<usize>,
}

/// Something that follows from a grant: its box, the grants whose lines
/// lead to it, and, for a step of a chain, the label on the line from
/// the box above it.
#[derive(Debug)]
pub struct Follow {
    pub placing: Placing,
    pub grants: Vec<usize>,
    pub after: Option<String>,
}

pub fn draw(scene: Scene) -> Drawing {
    let mut drawing = Drawing::new();
    let heads = [
        (RUNS.0, scene.runs_head),
        (ACCOUNT.0, "the account"),
        (ROLES.0, "bound to"),
        (GRANTS.0, "it may"),
        (FOLLOWS.0, "what follows"),
    ];
    for (x, head) in heads {
        drawing.text(x, 12, head, HEAD);
    }
    let grants = column(&mut drawing, &scene.grants, GRANTS);
    let roles = roles(&mut drawing, scene.roles, &scene.grants, &grants);
    let span = |rects: &[Rect]| match (rects.first(), rects.last()) {
        (Some(first), Some(last)) => (first.y + last.bottom()) / 2,
        _ => TOP + 40,
    };
    let middle = span(&roles);
    let h = height(&scene.account, ACCOUNT.1).max(72);
    let account = drawing.place(
        scene.account,
        "section-box section-box-workload ax-box-list",
        Rect {
            x: ACCOUNT.0,
            y: (middle - h / 2).max(TOP),
            width: ACCOUNT.1,
            height: h,
        },
    );
    let runs = stack(&mut drawing, scene.runs, RUNS, account.middle());
    for run in &runs {
        let from = Point {
            x: run.right(),
            y: run.middle(),
        };
        let to = Point {
            x: ACCOUNT.0,
            y: account.middle(),
        };
        drawing.line(&across(from, ACCOUNT.0 - 20, to), Style::Solid, true, None);
    }
    let from = Point {
        x: account.right(),
        y: account.middle(),
    };
    for role in &roles {
        let to = Point {
            x: ROLES.0,
            y: role.middle(),
        };
        drawing.line(&across(from, ROLES.0 - 25, to), Style::Solid, true, None);
    }
    for (grant, rect) in scene.grants.iter().zip(&grants) {
        for role in grant.roles.iter().filter_map(|at| roles.get(*at)) {
            let from = Point {
                x: role.right(),
                y: role.middle(),
            };
            let to = Point {
                x: GRANTS.0,
                y: rect.middle(),
            };
            drawing.line(&across(from, GRANTS.0 - 25, to), Style::Solid, true, None);
        }
    }
    follows(&mut drawing, scene.follows, &grants);
    drawing.height = drawing.bottom() + 20;
    drawing
}

/// The grants, one under the next from the top.
fn column(drawing: &mut Drawing, grants: &[Grant], (x, width): (i32, i32)) -> Vec<Rect> {
    let mut y = TOP;
    let mut rects = Vec::new();
    for grant in grants {
        let h = height(&grant.boxed, width).max(56);
        let rect = drawing.place(
            grant.boxed.clone(),
            PLAIN,
            Rect {
                x,
                y,
                width,
                height: h,
            },
        );
        rects.push(rect);
        y += h + GAP;
    }
    rects
}

/// Places each role level with the middle of the grants that its lines
/// lead to, and below the role before it.
fn roles(
    drawing: &mut Drawing,
    roles: Vec<Placing>,
    grants: &[Grant],
    placed: &[Rect],
) -> Vec<Rect> {
    let mut below = TOP;
    let mut rects = Vec::new();
    for (at, role) in roles.into_iter().enumerate() {
        let h = height(&role.boxed, ROLES.1).max(56);
        let reached: Vec<&Rect> = grants
            .iter()
            .zip(placed)
            .filter(|(grant, _)| grant.roles.contains(&at))
            .map(|(_, rect)| rect)
            .collect();
        let wanted = match (reached.first(), reached.last()) {
            (Some(first), Some(last)) => (first.y + last.bottom()) / 2 - h / 2,
            _ => below,
        };
        let rect = drawing.place(
            role.boxed,
            role.class,
            Rect {
                x: ROLES.0,
                y: wanted.max(below),
                width: ROLES.1,
                height: h,
            },
        );
        below = rect.bottom() + GAP;
        rects.push(rect);
    }
    rects
}

/// `placings` one under the next, centered on `middle`.
fn stack(
    drawing: &mut Drawing,
    placings: Vec<Placing>,
    (x, width): (i32, i32),
    middle: i32,
) -> Vec<Rect> {
    let heights: Vec<i32> = placings
        .iter()
        .map(|p| height(&p.boxed, width).max(56))
        .collect();
    let total: i32 = heights.iter().sum::<i32>() + GAP * (heights.len() as i32 - 1).max(0);
    let mut y = (middle - total / 2).max(TOP);
    let mut rects = Vec::new();
    for (placing, h) in placings.into_iter().zip(heights) {
        let rect = drawing.place(
            placing.boxed,
            placing.class,
            Rect {
                x,
                y,
                width,
                height: h,
            },
        );
        rects.push(rect);
        y += h + GAP;
    }
    rects
}

/// Places what follows.  Each box is level with the first grant whose
/// line leads to it, or, for a step of a chain, directly below the step
/// before it, with a labelled line between them.
fn follows(drawing: &mut Drawing, follows: Vec<Follow>, grants: &[Rect]) {
    let mut below = TOP;
    let mut last: Option<Rect> = None;
    for follow in follows {
        let h = height(&follow.placing.boxed, FOLLOWS.1).max(56);
        let wanted = match (&follow.after, follow.grants.first()) {
            (Some(_), _) => below - GAP + STEP,
            (None, Some(grant)) => grants.get(*grant).map_or(below, |rect| rect.y),
            (None, None) => below,
        };
        let top = match follow.after {
            Some(_) => wanted,
            None => wanted.max(below),
        };
        let rect = drawing.place(
            follow.placing.boxed,
            follow.placing.class,
            Rect {
                x: FOLLOWS.0,
                y: top,
                width: FOLLOWS.1,
                height: h,
            },
        );
        if let (Some(label), Some(above)) = (&follow.after, last) {
            let x = FOLLOWS.0 + 40;
            let points = [
                Point {
                    x,
                    y: above.bottom(),
                },
                Point { x, y: rect.y },
            ];
            drawing.line(&points, Style::Solid, true, None);
            drawing.text(x + 12, (above.bottom() + rect.y) / 2 + 4, label, LABEL);
        }
        for grant in follow.grants.iter().filter_map(|at| grants.get(*at)) {
            let from = Point {
                x: grant.right(),
                y: grant.middle(),
            };
            let to = Point {
                x: FOLLOWS.0,
                y: (rect.y + 28).min(rect.middle()),
            };
            drawing.line(&across(from, FOLLOWS.0 - 18, to), Style::Solid, true, None);
        }
        below = rect.bottom() + GAP;
        last = Some(rect);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use transect_map::map::section_drawing::Look;

    fn boxed(title: &str) -> SectionBox {
        SectionBox::new(title.into(), vec!["a line".into()], Look::Plain)
    }

    fn scene() -> Scene {
        Scene {
            runs_head: "runs as it",
            runs: vec![Placing::plain(boxed("Deployment console"))],
            account: boxed("ServiceAccount ops/console"),
            roles: vec![
                Placing::plain(boxed("ClusterRole operate")),
                Placing::plain(boxed("ClusterRole view")),
            ],
            grants: vec![
                Grant {
                    boxed: boxed("run pods"),
                    roles: vec![0],
                },
                Grant {
                    boxed: boxed("change what a GitOps tool applies"),
                    roles: vec![0],
                },
                Grant {
                    boxed: boxed("read"),
                    roles: vec![0, 1],
                },
            ],
            follows: vec![
                Follow {
                    placing: Placing::plain(boxed("read Secrets")),
                    grants: vec![0],
                    after: None,
                },
                Follow {
                    placing: Placing::plain(boxed("ServiceAccount ops/applier")),
                    grants: vec![1],
                    after: None,
                },
                Follow {
                    placing: Placing::plain(boxed("ClusterRole cluster-admin")),
                    grants: Vec::new(),
                    after: Some("bound to".into()),
                },
            ],
        }
    }

    #[test]
    fn its_boxes_stand_apart() {
        assert!(draw(scene()).boxes_apart());
    }

    /// Two roles, a grant that both lead to, and a chain of two steps.
    #[test]
    fn no_two_of_its_lines_cross() {
        assert_eq!(draw(scene()).crossings(), vec![]);
    }

    #[test]
    fn its_lines_end_on_edges() {
        assert!(draw(scene()).ends_on_edges());
    }

    #[test]
    fn a_role_sits_level_with_what_it_grants() {
        let drawing = draw(scene());
        let rect = |title: &str| {
            let placed = drawing.boxes.iter().find(|b| b.boxed.title == title);
            placed.unwrap().rect
        };
        let (operate, view) = (rect("ClusterRole operate"), rect("ClusterRole view"));
        assert!(operate.y < view.y);
        assert!(operate.bottom() <= view.y);
    }

    #[test]
    fn a_step_of_a_chain_sits_below_the_one_before_it() {
        let drawing = draw(scene());
        let rect = |title: &str| {
            let placed = drawing.boxes.iter().find(|b| b.boxed.title == title);
            placed.unwrap().rect
        };
        let (applier, role) = (
            rect("ServiceAccount ops/applier"),
            rect("ClusterRole cluster-admin"),
        );
        assert_eq!(role.y, applier.bottom() + STEP);
    }
}
