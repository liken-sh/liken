//! The text of the workload page. All other text on the page is a name
//! or a number from the cluster.

use super::super::bar::Bar;
use super::tiles::{PodTile, State};
use transect_resources::knowledge::age;

/// The pods column's heading: "Pods", or "Pods · one per machine" when
/// no two running pods share a machine.
pub fn pods_heading(tiles: &[PodTile]) -> String {
    let running: Vec<&PodTile> = tiles
        .iter()
        .filter(|t| t.state != State::Finished)
        .collect();
    let mut machines: Vec<&str> = running
        .iter()
        .filter_map(|t| t.machine.as_deref())
        .collect();
    machines.sort_unstable();
    machines.dedup();
    if running.len() > 1 && machines.len() == running.len() {
        "Pods · one per machine".into()
    } else {
        "Pods".into()
    }
}

/// The text for the count of Secrets in the "Also needs" row, such as
/// "4 Secrets". `None` when the count is 0.
pub fn secrets(count: usize) -> Option<String> {
    match count {
        0 => None,
        1 => Some("1 Secret".into()),
        count => Some(format!("{count} Secrets")),
    }
}

/// The line under a pod's name, such as "alpha · up 4d · 0 restarts".
pub fn tile_line(tile: &PodTile) -> String {
    let machine = tile.machine.as_deref().unwrap_or("not scheduled");
    let mut parts = vec![machine.to_string()];
    match (tile.state, tile.age) {
        (State::Finished, _) => parts.push("finished".into()),
        (_, Some(minutes)) => parts.push(format!("up {}", age(minutes))),
        (_, None) => {}
    }
    if tile.state == State::NotReady {
        parts.push("not ready".into());
    }
    parts.push(match tile.restarts {
        1 => "1 restart".into(),
        restarts => format!("{restarts} restarts"),
    });
    parts.join(" · ")
}

/// The legend under the tiles, which says what a full bar stands for.
/// When the memory bars and the CPU bars have the same scale, one
/// sentence covers both.
pub fn legend<'a>(tiles: impl Iterator<Item = &'a PodTile> + Clone) -> String {
    let memory = BarScale::of(tiles.clone().map(|tile| &tile.memory));
    let cpu = BarScale::of(tiles.map(|tile| &tile.cpu));
    if memory == cpu {
        return memory.sentence(None);
    }
    format!(
        "{} {}",
        memory.sentence(Some("memory")),
        cpu.sentence(Some("CPU"))
    )
}

/// What a full bar stands for, for one resource across all the tiles:
/// the scale that `Bar::scale` returns. A bar with no scale shows only
/// its figure.
#[derive(Clone, Copy, PartialEq, Eq)]
enum BarScale {
    Unscaled,
    RequestOrUnscaled,
    Request,
    Limit,
    LimitOrRequest,
}

impl BarScale {
    fn of<'a>(bars: impl Iterator<Item = &'a Bar> + Clone) -> Self {
        let limited = bars.clone().filter(|bar| bar.limit.is_some()).count();
        let scaled = bars.clone().filter(|bar| bar.scale().is_some()).count();
        let all = bars.count();
        match limited {
            0 if scaled == 0 => Self::Unscaled,
            0 if scaled < all => Self::RequestOrUnscaled,
            0 => Self::Request,
            _ if limited == all => Self::Limit,
            _ => Self::LimitOrRequest,
        }
    }

    /// The legend sentence for `resource`, or for memory and CPU
    /// together when `resource` is `None`.
    fn sentence(self, resource: Option<&str>) -> String {
        let no = resource.unwrap_or("memory or CPU");
        let bars = match resource {
            Some("memory") => "Memory bars".to_string(),
            Some(resource) => format!("{resource} bars"),
            None => "Bars".to_string(),
        };
        match self {
            Self::Unscaled => {
                format!("No {no} request or limit, so the figures show what each uses.")
            }
            Self::RequestOrUnscaled => format!(
                "No {no} limit; bars run to the request, and the figures show what one with no request uses."
            ),
            Self::Request => format!("No {no} limit; bars run to the request."),
            Self::Limit => format!("{bars} run to the limit, with the request marked."),
            Self::LimitOrRequest => {
                format!("{bars} run to the limit, or to the request where there is none.")
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn tile(machine: Option<&str>, state: State, restarts: i32) -> PodTile {
        PodTile {
            name: "web-5d8f-x2x7q".into(),
            short_name: "web-…-x2x7q".into(),
            machine: machine.map(Into::into),
            age: Some(4 * 1440),
            restarts,
            state,
            memory: Bar::default(),
            cpu: Bar::default(),
        }
    }

    #[rstest]
    #[case::ready(tile(Some("alpha"), State::Ready, 0), "alpha · up 4d · 0 restarts")]
    #[case::not_ready(
        tile(Some("alpha"), State::NotReady, 1),
        "alpha · up 4d · not ready · 1 restart"
    )]
    #[case::finished(
        tile(Some("alpha"), State::Finished, 0),
        "alpha · finished · 0 restarts"
    )]
    #[case::unscheduled(PodTile { age: None, ..tile(None, State::NotReady, 0) }, "not scheduled · not ready · 0 restarts")]
    fn a_tiles_line(#[case] tile: PodTile, #[case] line: &str) {
        assert_eq!(tile_line(&tile), line);
    }

    #[rstest]
    #[case::spread(&[Some("alpha"), Some("beta")], "Pods · one per machine")]
    #[case::sharing(&[Some("alpha"), Some("alpha")], "Pods")]
    #[case::one(&[Some("alpha")], "Pods")]
    fn the_pods_heading(#[case] machines: &[Option<&str>], #[case] heading: &str) {
        let tiles: Vec<PodTile> = machines.iter().map(|m| tile(*m, State::Ready, 0)).collect();
        assert_eq!(pods_heading(&tiles), heading);
    }

    fn with_limits(memory: bool, cpu: bool) -> PodTile {
        let limited = Bar {
            limit: Some(1.0),
            ..Bar::default()
        };
        let requested = Bar {
            request: Some(1.0),
            ..Bar::default()
        };
        PodTile {
            memory: if memory { limited } else { requested },
            cpu: if cpu { limited } else { requested },
            ..tile(Some("alpha"), State::Ready, 0)
        }
    }

    #[rstest]
    #[case::memory_limited_cpu_not(
        vec![with_limits(true, false)],
        "Memory bars run to the limit, with the request marked. \
         No CPU limit; bars run to the request."
    )]
    #[case::some_limited(
        vec![with_limits(true, true), with_limits(false, true)],
        "Memory bars run to the limit, or to the request where there is none. \
         CPU bars run to the limit, with the request marked."
    )]
    #[case::nothing_to_run_to(
        vec![tile(Some("alpha"), State::Ready, 0)],
        "No memory or CPU request or limit, so the figures show what each uses."
    )]
    #[case::neither_limited(
        vec![with_limits(false, false)],
        "No memory or CPU limit; bars run to the request."
    )]
    fn the_legend(#[case] tiles: Vec<PodTile>, #[case] legend: &str) {
        assert_eq!(super::legend(tiles.iter()), legend);
    }
}
