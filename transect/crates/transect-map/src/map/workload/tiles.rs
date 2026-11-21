//! The pod tiles on the workload page. Each tile shows the pod's
//! machine, age, and restart count, and a memory bar and a CPU bar that
//! compare the pod's usage with its requests and limits.

use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::jiff::Timestamp;
use kube::ResourceExt;

use super::super::bar::Bar;
use transect_graph::pods::finished;
use transect_ground::machines::{pod_limits, pod_requests};
use transect_observe::health::minutes_between;
use transect_observe::usage::{Usage, Used};

/// The label that a Deployment's ReplicaSet sets on each pod. Its value
/// is the middle part of the pod's name.
const POD_TEMPLATE_HASH: &str = "pod-template-hash";

#[derive(Debug, PartialEq)]
pub struct PodTile {
    /// The pod's full name, for its tooltip.
    pub name: String,
    /// The name with the template hash replaced by "…", as in
    /// "web-…-4sf9j". A narrow column then still shows the part of the
    /// name that differs between pods.
    pub short_name: String,
    pub machine: Option<String>,
    /// Minutes since the pod started, or `None` if it has not started.
    pub age: Option<i64>,
    pub restarts: i32,
    pub state: State,
    pub memory: Bar,
    pub cpu: Bar,
}

impl PodTile {
    pub fn class(&self) -> &'static str {
        match self.state {
            State::Ready => "tile",
            State::NotReady => "tile tile-not-ready",
            State::Finished => "tile tile-finished",
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum State {
    Ready,
    /// Running, or waiting to run, but not ready.
    NotReady,
    /// The pod succeeded or failed, as the pods of a Job do.
    Finished,
}

/// A tile for each of `pods`. Tiles of pods that have not finished come
/// first, and each group is sorted by name.
pub fn tiles(pods: &[&Pod], usage: Option<&Usage>, now: Timestamp) -> Vec<PodTile> {
    let mut tiles: Vec<PodTile> = pods.iter().map(|pod| tile(pod, usage, now)).collect();
    tiles.sort_by(|a, b| {
        let finished = |tile: &PodTile| tile.state == State::Finished;
        finished(a)
            .cmp(&finished(b))
            .then_with(|| a.name.cmp(&b.name))
    });
    tiles
}

pub fn tile(pod: &Pod, usage: Option<&Usage>, now: Timestamp) -> PodTile {
    let name = pod.name_any();
    let used = usage.map_or_else(Used::default, |usage| {
        usage.of_pod(&pod.namespace().unwrap_or_default(), &name)
    });
    let (requests, limits) = pod
        .spec
        .as_ref()
        .map(|spec| (pod_requests(spec), pod_limits(spec)))
        .unwrap_or_default();
    let status = pod.status.as_ref();
    let started = status.and_then(|s| s.start_time.as_ref());
    let restarts = status
        .and_then(|s| s.container_statuses.as_ref())
        .into_iter()
        .flatten()
        .map(|container| container.restart_count)
        .sum();
    let state = if finished(pod) {
        State::Finished
    } else if transect_graph::pods::ready(pod) {
        State::Ready
    } else {
        State::NotReady
    };
    PodTile {
        short_name: short_name(&name, pod.labels().get(POD_TEMPLATE_HASH)),
        machine: pod.spec.as_ref().and_then(|spec| spec.node_name.clone()),
        age: started.map(|time| minutes_between(time.0, now)),
        restarts,
        state,
        memory: Bar::new(used.memory, requests.memory, limits.memory),
        cpu: Bar::new(used.cpu, requests.cpu, limits.cpu),
        name,
    }
}

fn short_name(name: &str, hash: Option<&String>) -> String {
    match hash {
        Some(hash) if !hash.is_empty() => name.replacen(&format!("-{hash}-"), "-…-", 1),
        _ => name.into(),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_observe::usage::fixtures::sample;

    fn pod(name: &str, phase: &str, ready: bool) -> Pod {
        serde_yaml::from_str(&format!(
            r#"
metadata: {{namespace: shop, name: {name}, labels: {{pod-template-hash: 5d8f}}}}
spec:
  nodeName: alpha
  containers:
    - {{name: app, resources: {{requests: {{cpu: 200m, memory: 750Mi}}, limits: {{memory: 1500Mi}}}}}}
status:
  phase: {phase}
  startTime: '2026-01-01T08:00:00Z'
  conditions: [{{type: Ready, status: '{}'}}]
  containerStatuses:
    - {{name: app, restartCount: 2, ready: true, image: app, imageID: '', running: {{}}}}
"#,
            if ready { "True" } else { "False" }
        ))
        .unwrap()
    }

    #[rstest]
    fn reads_a_tile_from_a_pod_and_its_usage() {
        let pods = [pod("web-5d8f-x2x7q", "Running", true)];
        let labels = [
            ("namespace", "shop"),
            ("pod", "web-5d8f-x2x7q"),
            ("container", "app"),
        ];
        let usage = Usage::default().with_containers(
            vec![sample(&labels, 355.0 * 1048576.0)],
            vec![sample(&labels, 0.002)],
            &transect_observe::usage::fixtures::container_labels(),
        );
        let now = "2026-01-01T12:00:00Z".parse().unwrap();
        let tile = &tiles(&[&pods[0]], Some(&usage), now)[0];
        assert_eq!(
            (
                tile.short_name.as_str(),
                tile.machine.as_deref(),
                tile.age,
                tile.restarts,
                tile.state
            ),
            ("web-…-x2x7q", Some("alpha"), Some(240), 2, State::Ready)
        );
        assert_eq!(
            (tile.memory.used_percent(), tile.memory.request_percent()),
            (Some(24), Some(50))
        );
        assert_eq!(
            (tile.cpu.used_percent(), tile.cpu.request_percent()),
            (Some(1), Some(100))
        );
    }

    #[rstest]
    fn running_pods_come_before_finished_ones() {
        let pods = [
            pod("report-1", "Succeeded", false),
            pod("report-3", "Running", false),
            pod("report-2", "Running", true),
        ];
        let pods: Vec<&Pod> = pods.iter().collect();
        let order: Vec<(String, State)> =
            tiles(&pods, None, "2026-01-01T12:00:00Z".parse().unwrap())
                .into_iter()
                .map(|tile| (tile.name, tile.state))
                .collect();
        assert_eq!(
            order,
            vec![
                ("report-2".into(), State::Ready),
                ("report-3".into(), State::NotReady),
                ("report-1".into(), State::Finished),
            ]
        );
    }
}
