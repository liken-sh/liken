//! The drawing of what may reach a workload and what the workload may
//! reach.  On the left are the ways in from outside the cluster and the
//! callers inside it.  Each of their lines ends at a port on the
//! workload's left edge, with a dot on the line for each gate.  The
//! right side shows what the workload runs as, what it holds, and what
//! it may call.

use super::right::right;
pub use super::right::{Out, Right};
use super::route::across;
use super::sockets::{Arrival, arrive, ports};
use super::{Drawing, Gate, HEAD, LABEL, NONE, PLAIN, VIA, height};
use transect_map::map::section_drawing::{Point, Rect, SectionBox, Style};

const LEFT_X: i32 = 20;
const LEFT_W: i32 = 250;
const TLS_X: i32 = 298;
const SIGN_X: i32 = 340;
const CONTROLLER_X: i32 = 376;
const CONTROLLER_W: i32 = 130;
const SERVICE_X: i32 = 532;
const SERVICE_W: i32 = 160;
/// The x at which the lines from the callers of one rule join.
const JOIN_X: i32 = 600;
const POLICY_X: i32 = 722;
pub(super) const SOCKET_X: i32 = 776;
pub(super) const SOCKET_W: i32 = 60;
pub(super) const SOCKET_H: i32 = 24;
pub(super) const WORKLOAD_X: i32 = 806;
pub(super) const WORKLOAD_W: i32 = 200;
pub(super) const RIGHT_X: i32 = 1062;
pub(super) const RIGHT_W: i32 = 208;
pub(super) const TOP: i32 = 28;
pub(super) const GAP: i32 = 8;
/// The smallest vertical distance between two ports on the workload's
/// edge.
pub(super) const SOCKET_GAP: i32 = 34;

#[derive(Debug)]
pub struct Reach {
    /// The ways in, with those through the ingress controller first.
    /// When there are none, the drawing shows a dashed "no way in" box.
    pub ways: Vec<WayBox>,
    /// The ingress controller the hosts enter through.
    pub controller: Option<SectionBox>,
    pub services: Vec<ServiceBox>,
    pub inside: Inside,
    pub ports: Vec<Port>,
    pub workload: SectionBox,
    pub right: Right,
}

#[derive(Debug)]
pub struct WayBox {
    pub boxed: SectionBox,
    pub tls: Gate,
    pub sign_in: Gate,
    pub path: Path,
}

/// Where a way's line goes after its gates.
#[derive(Debug)]
pub enum Path {
    /// Into the ingress controller, which sends it on to its Services.
    Controller,
    /// Into one of the drawing's Services.
    Service(usize),
    /// Straight to the workload's ports, with no ingress controller or
    /// Service between, as for a host port.  The line has its own policy
    /// dot and label.  With no ports, the line ends on the workload's
    /// edge.
    Ports {
        ports: Vec<usize>,
        policy: Gate,
        label: String,
    },
}

/// A Service on a way's path.  With no ports, its line ends on the
/// workload's edge.
#[derive(Debug)]
pub struct ServiceBox {
    pub boxed: SectionBox,
    pub ports: Vec<usize>,
    pub policy: Gate,
    /// Whether the ingress controller sends to it.
    pub from_controller: bool,
}

/// Who may call the workload from inside the cluster.
#[derive(Debug)]
pub enum Inside {
    /// One box with no ports: "any pod" when no NetworkPolicy limits
    /// the callers, or "no pod" when a policy admits none.
    Any {
        boxed: Box<SectionBox>,
        label: String,
        gate: Gate,
    },
    /// The callers each rule admits, a group per rule.
    Admitted(Vec<Group>),
}

/// The callers that one rule of a NetworkPolicy admits.  An empty
/// `ports` means the rule admits any port.
#[derive(Debug)]
pub struct Group {
    pub callers: Vec<SectionBox>,
    pub policy: String,
    /// The policy's page.
    pub href: Option<String>,
    pub gate: Gate,
    pub ports: Vec<usize>,
}

/// A port the workload serves.
#[derive(Debug)]
pub struct Port {
    pub text: String,
    pub title: String,
}

pub fn draw(reach: Reach) -> Drawing {
    let mut drawing = Drawing::new();
    drawing.text(LEFT_X, 12, "from outside the cluster", HEAD);
    drawing.text(POLICY_X - 44, 12, "NetworkPolicy", HEAD);
    let mut arrivals: Vec<Arrival> = Vec::new();
    let mut y = TOP;
    if reach.ways.is_empty() {
        let none = SectionBox::new(
            "no way in".into(),
            vec!["No Ingress, LoadBalancer, NodePort, or host port reaches it.".into()],
            transect_map::map::section_drawing::Look::Plain,
        );
        let h = height(&none, LEFT_W);
        drawing.place(
            none,
            NONE,
            Rect {
                x: LEFT_X,
                y,
                width: LEFT_W,
                height: h,
            },
        );
        y += h + GAP;
    } else {
        drawing.text(TLS_X - 10, 12, "TLS", HEAD);
        drawing.text(SIGN_X - 14, 12, "sign-in", HEAD);
    }
    let outside = outside_ports(&reach);
    let mut controlled: Vec<i32> = Vec::new();
    let mut to_services: Vec<(Point, usize)> = Vec::new();
    for way in reach.ways {
        let h = height(&way.boxed, LEFT_W);
        let rect = drawing.place(
            way.boxed,
            PLAIN,
            Rect {
                x: LEFT_X,
                y,
                width: LEFT_W,
                height: h,
            },
        );
        let middle = rect.middle();
        drawing.dot(TLS_X, middle, &way.tls);
        drawing.dot(SIGN_X, middle, &way.sign_in);
        let from = Point {
            x: rect.right(),
            y: middle,
        };
        match way.path {
            Path::Controller => controlled.push(middle),
            Path::Service(index) => to_services.push((from, index)),
            Path::Ports {
                ports,
                policy,
                label,
            } => {
                drawing.text(CONTROLLER_X, middle - 12, &label, LABEL);
                drawing.dot(POLICY_X, middle, &policy);
                arrive(&mut arrivals, from, &ports, Style::Solid);
            }
        }
        y += h + GAP;
    }
    let controller = match (reach.controller, controlled.as_slice()) {
        (Some(boxed), [first, .., last]) | (Some(boxed), [first @ last]) => {
            let top = first - 32;
            let bottom = (last + 32).max(top + height(&boxed, CONTROLLER_W));
            for &at in &controlled {
                drawing.line(
                    &[
                        Point {
                            x: LEFT_X + LEFT_W,
                            y: at,
                        },
                        Point {
                            x: CONTROLLER_X,
                            y: at,
                        },
                    ],
                    Style::Solid,
                    true,
                    None,
                );
            }
            let rect = Rect {
                x: CONTROLLER_X,
                y: top,
                width: CONTROLLER_W,
                height: bottom - top,
            };
            Some(drawing.place(boxed, PLAIN, rect))
        }
        _ => None,
    };
    let mut services_bottom = TOP;
    for (index, service) in reach.services.into_iter().enumerate() {
        let wanted = match (service.from_controller, controller) {
            (true, Some(rect)) => rect.middle(),
            _ => to_services
                .iter()
                .find(|(_, at)| *at == index)
                .map_or(services_bottom, |(from, _)| from.y),
        };
        let h = height(&service.boxed, SERVICE_W);
        let top = (wanted - h / 2).max(services_bottom);
        let rect = drawing.place(
            service.boxed,
            PLAIN,
            Rect {
                x: SERVICE_X,
                y: top,
                width: SERVICE_W,
                height: h,
            },
        );
        services_bottom = rect.bottom() + GAP;
        let into = Point {
            x: SERVICE_X,
            y: rect.middle(),
        };
        if let (true, Some(controller)) = (service.from_controller, controller) {
            let from = Point {
                x: controller.right(),
                y: controller.middle(),
            };
            drawing.line(
                &across(from, (controller.right() + SERVICE_X) / 2, into),
                Style::Solid,
                true,
                None,
            );
        }
        for (from, _) in to_services.iter().filter(|(_, at)| *at == index) {
            drawing.line(
                &across(*from, (CONTROLLER_X + CONTROLLER_W + SERVICE_X) / 2, into),
                Style::Solid,
                true,
                None,
            );
        }
        drawing.dot(POLICY_X, rect.middle(), &service.policy);
        let from = Point {
            x: rect.right(),
            y: rect.middle(),
        };
        arrive(&mut arrivals, from, &service.ports, Style::Solid);
    }
    y = y.max(services_bottom) + 22;
    drawing.text(LEFT_X, y, "from inside the cluster", HEAD);
    y += 16;
    match reach.inside {
        Inside::Any { boxed, label, gate } => {
            let h = height(&boxed, LEFT_W);
            let rect = drawing.place(
                *boxed,
                PLAIN,
                Rect {
                    x: LEFT_X,
                    y,
                    width: LEFT_W,
                    height: h,
                },
            );
            drawing.text(rect.right() + 20, rect.middle() - 12, &label, LABEL);
            drawing.dot(POLICY_X, rect.middle(), &gate);
            let from = Point {
                x: rect.right(),
                y: rect.middle(),
            };
            arrive(&mut arrivals, from, &[], Style::Dashed);
        }
        Inside::Admitted(groups) => {
            for group in in_port_order(groups, &outside) {
                let mut middles = Vec::new();
                for caller in group.callers {
                    let h = height(&caller, LEFT_W);
                    let rect = drawing.place(
                        caller,
                        PLAIN,
                        Rect {
                            x: LEFT_X,
                            y,
                            width: LEFT_W,
                            height: h,
                        },
                    );
                    middles.push(rect.middle());
                    y += h + 6;
                }
                y += 6;
                let Some(&trunk) = middles.get(middles.len() / 2) else {
                    continue;
                };
                for &at in middles.iter().filter(|&&at| at != trunk) {
                    let from = Point {
                        x: LEFT_X + LEFT_W,
                        y: at,
                    };
                    let points = [
                        from,
                        Point { x: JOIN_X, y: at },
                        Point {
                            x: JOIN_X,
                            y: trunk,
                        },
                    ];
                    drawing.line(&points, Style::Dashed, false, None);
                }
                drawing.link(POLICY_X - 14, trunk - 13, &group.policy, VIA, group.href);
                drawing.dot(POLICY_X, trunk, &group.gate);
                if group.ports.is_empty() {
                    drawing.text(POLICY_X + 14, trunk - 13, "any port", LABEL);
                }
                let from = Point {
                    x: LEFT_X + LEFT_W,
                    y: trunk,
                };
                arrive(&mut arrivals, from, &group.ports, Style::Dashed);
            }
        }
    }
    let workload = ports(&mut drawing, &arrivals, reach.ports, reach.workload);
    right(&mut drawing, reach.right, workload);
    drawing.height = drawing.bottom() + 16;
    drawing
}

/// The ports that lines from outside the cluster end at, in the order
/// `draw` adds those lines, without repeats.
fn outside_ports(reach: &Reach) -> Vec<usize> {
    let ways = reach.ways.iter().filter_map(|way| match &way.path {
        Path::Ports { ports, .. } => Some(ports.as_slice()),
        _ => None,
    });
    let services = reach
        .services
        .iter()
        .map(|service| service.ports.as_slice());
    let mut outside: Vec<usize> = Vec::new();
    for port in ways.chain(services).flatten() {
        if !outside.contains(port) {
            outside.push(*port);
        }
    }
    outside
}

/// Orders `groups` so that their lines to the ports do not cross:
///
/// 1. Groups that call a port in `outside`, by the position of their
///    first such port, so each line runs beside the lines from outside
///    that end at the same port.
/// 2. Groups that call any port.
/// 3. Groups that call only ports that no line from outside ends at.
///    `sockets::ports` places each such port at the height of the
///    highest line to it.
///
/// Groups of the same rank keep their input order.
fn in_port_order(mut groups: Vec<Group>, outside: &[usize]) -> Vec<Group> {
    groups.sort_by_key(|group| {
        let first = group
            .ports
            .iter()
            .filter_map(|port| outside.iter().position(|f| f == port))
            .min();
        match (first, group.ports.is_empty()) {
            (Some(at), _) => (0, at),
            (None, true) => (1, 0),
            (None, false) => (2, 0),
        }
    });
    groups
}
