//! The ports on the workload's left edge, and the lines that end at
//! them.  Each port is placed at the height of the first line that ends
//! at it, when there is room, and the lines nest into it.

use super::reach::{Port, SOCKET_GAP, SOCKET_H, SOCKET_W, SOCKET_X, TOP, WORKLOAD_W, WORKLOAD_X};
use super::route::{across, lanes};
use super::{Drawing, Socket, WORKLOAD, height};
use transect_map::map::section_drawing::{Point, Rect, SectionBox, Style};

/// Where a line ends: a port, or the workload's edge, for any port.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub enum End {
    Port(usize),
    Edge(usize),
}

/// A line on its way to the workload: the point after its gate where it
/// starts, whose height the line keeps, where it ends, and its style.
pub struct Arrival {
    pub from: Point,
    pub end: End,
    pub style: Style,
}

/// Adds the lines from `from` to each of `ports`, or one to the
/// workload's edge, for any port, when there are none.
pub fn arrive(arrivals: &mut Vec<Arrival>, from: Point, ports: &[usize], style: Style) {
    if ports.is_empty() {
        let end = End::Edge(arrivals.len());
        arrivals.push(Arrival { from, end, style });
    }
    for &port in ports {
        let end = End::Port(port);
        arrivals.push(Arrival { from, end, style });
    }
}

/// Places the ports on the workload's left edge, each at the height of
/// the first line that ends at it when there is room, and places the
/// workload around them.  Then it draws each line from its start to its
/// port.  A line for any port, such as "any pod" from inside the
/// cluster, ends on the edge just under the last port and not at its
/// own height, so the workload keeps the height that its ports and
/// words need.
pub fn ports(
    drawing: &mut Drawing,
    arrivals: &[Arrival],
    ports: Vec<Port>,
    workload: SectionBox,
) -> Rect {
    let mut wanted: Vec<(i32, End)> = Vec::new();
    for arrival in arrivals {
        match wanted.iter_mut().find(|(_, end)| *end == arrival.end) {
            Some((at, _)) => *at = (*at).min(arrival.from.y),
            None => wanted.push((arrival.from.y, arrival.end)),
        }
    }
    let reached: Vec<usize> = wanted
        .iter()
        .filter_map(|(_, end)| match end {
            End::Port(port) => Some(*port),
            End::Edge(_) => None,
        })
        .collect();
    let ports_at = wanted
        .iter()
        .filter(|(_, end)| matches!(end, End::Port(_)))
        .map(|(at, _)| *at)
        .max();
    if let Some(ports_at) = ports_at {
        for (at, end) in wanted.iter_mut() {
            if matches!(end, End::Edge(_)) {
                *at = (*at).min(ports_at + SOCKET_GAP);
            }
        }
    }
    wanted.sort();
    let mut placed: Vec<(End, i32)> = Vec::new();
    let mut last = i32::MIN / 2;
    for (at, end) in wanted {
        let at = at.max(last + SOCKET_GAP);
        placed.push((end, at));
        last = at;
    }
    let first = placed.first().map_or(TOP + 60, |(_, at)| *at);
    for port in (0..ports.len()).filter(|port| !reached.contains(port)) {
        let at = match placed.is_empty() {
            true => first,
            false => last + SOCKET_GAP,
        };
        placed.push((End::Port(port), at));
        last = at;
    }
    let text = height(&workload, WORKLOAD_W - 46);
    let top = (first - 40).max(TOP);
    let bottom = (last + 36).max(top + text);
    let rect = drawing.place(
        workload,
        WORKLOAD,
        Rect {
            x: WORKLOAD_X,
            y: top,
            width: WORKLOAD_W,
            height: bottom - top,
        },
    );
    for (index, port) in ports.into_iter().enumerate() {
        let Some((_, at)) = placed.iter().find(|(end, _)| *end == End::Port(index)) else {
            continue;
        };
        drawing.sockets.push(Socket {
            rect: Rect {
                x: SOCKET_X,
                y: at - SOCKET_H / 2,
                width: SOCKET_W,
                height: SOCKET_H,
            },
            text: port.text,
            title: port.title,
        });
    }
    let mut ends: Vec<(Point, Point, Style)> = arrivals
        .iter()
        .filter_map(|arrival| {
            let (_, at) = placed.iter().find(|(end, _)| *end == arrival.end)?;
            let x = match arrival.end {
                End::Port(_) => SOCKET_X,
                End::Edge(_) => WORKLOAD_X,
            };
            Some((arrival.from, Point { x, y: *at }, arrival.style))
        })
        .collect();
    // The lanes nest when the lines are sorted by the height they leave
    // at, from the top, and the lines from one start are sorted by the
    // height they arrive at.
    ends.sort_by_key(|(from, to, _)| (from.y, to.y));
    let froms: Vec<i32> = ends.iter().map(|(from, _, _)| from.y).collect();
    let tos: Vec<i32> = ends.iter().map(|(_, to, _)| to.y).collect();
    let lanes = lanes(&froms, &tos, SOCKET_X - 14, 6);
    for ((from, to, style), lane) in ends.into_iter().zip(lanes) {
        drawing.line(&across(from, lane, to), style, true, None);
    }
    rect
}
