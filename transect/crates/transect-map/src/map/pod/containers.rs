//! The pod page has one block per container. A block shows what the
//! container runs and where its image came from, its ports, a memory
//! bar and a CPU bar against the container's own request and limit, its
//! restarts and state, and its probes.
//!
//! App containers come first, then sidecars, then init containers. A
//! sidecar is an init container with `restartPolicy: Always`, which
//! keeps running beside the app containers. Any other init container
//! runs to completion before the app containers start.

use k8s_openapi::api::core::v1::{Container, ContainerStatus, Pod, Probe as ApiProbe};
use k8s_openapi::apimachinery::pkg::util::intstr::IntOrString;

use super::super::bar::Bar;
use transect_ground::machines::{Amount, Resources};
use transect_observe::usage::{Usage, Used};
use transect_supply::images::{Origin, Origins};

#[derive(Debug, PartialEq)]
pub struct ContainerBlock {
    pub name: String,
    pub role: Role,
    pub image: Option<String>,
    /// Where the container's image came from, once it has pulled one.
    pub origin: Option<Origin>,
    /// The command followed by its arguments, when the spec sets either.
    pub command: Option<Vec<String>>,
    pub ports: Vec<Port>,
    pub memory: Bar,
    pub cpu: Bar,
    pub restarts: i32,
    pub state: State,
    /// Why its previous run stopped, when it has restarted, such as
    /// "OOMKilled" or "Error".
    pub last_stop: Option<String>,
    pub probes: Vec<Probe>,
}

impl ContainerBlock {
    /// An init container that has terminated uses no resources, so its
    /// bars would show nothing.
    pub fn finished(&self) -> bool {
        self.role == Role::Init && matches!(self.state, State::Terminated { .. })
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub enum Role {
    App,
    Sidecar,
    Init,
}

#[derive(Debug, PartialEq)]
pub struct Port {
    pub number: i32,
    pub name: Option<String>,
    /// "UDP" or "SCTP". It is `None` for TCP, the default.
    pub protocol: Option<String>,
}

/// A container's state as the kubelet reports it.
#[derive(Debug, PartialEq)]
pub enum State {
    Running {
        ready: bool,
    },
    /// Not started, or between restarts, with the kubelet's reason, such
    /// as "CrashLoopBackOff".
    Waiting(Option<String>),
    /// Stopped, with its reason and exit code. An init container ends
    /// here when it is done.
    Terminated {
        reason: Option<String>,
        code: i32,
    },
    /// The pod has no status for this container yet, as before the pod
    /// is scheduled.
    Unknown,
}

#[derive(Debug, PartialEq)]
pub struct Probe {
    pub kind: ProbeKind,
    pub action: Action,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ProbeKind {
    Startup,
    Readiness,
    Liveness,
}

#[derive(Debug, PartialEq)]
pub enum Action {
    Http { port: String, path: String },
    Tcp { port: String },
    Grpc { port: i32 },
    Exec,
}

pub fn blocks(pod: &Pod, usage: Option<&Usage>, origins: Origins) -> Vec<ContainerBlock> {
    let Some(spec) = &pod.spec else {
        return Vec::new();
    };
    let statuses = pod.status.as_ref();
    let app = statuses.and_then(|s| s.container_statuses.as_deref());
    let init = statuses.and_then(|s| s.init_container_statuses.as_deref());
    let namespace = pod.metadata.namespace.as_deref().unwrap_or_default();
    let name = pod.metadata.name.as_deref().unwrap_or_default();
    let used = |container: &str| {
        usage.map_or_else(Used::default, |usage| {
            usage.of_container(namespace, name, container)
        })
    };
    let apps = spec
        .containers
        .iter()
        .map(|container| (container, Role::App, app));
    let inits = spec.init_containers.iter().flatten().map(|container| {
        let role = if container.restart_policy.as_deref() == Some("Always") {
            Role::Sidecar
        } else {
            Role::Init
        };
        (container, role, init)
    });
    let mut blocks: Vec<ContainerBlock> = apps
        .chain(inits)
        .map(|(container, role, statuses)| {
            let status = statuses
                .into_iter()
                .flatten()
                .find(|status| status.name == container.name);
            let origin = status.and_then(|status| origins.of(pod, status));
            block(container, role, status, used(&container.name), origin)
        })
        .collect();
    blocks.sort_by_key(|block| block.role);
    blocks
}

fn block(
    container: &Container,
    role: Role,
    status: Option<&ContainerStatus>,
    used: Used,
    origin: Option<Origin>,
) -> ContainerBlock {
    let resources = container.resources.as_ref();
    let requests = Resources::from_list(resources.and_then(|r| r.requests.as_ref()));
    let limit = |name: &str| {
        let quantity = resources?.limits.as_ref()?.get(name)?;
        Amount::parse(&quantity.0)
    };
    let command: Vec<String> = container
        .command
        .iter()
        .chain(&container.args)
        .flatten()
        .cloned()
        .collect();
    let probes = [
        (ProbeKind::Startup, &container.startup_probe),
        (ProbeKind::Readiness, &container.readiness_probe),
        (ProbeKind::Liveness, &container.liveness_probe),
    ];
    ContainerBlock {
        name: container.name.clone(),
        role,
        image: container.image.clone(),
        origin,
        command: (!command.is_empty()).then_some(command),
        ports: container
            .ports
            .iter()
            .flatten()
            .map(|port| Port {
                number: port.container_port,
                name: port.name.clone(),
                protocol: port.protocol.clone().filter(|p| p != "TCP"),
            })
            .collect(),
        memory: Bar::new(used.memory, requests.memory, limit("memory")),
        cpu: Bar::new(used.cpu, requests.cpu, limit("cpu")),
        restarts: status.map_or(0, |status| status.restart_count),
        state: status.map_or(State::Unknown, state),
        last_stop: status.and_then(|status| {
            status
                .last_state
                .as_ref()?
                .terminated
                .as_ref()?
                .reason
                .clone()
        }),
        probes: probes
            .into_iter()
            .filter_map(|(kind, probe)| {
                Some(Probe {
                    kind,
                    action: action(probe.as_ref()?)?,
                })
            })
            .collect(),
    }
}

fn state(status: &ContainerStatus) -> State {
    let Some(state) = &status.state else {
        return State::Unknown;
    };
    if state.running.is_some() {
        State::Running {
            ready: status.ready,
        }
    } else if let Some(terminated) = &state.terminated {
        State::Terminated {
            reason: terminated.reason.clone(),
            code: terminated.exit_code,
        }
    } else if let Some(waiting) = &state.waiting {
        State::Waiting(waiting.reason.clone())
    } else {
        State::Unknown
    }
}

fn action(probe: &ApiProbe) -> Option<Action> {
    if let Some(http) = &probe.http_get {
        return Some(Action::Http {
            port: port(&http.port),
            path: http.path.clone().unwrap_or_else(|| "/".into()),
        });
    }
    if let Some(tcp) = &probe.tcp_socket {
        return Some(Action::Tcp {
            port: port(&tcp.port),
        });
    }
    if let Some(grpc) = &probe.grpc {
        return Some(Action::Grpc { port: grpc.port });
    }
    probe.exec.as_ref().map(|_| Action::Exec)
}

fn port(port: &IntOrString) -> String {
    match port {
        IntOrString::Int(number) => number.to_string(),
        IntOrString::String(name) => name.clone(),
    }
}

#[cfg(test)]
pub mod fixtures {
    use k8s_openapi::api::core::v1::Pod;

    /// A pod in shop named `name`, with `spec` and `status` as YAML flow
    /// mappings.
    pub fn pod(name: &str, spec: &str, status: &str) -> Pod {
        serde_yaml_ng::from_str(&format!(
            "metadata: {{namespace: shop, name: {name}}}\nspec: {spec}\nstatus: {status}"
        ))
        .unwrap()
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::fixtures::pod;
    use super::*;
    use transect_ground::machines::BYTES_PER_MI;
    use transect_observe::usage::fixtures::sample;
    use transect_supply::forge::Forges;
    use transect_supply::images::Known;

    /// No container in these tests has an image ID, so no origin is
    /// looked up.
    fn none_read() -> Origins<'static> {
        static KNOWN: std::sync::LazyLock<Known> = std::sync::LazyLock::new(Known::default);
        static FORGES: std::sync::LazyLock<Forges> = std::sync::LazyLock::new(Forges::default);
        Origins {
            known: &KNOWN,
            forges: &FORGES,
        }
    }

    fn web() -> Pod {
        pod(
            "web-1",
            r#"{
  nodeName: alpha,
  initContainers: [
    {name: migrate, image: app:1, command: [app, migrate]},
    {name: proxy, image: proxy:2, restartPolicy: Always,
     readinessProbe: {tcpSocket: {port: 8443}}}
  ],
  containers: [{
    name: app, image: app:1, args: [serve, --port, '8000'],
    ports: [{containerPort: 8000, name: http}, {containerPort: 5353, protocol: UDP}],
    resources: {requests: {cpu: 200m, memory: 750Mi}, limits: {memory: 1500Mi}},
    readinessProbe: {httpGet: {path: /healthz, port: http}},
    livenessProbe: {exec: {command: [true]}}
  }]
}"#,
            r#"{
  initContainerStatuses: [
    {name: migrate, ready: false, restartCount: 0, image: app:1, imageID: '',
     state: {terminated: {exitCode: 0, reason: Completed}}},
    {name: proxy, ready: true, restartCount: 0, image: proxy:2, imageID: '',
     state: {running: {}}}
  ],
  containerStatuses: [
    {name: app, ready: false, restartCount: 3, image: app:1, imageID: '',
     state: {waiting: {reason: CrashLoopBackOff}},
     lastState: {terminated: {exitCode: 137, reason: OOMKilled}}}
  ]
}"#,
        )
    }

    #[rstest]
    fn lists_app_containers_then_sidecars_then_init_containers() {
        let order: Vec<(String, Role)> = blocks(&web(), None, none_read())
            .into_iter()
            .map(|block| (block.name, block.role))
            .collect();
        assert_eq!(
            order,
            vec![
                ("app".into(), Role::App),
                ("proxy".into(), Role::Sidecar),
                ("migrate".into(), Role::Init),
            ]
        );
    }

    #[rstest]
    fn reads_a_container_from_its_spec_status_and_usage() {
        let labels = [
            ("namespace", "shop"),
            ("pod", "web-1"),
            ("container", "app"),
        ];
        let usage = Usage::default().with_containers(
            vec![sample(&labels, 375.0 * BYTES_PER_MI)],
            vec![sample(&labels, 0.006)],
            &transect_observe::usage::fixtures::container_labels(),
        );
        let app = blocks(&web(), Some(&usage), none_read()).remove(0);
        assert_eq!(
            app,
            ContainerBlock {
                name: "app".into(),
                role: Role::App,
                image: Some("app:1".into()),
                origin: None,
                command: Some(vec!["serve".into(), "--port".into(), "8000".into()]),
                ports: vec![
                    Port {
                        number: 8000,
                        name: Some("http".into()),
                        protocol: None
                    },
                    Port {
                        number: 5353,
                        name: None,
                        protocol: Some("UDP".into())
                    },
                ],
                memory: Bar {
                    used: Some(375.0 * BYTES_PER_MI),
                    request: Some(750.0 * BYTES_PER_MI),
                    limit: Some(1500.0 * BYTES_PER_MI),
                },
                cpu: Bar {
                    used: Some(0.006),
                    request: Some(0.2),
                    limit: None,
                },
                restarts: 3,
                state: State::Waiting(Some("CrashLoopBackOff".into())),
                last_stop: Some("OOMKilled".into()),
                probes: vec![
                    Probe {
                        kind: ProbeKind::Readiness,
                        action: Action::Http {
                            port: "http".into(),
                            path: "/healthz".into()
                        }
                    },
                    Probe {
                        kind: ProbeKind::Liveness,
                        action: Action::Exec
                    },
                ],
            }
        );
    }

    #[rstest]
    #[case::running("proxy", State::Running { ready: true })]
    #[case::finished("migrate", State::Terminated { reason: Some("Completed".into()), code: 0 })]
    fn reads_each_containers_state(#[case] name: &str, #[case] expected: State) {
        let found = blocks(&web(), None, none_read())
            .into_iter()
            .find(|b| b.name == name);
        assert_eq!(found.map(|block| block.state), Some(expected));
    }

    #[rstest]
    #[case::a_finished_init_container("migrate", true)]
    #[case::a_running_sidecar("proxy", false)]
    #[case::a_stopped_app_container("app", false)]
    fn only_a_finished_init_container_is_finished(#[case] name: &str, #[case] finished: bool) {
        let found = blocks(&web(), None, none_read())
            .into_iter()
            .find(|b| b.name == name);
        assert_eq!(found.map(|block| block.finished()), Some(finished));
    }

    #[rstest]
    fn a_pod_with_no_status_has_containers_in_no_state() {
        let pending = pod("web-2", "{containers: [{name: app}]}", "{}");
        let states: Vec<State> = blocks(&pending, None, none_read())
            .into_iter()
            .map(|b| b.state)
            .collect();
        assert_eq!(states, vec![State::Unknown]);
    }
}
