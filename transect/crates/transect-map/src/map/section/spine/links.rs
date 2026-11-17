//! The spine's lines, by the boxes at their ends: the Ingresses to the
//! Services they route to, the Services to the workload they select,
//! the workload to each claims box it stores on, each claims box to the
//! machines that mount its claims, and each machine to the backends that
//! store its volumes' data.  When there is no machine to pass through, as
//! for a workload with no pod running, a line runs from a claims box to
//! its backend, and from the workload to a backend that it names only
//! inline.  Each side box is joined to the workload, except that a
//! device is joined to the machine it is on.  A device is also joined to
//! each box in the right column that it ties to.  When the workload
//! reaches a machine that the spine already draws, the line joins the
//! workload to that machine's box on the spine.

use super::sides::{self, SideBox};
use super::{Band, MOST_MACHINES, Parts, Row};
use crate::map::section::layout::{At, Side};
use crate::map::section::lines::{Link, Style};
use crate::map::section::sentence;
use transect_graph::edges::Endpoint;

pub fn links(parts: &Parts, rows: &[Row]) -> Vec<Link> {
    let row = |band: &Band| rows.iter().position(|row| row.band == *band);
    let mut links = Vec::new();
    let door = row(&Band::Entry);
    let services_tier = usize::from(!parts.ingresses.is_empty());
    if let Some(door) = door {
        for &(ingress, service) in &parts.routes {
            let from = At::new(door, 0, ingress);
            let to = At::new(door, services_tier, service);
            links.push(link(from, to, "routes to"));
        }
    }
    let workload_row = row(&Band::Layer(parts.layer.clone()));
    let Some(workload) = workload_row.filter(|_| parts.workload.is_some()) else {
        return links;
    };
    let workload = At::new(workload, 0, 0);
    if let Some(door) = door.filter(|_| !parts.services.is_empty()) {
        for service in 0..parts.services.len() {
            links.push(link(
                At::new(door, services_tier, service),
                workload,
                "selects",
            ));
        }
    }
    let volumes = row(&Band::Volumes);
    let machines = row(&Band::Machines);
    let ground = row(&Band::Ground);
    let folded = parts.hosts.len() > MOST_MACHINES;
    let mut claims_index = 0;
    let mut ground_index = 0;
    for backing in &parts.backings {
        let claims = match volumes.filter(|_| !backing.claims.is_empty()) {
            Some(volumes) => {
                claims_index += 1;
                Some(At::new(volumes, 0, claims_index - 1))
            }
            None => None,
        };
        let hosts: Vec<At> = match machines {
            Some(machines) if folded && !backing.machines.is_empty() => {
                vec![At::new(machines, 0, 0)]
            }
            Some(machines) => parts
                .hosts
                .iter()
                .enumerate()
                .filter(|(_, host)| backing.machines.contains(&host.name))
                .map(|(index, _)| At::new(machines, 0, index))
                .collect(),
            None => Vec::new(),
        };
        if let Some(claims) = claims {
            links.push(link(workload, claims, "stores on"));
            for &host in &hosts {
                links.push(link(claims, host, "mounted on"));
            }
        }
        let (Some(ground), Some(_)) = (ground, backing.outside()) else {
            continue;
        };
        let backend = At::new(ground, 0, ground_index);
        ground_index += 1;
        let kinds = sentence::kinds_label(backing);
        let above = match (hosts.is_empty(), claims) {
            (false, _) => hosts,
            (true, Some(claims)) => vec![claims],
            (true, None) => vec![workload],
        };
        for from in above {
            links.push(link(from, backend, &kinds));
        }
    }
    let reached = parts.sides.iter().filter(|side| parts.on_spine(side));
    for side_box in reached {
        let name = side_box.names.as_ref();
        if let Some(host) = name.and_then(|name| host(parts, machines, name)) {
            let (from, to) = match side_box.inward {
                true => (host, workload),
                false => (workload, host),
            };
            links.push(Link {
                from,
                to,
                label: side_box.label.clone(),
                style: side_box.style,
                title: side_box.title.clone(),
            });
        }
    }
    let beside = parts.beside();
    let mut placed: Vec<(&Endpoint, At)> = Vec::new();
    let columns: Vec<(usize, Side, Vec<SideBox>)> = rows
        .iter()
        .enumerate()
        .flat_map(|(index, row)| {
            [Side::Left, Side::Right]
                .map(|side| (index, side, sides::column(&beside, &row.band, side)))
        })
        .collect();
    for (index, side, column) in &columns {
        for (place, side_box) in column.iter().enumerate() {
            let at = At::side(*index, *side, place);
            if let Some(end) = &side_box.end {
                placed.push((end, at));
            }
            let target = machine(parts, machines, side_box).unwrap_or(workload);
            let (from, to) = match side_box.inward {
                true => (at, target),
                false => (target, at),
            };
            links.push(Link {
                from,
                to,
                label: side_box.label.clone(),
                style: side_box.style,
                title: side_box.title.clone(),
            });
        }
    }
    let at = |end: &Endpoint| {
        let found = placed.iter().find(|(placed, _)| *placed == end);
        found.map(|(_, at)| *at)
    };
    for tie in &parts.ties {
        let (Some(from), Some(to)) = (at(&tie.from), at(&tie.to)) else {
            continue;
        };
        links.push(Link {
            from,
            to,
            label: tie.label.clone(),
            style: tie.style,
            title: tie.title.clone(),
        });
    }
    links
}

/// The box of the machine that a side box is joined to, or `None` when
/// the side box is joined to the workload or its machine has no box.
fn machine(parts: &Parts, machines: Option<usize>, side_box: &SideBox) -> Option<At> {
    host(parts, machines, side_box.machine.as_ref()?)
}

/// The box of the machine named `name` in the machines row, or the box
/// drawn in place of every machine when there are more than
/// `MOST_MACHINES`.
fn host(parts: &Parts, machines: Option<usize>, name: &str) -> Option<At> {
    let row = machines?;
    if parts.hosts.len() > MOST_MACHINES {
        return Some(At::new(row, 0, 0));
    }
    let index = parts.hosts.iter().position(|host| host.name == *name)?;
    Some(At::new(row, 0, index))
}

fn link(from: At, to: At, label: &str) -> Link {
    Link {
        from,
        to,
        label: label.into(),
        style: Style::Solid,
        title: None,
    }
}
