//! Tests that build the matrix from a namespace of DaemonSets on a few
//! machines.

use rstest::rstest;
use serde::de::DeserializeOwned;

use super::*;

fn yaml<K: DeserializeOwned>(text: &str) -> K {
    serde_yaml::from_str(text).unwrap()
}

fn now() -> Timestamp {
    "2026-01-01T12:00:00Z".parse().unwrap()
}

fn workload(kind: &str, name: &str) -> Object {
    let group = if kind == "Pod" { "" } else { "apps" };
    Object::new(group, kind, Some("agents"), name)
}

fn node(name: &str, labels: &str) -> Arc<Node> {
    Arc::new(yaml(&format!(
        "metadata: {{name: {name}, labels: {labels}}}"
    )))
}

fn daemon_set(name: &str, template: &str) -> Arc<DaemonSet> {
    Arc::new(yaml(&format!(
        "metadata: {{namespace: agents, name: {name}}}\nspec: {{selector: {{}}, template: {{spec: {{containers: [], {template}}}}}}}"
    )))
}

/// A pod on `node` whose one container is `ready` and restarted
/// `restarts` times, the last ten minutes before `now`.
fn pod(name: &str, node: &str, ready: bool, restarts: i32) -> Pod {
    let last = if restarts > 0 {
        "lastState: {terminated: {exitCode: 1, finishedAt: '2026-01-01T11:50:00Z'}}"
    } else {
        "lastState: {}"
    };
    yaml(&format!(
        "metadata: {{namespace: agents, name: {name}}}\nspec: {{nodeName: {node}, containers: []}}\nstatus: {{phase: Running, conditions: [{{type: Ready, status: '{}'}}], containerStatuses: [{{name: main, image: agent, imageID: '', ready: {ready}, restartCount: {restarts}, {last}}}]}}",
        if ready { "True" } else { "False" }
    ))
}

/// The cluster's three machines: two with a radio, one without.
fn nodes() -> Vec<Arc<Node>> {
    vec![
        node("web-2", "{radio: none}"),
        node("web-1", "{radio: usb}"),
        node("web-3", "{radio: usb}"),
    ]
}

struct Namespace {
    nodes: Vec<Arc<Node>>,
    workloads: Vec<Object>,
    pods: Vec<(Object, Pod)>,
    daemon_sets: Vec<Arc<DaemonSet>>,
    by_workload: HashMap<Object, Health>,
}

impl Namespace {
    fn new() -> Self {
        Self {
            nodes: nodes(),
            workloads: Vec::new(),
            pods: Vec::new(),
            daemon_sets: Vec::new(),
            by_workload: HashMap::new(),
        }
    }

    /// A DaemonSet with `template` as YAML and `pods` as built.
    fn daemon_set(mut self, name: &str, template: &str, pods: Vec<Pod>) -> Self {
        let owner = workload("DaemonSet", name);
        for pod in pods {
            self.pods.push((owner.clone(), pod));
        }
        self.daemon_sets.push(daemon_set(name, template));
        self.workloads.push(owner);
        self
    }

    fn other(mut self, kind: &str, name: &str) -> Self {
        self.workloads.push(workload(kind, name));
        self
    }

    fn failing(mut self, kind: &str, name: &str) -> Self {
        let reason = Reason::PodPending {
            pod: name.into(),
            minutes: 10,
        };
        self.by_workload
            .insert(workload(kind, name), Health::from_reasons(vec![reason]));
        self
    }

    fn matrix(&self) -> Matrix {
        let mut by_owner: HashMap<Object, Vec<&Pod>> = HashMap::new();
        for (owner, pod) in &self.pods {
            by_owner.entry(owner.clone()).or_default().push(pod);
        }
        let sources = Sources {
            by_owner: &by_owner,
            daemon_sets: &self.daemon_sets,
            nodes: &self.nodes,
            by_workload: &self.by_workload,
            plurals: &Plurals::default(),
            now: now(),
        };
        let workloads: Vec<&Object> = self.workloads.iter().collect();
        matrix(&workloads, &sources)
    }
}

const RADIO: &str = "nodeSelector: {radio: usb}";

/// A namespace of top owners, each `(kind, pods)`, with that many
/// running pods.
fn owners(kinds: &[(&str, usize)]) -> (Vec<Object>, Vec<(Object, Pod)>) {
    let mut workloads = Vec::new();
    let mut pods = Vec::new();
    for (i, (kind, count)) in kinds.iter().enumerate() {
        let owner = workload(kind, &format!("w{i}"));
        for n in 0..*count {
            pods.push((owner.clone(), pod(&format!("w{i}-{n}"), "web-1", true, 0)));
        }
        workloads.push(owner);
    }
    (workloads, pods)
}

#[rstest]
#[case::all_daemon_sets(&[("DaemonSet", 3), ("DaemonSet", 3)], true)]
#[case::many_controllers_few_pods(
    &[("DaemonSet", 3), ("DaemonSet", 3), ("Deployment", 1), ("Deployment", 1), ("Deployment", 1)],
    true
)]
#[case::half_the_pods_is_not_most(&[("DaemonSet", 1), ("DaemonSet", 1), ("StatefulSet", 2)], false)]
#[case::one_alone(&[("DaemonSet", 3)], false)]
#[case::none(&[("Deployment", 2), ("Deployment", 2)], false)]
fn most_pods_run_under_daemon_sets(#[case] kinds: &[(&str, usize)], #[case] matrix: bool) {
    let (workloads, pods) = owners(kinds);
    let mut by_owner: HashMap<Object, Vec<&Pod>> = HashMap::new();
    for (owner, pod) in &pods {
        by_owner.entry(owner.clone()).or_default().push(pod);
    }
    let workloads: Vec<&Object> = workloads.iter().collect();
    assert_eq!(draws_as_matrix(&workloads, &by_owner), matrix);
}

fn columns(matrix: &Matrix) -> Vec<(&str, bool)> {
    let columns = matrix.machines.iter();
    columns
        .map(|c| (c.name.as_str(), c.control_plane))
        .collect()
}

#[rstest]
fn the_columns_are_every_machine_by_name() {
    let matrix = Namespace::new().matrix();
    assert_eq!(
        columns(&matrix),
        [("web-1", false), ("web-2", false), ("web-3", false)]
    );
}

#[rstest]
fn the_control_plane_columns_come_first() {
    let namespace = Namespace {
        nodes: vec![
            node("web-1", "{}"),
            node("web-2", "{node-role.kubernetes.io/control-plane: ''}"),
        ],
        ..Namespace::new()
    };
    let matrix = namespace.matrix();
    assert_eq!(columns(&matrix), [("web-2", true), ("web-1", false)]);
}

#[rstest]
#[case::ready_everywhere(
    "hostNetwork: false",
    vec![pod("a", "web-1", true, 0), pod("b", "web-2", true, 0), pod("c", "web-3", true, 0)],
    vec![Cell::Ready, Cell::Ready, Cell::Ready]
)]
#[case::skipped_by_its_selector(
    RADIO,
    vec![pod("a", "web-1", true, 0), pod("c", "web-3", true, 0)],
    vec![Cell::Ready, Cell::Skipped(Skip::NodeSelector), Cell::Ready]
)]
#[case::missing_where_it_fits(
    RADIO,
    vec![pod("a", "web-1", true, 0)],
    vec![Cell::Ready, Cell::Skipped(Skip::NodeSelector), Cell::Missing]
)]
#[case::not_ready_and_restarting(
    "hostNetwork: false",
    vec![pod("a", "web-1", false, 0), pod("b", "web-2", true, 4), pod("c", "web-3", true, 0)],
    vec![Cell::NotReady, Cell::Restarting { restarts: 4 }, Cell::Ready]
)]
#[case::worst_of_two_in_a_rollout(
    "hostNetwork: false",
    vec![pod("a", "web-1", true, 0), pod("a2", "web-1", false, 0), pod("b", "web-2", true, 0), pod("c", "web-3", true, 0)],
    vec![Cell::NotReady, Cell::Ready, Cell::Ready]
)]
fn each_cell_reads_the_pod_on_its_machine(
    #[case] template: &str,
    #[case] pods: Vec<Pod>,
    #[case] cells: Vec<Cell>,
) {
    let matrix = Namespace::new()
        .daemon_set("radio-agent", template, pods)
        .matrix();
    assert_eq!(matrix.rows[0].cells, cells);
}

#[rstest]
fn the_rows_go_by_name_and_count_their_pods() {
    let matrix = Namespace::new()
        .daemon_set(
            "log-agent",
            "hostNetwork: false",
            vec![pod("l", "web-1", true, 0)],
        )
        .daemon_set("disk-agent", RADIO, vec![pod("d", "web-3", true, 0)])
        .matrix();
    let names: Vec<&str> = matrix.rows.iter().map(|row| row.name.as_str()).collect();
    assert_eq!(names, ["disk-agent", "log-agent"]);
    assert_eq!(matrix.pods, 2);
    assert_eq!(
        matrix.rows[0].href.as_deref(),
        Some("/namespaces/agents/daemonsets/disk-agent")
    );
}

#[rstest]
fn the_other_top_owners_link_to_their_pages() {
    let matrix = Namespace::new()
        .daemon_set("log-agent", "hostNetwork: false", Vec::new())
        .other("Deployment", "controller")
        .other("Pod", "debug")
        .failing("Deployment", "controller")
        .matrix();
    assert_eq!(
        matrix.others,
        [
            Other {
                kind: "Deployment".into(),
                name: "controller".into(),
                href: Some("/namespaces/agents/deployments/controller".into()),
                warm: true,
            },
            Other {
                kind: "Pod".into(),
                name: "debug".into(),
                href: Some("/namespaces/agents/pods/debug".into()),
                warm: false,
            },
        ]
    );
}

#[rstest]
#[case::ready(Cell::Ready, 0)]
#[case::restarting(Cell::Restarting { restarts: 2 }, 1)]
#[case::not_ready(Cell::NotReady, 2)]
#[case::missing(Cell::Missing, 3)]
#[case::skipped(Cell::Skipped(Skip::NodeSelector), 4)]
fn each_legend_sample_draws_as_its_cells_do(#[case] cell: Cell, #[case] entry: usize) {
    let (class, glyph, _) = LEGEND[entry];
    assert_eq!((class, glyph), (cell.class(), cell.glyph()));
}
