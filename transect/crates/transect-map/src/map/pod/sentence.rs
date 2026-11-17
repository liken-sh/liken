//! The words the pod page shows.  Everything else on it is a name or a
//! number from the cluster.

use k8s_openapi::jiff::Timestamp;

use super::super::bar::{Bar, mebibytes, millicores};
use super::containers::{Action, ContainerBlock, Port, Probe, ProbeKind, Role, State};
use super::placement::{OnMachine, Placement};
use transect_ground::machines::trimmed;
use transect_resources::knowledge::age;

/// A container block's label: "Container", "Sidecar", "Init container".
pub fn role(role: Role) -> &'static str {
    match role {
        Role::App => "Container",
        Role::Sidecar => "Sidecar",
        Role::Init => "Init container",
    }
}

/// The command and its arguments as a shell would read them, with an
/// argument that contains a space or is empty in double quotes:
/// `sh -c "exec app"`.
pub fn command(words: &[String]) -> String {
    let quoted = words.iter().map(|word| {
        if word.is_empty() || word.contains(char::is_whitespace) {
            format!("\"{word}\"")
        } else {
            word.clone()
        }
    });
    quoted.collect::<Vec<_>>().join(" ")
}

/// "listens on :8000 http · :5353/UDP", or none with no port declared.
pub fn ports(ports: &[Port]) -> Option<String> {
    if ports.is_empty() {
        return None;
    }
    let each = ports.iter().map(|port| {
        let protocol = port
            .protocol
            .as_ref()
            .map(|p| format!("/{p}"))
            .unwrap_or_default();
        let name = port
            .name
            .as_ref()
            .map(|n| format!(" {n}"))
            .unwrap_or_default();
        format!(":{}{protocol}{name}", port.number)
    });
    Some(format!(
        "listens on {}",
        each.collect::<Vec<_>>().join(" · ")
    ))
}

/// The figures under a memory bar: what the container uses, then what it
/// requests and is limited to.  ("368Mi", "req 750Mi · limit 1500Mi")
pub fn memory_figures(bar: &Bar) -> (String, String) {
    figures(bar, mebibytes)
}

/// ("6m", "req 200m · no limit")
pub fn cpu_figures(bar: &Bar) -> (String, String) {
    figures(bar, millicores)
}

fn figures(bar: &Bar, amount: impl Fn(f64) -> String) -> (String, String) {
    let used = bar.used.map_or("no reading".into(), &amount);
    let request = bar
        .request
        .map_or("no request".into(), |r| format!("req {}", amount(r)));
    let limit = bar
        .limit
        .map_or("no limit".into(), |l| format!("limit {}", amount(l)));
    (used, format!("{request} · {limit}"))
}

/// How a chip under a container is drawn.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Tone {
    Plain,
    /// Something that is done or not yet known, which is drawn faint.
    Faint,
    /// Something that needs a look, in --warm.
    Warn,
}

#[derive(Debug, PartialEq)]
pub struct Chip {
    pub text: String,
    pub tone: Tone,
}

impl Chip {
    fn new(text: impl Into<String>, tone: Tone) -> Self {
        Self {
            text: text.into(),
            tone,
        }
    }

    pub fn class(&self) -> &'static str {
        match self.tone {
            Tone::Plain => "pod-chip",
            Tone::Faint => "pod-chip pod-chip-faint",
            Tone::Warn => "pod-chip pod-chip-warn",
        }
    }
}

/// The chips under a container: its state when it is not running and
/// ready, the pod's QoS class, its restarts and why it last stopped,
/// and its probes.  An init container that runs to completion has no
/// probes, and the QoS class's effect on a running pod does not apply to
/// it, so it shows neither.
pub fn chips(block: &ContainerBlock, qos: Option<&str>) -> Vec<Chip> {
    let mut chips: Vec<Chip> = state(&block.state, block.role).into_iter().collect();
    let runs = block.role != Role::Init;
    if let Some(qos) = qos.filter(|_| runs) {
        chips.push(Chip::new(format!("QoS {qos}"), Tone::Plain));
    }
    chips.push(Chip::new(restarts(block.restarts), Tone::Plain));
    if let Some(reason) = block.last_stop.as_ref().filter(|_| block.restarts > 0) {
        chips.push(Chip::new(format!("last stopped: {reason}"), Tone::Warn));
    }
    if runs {
        if block.probes.is_empty() {
            chips.push(Chip::new("no probes", Tone::Faint));
        }
        chips.extend(
            block
                .probes
                .iter()
                .map(|p| Chip::new(probe(p), Tone::Plain)),
        );
    }
    chips
}

fn state(state: &State, role: Role) -> Option<Chip> {
    match state {
        State::Running { ready: true } => None,
        State::Running { ready: false } if role == Role::Init => None,
        State::Running { ready: false } => Some(Chip::new("not ready", Tone::Warn)),
        State::Waiting(Some(reason)) => Some(Chip::new(format!("waiting: {reason}"), Tone::Warn)),
        State::Waiting(None) => Some(Chip::new("waiting", Tone::Warn)),
        State::Terminated { code: 0, .. } if role == Role::Init => {
            Some(Chip::new("ran to completion", Tone::Faint))
        }
        State::Terminated { reason, code } => {
            let reason = reason.as_deref().unwrap_or("stopped");
            Some(Chip::new(format!("{reason}, exit {code}"), Tone::Warn))
        }
        State::Unknown => Some(Chip::new("no status yet", Tone::Faint)),
    }
}

fn restarts(count: i32) -> String {
    match count {
        1 => "1 restart".into(),
        count => format!("{count} restarts"),
    }
}

/// "readiness HTTP :8000/healthz", "liveness TCP :http", "startup
/// exec".
fn probe(probe: &Probe) -> String {
    let kind = match probe.kind {
        ProbeKind::Startup => "startup",
        ProbeKind::Readiness => "readiness",
        ProbeKind::Liveness => "liveness",
    };
    let action = match &probe.action {
        Action::Http { port, path } => format!("HTTP :{port}{path}"),
        Action::Tcp { port } => format!("TCP :{port}"),
        Action::Grpc { port } => format!("gRPC :{port}"),
        Action::Exec => "exec".into(),
    };
    format!("{kind} {action}")
}

/// What follows the machine's name: "control plane · 4 cores".
pub fn machine_line(machine: &OnMachine) -> String {
    let role = if machine.control_plane {
        "control plane"
    } else {
        "worker"
    };
    match machine.cores {
        Some(1.0) => format!("{role} · 1 core"),
        Some(cores) => format!("{role} · {} cores", trimmed(cores)),
        None => role.into(),
    }
}

/// The line after it: "46 pods here · started 2026-11-03 22:19 UTC, 4d
/// ago".
pub fn placement_line(placement: &Placement) -> String {
    let pods = placement.machine.as_ref().and_then(|machine| machine.pods);
    let pods = pods.map(|pods| match pods {
        1 => "1 pod here".to_string(),
        pods => format!("{pods} pods here"),
    });
    let started = match placement.started {
        Some((time, minutes)) => format!("started {}, {} ago", utc(time), age(minutes)),
        None => "not started".into(),
    };
    pods.into_iter()
        .chain([started])
        .collect::<Vec<_>>()
        .join(" · ")
}

fn utc(time: Timestamp) -> String {
    time.strftime("%Y-%m-%d %H:%M UTC").to_string()
}

/// A pod in the row of its workload's pods: the part of its name after
/// a Deployment's template hash, as "…4sf9j", and its machine.
pub fn switch_label(short_name: &str, machine: Option<&str>) -> String {
    let name = match short_name.rsplit_once("-…-") {
        Some((_, suffix)) => format!("…{suffix}"),
        None => short_name.into(),
    };
    match machine {
        Some(machine) => format!("{name} · {machine}"),
        None => format!("{name} · not scheduled"),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::plain(&["serve", "--port", "8000"], "serve --port 8000")]
    #[case::a_script(&["sh", "-c", "exec app serve"], "sh -c \"exec app serve\"")]
    #[case::an_empty_argument(&["app", ""], "app \"\"")]
    fn a_command(#[case] words: &[&str], #[case] text: &str) {
        let words: Vec<String> = words.iter().map(|w| w.to_string()).collect();
        assert_eq!(command(&words), text);
    }

    fn port(number: i32, name: Option<&str>, protocol: Option<&str>) -> Port {
        Port {
            number,
            name: name.map(Into::into),
            protocol: protocol.map(Into::into),
        }
    }

    #[rstest]
    #[case::none(vec![], None)]
    #[case::one(vec![port(8000, None, None)], Some("listens on :8000"))]
    #[case::named_and_udp(
        vec![port(8000, Some("http"), None), port(5353, None, Some("UDP"))],
        Some("listens on :8000 http · :5353/UDP")
    )]
    fn the_ports_line(#[case] list: Vec<Port>, #[case] line: Option<&str>) {
        assert_eq!(ports(&list).as_deref(), line);
    }

    #[rstest]
    #[case::to_a_limit(
        memory_figures(&Bar { used: Some(368.0 * 1048576.0), request: Some(750.0 * 1048576.0), limit: Some(1500.0 * 1048576.0) }),
        ("368Mi", "req 750Mi · limit 1500Mi")
    )]
    #[case::no_limit(
        cpu_figures(&Bar { used: Some(0.006), request: Some(0.2), limit: None }),
        ("6m", "req 200m · no limit")
    )]
    #[case::nothing(cpu_figures(&Bar::default()), ("no reading", "no request · no limit"))]
    fn figures_under_a_bar(#[case] figures: (String, String), #[case] expected: (&str, &str)) {
        assert_eq!((figures.0.as_str(), figures.1.as_str()), expected);
    }

    fn block(role: Role, state: State, restarts: i32, probes: Vec<Probe>) -> ContainerBlock {
        ContainerBlock {
            name: "app".into(),
            role,
            image: None,
            origin: None,
            command: None,
            ports: vec![],
            memory: Bar::default(),
            cpu: Bar::default(),
            restarts,
            state,
            last_stop: Some("OOMKilled".into()),
            probes,
        }
    }

    fn texts(chips: Vec<Chip>) -> Vec<(String, Tone)> {
        chips
            .into_iter()
            .map(|chip| (chip.text, chip.tone))
            .collect()
    }

    fn chip(text: &str, tone: Tone) -> (String, Tone) {
        (text.into(), tone)
    }

    #[rstest]
    #[case::running_with_no_probes(
        block(Role::App, State::Running { ready: true }, 0, vec![]),
        vec![chip("QoS Burstable", Tone::Plain), chip("0 restarts", Tone::Plain), chip("no probes", Tone::Faint)]
    )]
    #[case::crashing_with_probes(
        block(Role::App, State::Waiting(Some("CrashLoopBackOff".into())), 3, vec![
            Probe { kind: ProbeKind::Readiness, action: Action::Http { port: "8000".into(), path: "/healthz".into() } },
            Probe { kind: ProbeKind::Liveness, action: Action::Tcp { port: "http".into() } },
        ]),
        vec![
            chip("waiting: CrashLoopBackOff", Tone::Warn),
            chip("QoS Burstable", Tone::Plain),
            chip("3 restarts", Tone::Plain),
            chip("last stopped: OOMKilled", Tone::Warn),
            chip("readiness HTTP :8000/healthz", Tone::Plain),
            chip("liveness TCP :http", Tone::Plain),
        ]
    )]
    #[case::a_sidecar_not_ready(
        block(Role::Sidecar, State::Running { ready: false }, 1, vec![
            Probe { kind: ProbeKind::Startup, action: Action::Exec },
        ]),
        vec![
            chip("not ready", Tone::Warn),
            chip("QoS Burstable", Tone::Plain),
            chip("1 restart", Tone::Plain),
            chip("last stopped: OOMKilled", Tone::Warn),
            chip("startup exec", Tone::Plain),
        ]
    )]
    #[case::an_init_container_done(
        block(Role::Init, State::Terminated { reason: Some("Completed".into()), code: 0 }, 0, vec![]),
        vec![chip("ran to completion", Tone::Faint), chip("0 restarts", Tone::Plain)]
    )]
    #[case::a_container_that_failed(
        block(Role::App, State::Terminated { reason: Some("Error".into()), code: 1 }, 0, vec![]),
        vec![
            chip("Error, exit 1", Tone::Warn),
            chip("QoS Burstable", Tone::Plain),
            chip("0 restarts", Tone::Plain),
            chip("no probes", Tone::Faint),
        ]
    )]
    #[case::not_started(
        block(Role::App, State::Unknown, 0, vec![]),
        vec![
            chip("no status yet", Tone::Faint),
            chip("QoS Burstable", Tone::Plain),
            chip("0 restarts", Tone::Plain),
            chip("no probes", Tone::Faint),
        ]
    )]
    fn chips_under_a_container(
        #[case] block: ContainerBlock,
        #[case] expected: Vec<(String, Tone)>,
    ) {
        assert_eq!(texts(chips(&block, Some("Burstable"))), expected);
    }

    fn on(control_plane: bool, cores: Option<f64>, pods: Option<usize>) -> OnMachine {
        OnMachine {
            name: "alpha".into(),
            control_plane,
            cores,
            pods,
        }
    }

    #[rstest]
    #[case::control_plane(on(true, Some(4.0), None), "control plane · 4 cores")]
    #[case::one_core(on(false, Some(1.0), None), "worker · 1 core")]
    #[case::a_fraction(on(false, Some(3.5), None), "worker · 3.5 cores")]
    #[case::unknown_size(on(false, None, None), "worker")]
    fn the_machine_line(#[case] machine: OnMachine, #[case] line: &str) {
        assert_eq!(machine_line(&machine), line);
    }

    #[rstest]
    #[case::running(
        Placement { machine: Some(on(false, None, Some(46))), started: Some(("2026-11-03T22:19:40Z".parse().unwrap(), 4 * 1440)), held: vec![] },
        "46 pods here · started 2026-11-03 22:19 UTC, 4d ago"
    )]
    #[case::alone(
        Placement { machine: Some(on(false, None, Some(1))), started: None, held: vec![] },
        "1 pod here · not started"
    )]
    #[case::not_scheduled(Placement { machine: None, started: None, held: vec![] }, "not started")]
    fn the_placement_line(#[case] placement: Placement, #[case] line: &str) {
        assert_eq!(placement_line(&placement), line);
    }

    #[rstest]
    #[case::a_deployments_pod("web-…-4sf9j", Some("alpha"), "…4sf9j · alpha")]
    #[case::a_stateful_sets_pod("db-0", Some("beta"), "db-0 · beta")]
    #[case::not_scheduled("db-1", None, "db-1 · not scheduled")]
    fn a_pod_in_the_row(#[case] short: &str, #[case] machine: Option<&str>, #[case] label: &str) {
        assert_eq!(switch_label(short, machine), label);
    }
}
