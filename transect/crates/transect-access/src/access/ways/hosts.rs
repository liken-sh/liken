//! The hosts the Ingresses route.  Each host and port that the
//! controller serves an Ingress on is one way in, and every Ingress
//! that routes that host and port goes into that one row.

use std::collections::{BTreeMap, BTreeSet};

use k8s_openapi::api::networking::v1::{Ingress, IngressBackend};
use kube::ResourceExt;

use super::routed::{routed, workloads};
use super::{Entered, Gate, Leg, Sources, State, Through, Way, Ways, policy};
use transect_graph::edges::{Object, Provenance, Stated};
use transect_network::entry::entry_points;
use transect_resources::intent::Line;

/// One Ingress's route of one host on one port.
struct Route {
    ingress: Object,
    /// The ingress controller that serves it.
    controller: Object,
    tls: bool,
    /// The TLS port the ingress controller redirects this port to, and
    /// the `IngressController` that declares it, when the route's TLS
    /// names the host.
    redirect: Option<(u16, String)>,
    sign_in: Vec<Object>,
    /// The sentences that state that the workloads it reaches sign their
    /// callers in themselves.
    declared: Declared,
    workloads: Vec<Object>,
    backends: Vec<super::Backend>,
}

/// What the people who run the cluster wrote about whether the
/// workloads a route reaches sign their callers in themselves.  `All` is
/// the route's own sentence or a sentence from each workload, and
/// `Some` is a sentence from some of the workloads.
#[derive(Clone, Debug)]
enum Declared {
    All(Line),
    Some(Line),
    None,
}

impl Declared {
    fn of(workloads: &[Object], sources: &Sources) -> Self {
        let lines: Vec<Line> = workloads.iter().filter_map(sources.declares).collect();
        let every = !workloads.is_empty() && lines.len() == workloads.len();
        match lines.into_iter().next() {
            None => Self::None,
            Some(line) if every => Self::All(line),
            Some(line) => Self::Some(line),
        }
    }

    fn all(&self) -> Option<&Line> {
        match self {
            Self::All(line) => Some(line),
            _ => None,
        }
    }

    fn some(&self) -> Option<&Line> {
        match self {
            Self::Some(line) => Some(line),
            _ => None,
        }
    }
}

/// Reads the host rows, the ingress controllers that the hosts enter
/// through, and the sign-in callbacks and unserved Ingresses that get
/// no row.
pub fn read(sources: &Sources) -> Ways {
    let mut routes: BTreeMap<(String, u16), Vec<Route>> = BTreeMap::new();
    let mut entered: BTreeMap<Object, BTreeSet<String>> = BTreeMap::new();
    let mut callbacks: Vec<(Object, usize)> = Vec::new();
    let signing = |ingress: &Ingress| {
        let routed = routed(ingress);
        let backends: Vec<&IngressBackend> =
            routed.iter().flat_map(|(_, b)| b.iter().copied()).collect();
        let workloads = workloads(ingress.namespace().as_deref(), &backends, sources);
        !workloads.is_empty() && workloads.iter().all(|w| sources.signs_in.contains(w))
    };
    // An Ingress that routes only to workloads that routes sign in
    // through, on hosts that other Ingresses also route, is the
    // sign-in's own callback on those hosts.  An Ingress with a host of
    // its own, such as the sign-in's own page, is a way in.
    let elsewhere: BTreeSet<String> = sources
        .ingresses
        .iter()
        .filter(|ingress| !signing(ingress))
        .flat_map(|ingress| routed(ingress).into_iter().map(|(host, _)| host))
        .collect();
    let mut unserved: Vec<Object> = Vec::new();
    for ingress in sources.ingresses {
        let object = Object::new(
            "networking.k8s.io",
            "Ingress",
            ingress.namespace().as_deref(),
            &ingress.name_any(),
        );
        let routed = routed(ingress);
        let hosts: Vec<String> = routed.iter().map(|(host, _)| host.clone()).collect();
        if signing(ingress) && hosts.iter().all(|host| elsewhere.contains(host)) {
            callbacks.push((object, hosts.len()));
            continue;
        }
        let controller = (sources.controller)(ingress);
        let Some(workload) = controller.workload else {
            unserved.push(object);
            continue;
        };
        let served = match controller.entry_points {
            Some(points) => points.of(ingress),
            None => entry_points::conventional(ingress),
        };
        let entry = entered.entry(workload.clone()).or_default();
        entry.extend(hosts.iter().cloned());
        let sign_in = (sources.sign_in)(&object);
        let route_declares = (sources.declares)(&object);
        for (host, backends) in &routed {
            let namespace = ingress.namespace();
            let workloads = workloads(namespace.as_deref(), backends, sources);
            let declared = match &route_declares {
                Some(line) => Declared::All(line.clone()),
                None => Declared::of(&workloads, sources),
            };
            let backends =
                super::backends::of_backends(namespace, backends.iter().copied(), sources.services);
            for port in &served {
                let named = tls_names(ingress, host);
                let redirect = port.redirect.filter(|_| named);
                let route = Route {
                    ingress: object.clone(),
                    controller: workload.clone(),
                    tls: port.tls && named,
                    redirect: redirect.zip(controller.declared_by.map(String::from)),
                    sign_in: sign_in.clone(),
                    declared: declared.clone(),
                    workloads: workloads.clone(),
                    backends: backends.clone(),
                };
                routes
                    .entry((host.clone(), port.port))
                    .or_default()
                    .push(route);
            }
        }
    }
    let rows = routes
        .into_iter()
        .map(|((host, port), routes)| row(host, port, &routes, sources))
        .collect();
    let entered = entered
        .into_iter()
        .map(|(workload, hosts)| Entered {
            workload,
            hosts: hosts.len(),
            service: None,
        })
        .collect();
    Ways {
        rows,
        entered,
        callbacks,
        unserved,
    }
}

fn row(host: String, port: u16, routes: &[Route], sources: &Sources) -> Way {
    let mut via: Vec<Object> = Vec::new();
    let mut reaches: Vec<Object> = Vec::new();
    let mut names: Vec<Object> = Vec::new();
    let mut backends: Vec<super::Backend> = Vec::new();
    for route in routes {
        for backend in &route.backends {
            if !backends.contains(backend) {
                backends.push(backend.clone());
            }
        }
        if !via.contains(&route.ingress) {
            via.push(route.ingress.clone());
        }
        for workload in &route.workloads {
            if !reaches.contains(workload) {
                reaches.push(workload.clone());
            }
        }
        for name in &route.sign_in {
            if !names.contains(name) {
                names.push(name.clone());
            }
        }
    }
    let state = |count: usize| match count {
        0 => State::Off,
        n if n == routes.len() => State::On,
        _ => State::Half,
    };
    let tls = tls(routes, port, state(routes.iter().filter(|r| r.tls).count()));
    let named = names
        .iter()
        .map(|n| transect_graph::links::kind_and_name_in(n, None))
        .collect::<Vec<_>>()
        .join(", ");
    let sign_in = sign_in(routes, named, names);
    Way {
        through: Through::Host,
        name: host,
        machine: None,
        via,
        ports: vec![port.to_string()],
        tls,
        sign_in,
        policy: policy::gate(
            &reaches,
            &[Leg::of(port, &backends)],
            routes.first().map(|r| &r.controller),
            sources,
        ),
        reaches,
        backends,
    }
}

/// The TLS gate of a row whose routes put TLS on it in `state`.  When
/// the controller redirects a route to a TLS port, and the route's TLS
/// names the host, the route declares TLS.  The gate is declared and
/// not on because the redirect is the controller's configuration, which
/// an `IngressController` states and the Ingress does not.
fn tls(routes: &[Route], port: u16, state: State) -> Gate {
    let redirected = routes
        .iter()
        .filter(|r| !r.tls)
        .map(|r| r.redirect.as_ref());
    let redirected: Option<Vec<&(u16, String)>> = redirected.collect();
    match (state, redirected.as_deref()) {
        (State::On, _) => Gate::of(State::On, "TLS ends at the ingress controller."),
        (_, Some([(to, name), ..])) => Gate {
            said: Some(Provenance::Intent(Stated::IngressController {
                field: "redirectsTo".into(),
                name: name.clone(),
            })),
            ..Gate::of(
                State::Declared,
                &format!(
                    "The ingress controller redirects port {port} to {to}, as IngressController {name} declares."
                ),
            )
        },
        (State::Half, _) => Gate::of(
            State::Half,
            "TLS ends at the ingress controller on some of the routes for this host and port.",
        ),
        _ => Gate::of(
            State::Off,
            "No Ingress names TLS for this host on this port.",
        ),
    }
}

/// The sign-in gate of a row: on when every route signs in through an
/// object, declared when each route that does not has a sentence that
/// states the workload signs its callers in itself, and half when some
/// routes have neither.  An object that puts sign-in on a route takes
/// precedence over a sentence.
fn sign_in(routes: &[Route], named: String, names: Vec<Object>) -> Gate {
    let filled = routes.iter().filter(|r| !r.sign_in.is_empty()).count();
    let unfilled = routes.iter().filter(|r| r.sign_in.is_empty());
    let unfilled: Vec<&Route> = unfilled.collect();
    let declared: Vec<&Line> = unfilled.iter().filter_map(|r| r.declared.all()).collect();
    let partly = unfilled.iter().find_map(|r| r.declared.some());
    let missing = routes.len() - filled - declared.len();
    let said = declared.first().copied().or(partly);
    let said = said.map(|line| line.provenance.clone());
    let (state, title) = match (filled, declared.first(), missing) {
        (_, None, 0) => (State::On, format!("Signs in through {named}.")),
        (0, Some(line), 0) => (State::Declared, line.text.clone()),
        (_, Some(line), 0) => (
            State::Declared,
            format!(
                "Some of its routes sign in through {named}; on the others the workload signs its callers in itself: {}",
                line.text
            ),
        ),
        (0, None, _) => match partly {
            Some(line) => (
                State::Half,
                format!(
                    "Some of the workloads its routes reach sign their callers in themselves: {}",
                    line.text
                ),
            ),
            None => (
                State::Off,
                "No route to it signs in through anything in the cluster.".into(),
            ),
        },
        _ => (
            State::Half,
            format!(
                "Some of its routes sign in through {}; the others sign in through nothing in the cluster.",
                match (named.is_empty(), declared.first()) {
                    (true, Some(line)) => format!(
                        "the workload itself, as stored text declares: {}",
                        line.text
                    ),
                    (false, Some(line)) => format!(
                        "{named}, or the workload itself, as stored text declares: {}",
                        line.text
                    ),
                    _ => named,
                }
            ),
        ),
    };
    Gate {
        state,
        title,
        names,
        said,
    }
}

/// Whether `ingress`'s TLS names `host`, or names no hosts, which
/// covers every host it routes.
fn tls_names(ingress: &Ingress, host: &str) -> bool {
    let spec = ingress.spec.as_ref();
    let tls = spec
        .and_then(|spec| spec.tls.as_ref())
        .into_iter()
        .flatten();
    tls.into_iter().any(|tls| match &tls.hosts {
        Some(hosts) if !hosts.is_empty() => hosts.iter().any(|named| named == host),
        _ => true,
    })
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use super::super::{Controller, Isolation};
    use super::*;

    fn ingress(name: &str, host: &str, service: &str) -> Arc<Ingress> {
        let text = format!(
            "{{metadata: {{namespace: auth, name: {name}}}, spec: {{rules: [{{host: {host}, http: {{paths: [{{path: /, pathType: Prefix, backend: {{service: {{name: {service}, port: {{number: 80}}}}}}}}]}}}}]}}}}"
        );
        Arc::new(serde_yaml::from_str(&text).unwrap())
    }

    fn workload(name: &str) -> Object {
        Object::new("apps", "Deployment", Some("auth"), name)
    }

    fn route(signs_in: bool, declared: bool, tls: bool, redirect: bool) -> Route {
        let line = || Line {
            text: "OAuth, in the app".into(),
            provenance: Provenance::Kubernetes,
        };
        Route {
            ingress: Object::new("networking.k8s.io", "Ingress", Some("auth"), "shop"),
            controller: workload("controller"),
            tls,
            redirect: redirect.then(|| (443, "proxy".to_string())),
            sign_in: match signs_in {
                true => vec![workload("gate")],
                false => Vec::new(),
            },
            declared: match declared {
                true => Declared::All(line()),
                false => Declared::None,
            },
            workloads: vec![workload("shop")],
            backends: Vec::new(),
        }
    }

    #[rstest::rstest]
    #[case::an_object(vec![route(true, false, false, false)], State::On)]
    #[case::a_sentence(vec![route(false, true, false, false)], State::Declared)]
    #[case::an_object_wins(vec![route(true, true, false, false)], State::On)]
    #[case::an_object_and_a_sentence(vec![route(true, false, false, false), route(false, true, false, false)], State::Declared)]
    #[case::one_with_neither(vec![route(false, true, false, false), route(false, false, false, false)], State::Half)]
    #[case::neither(vec![route(false, false, false, false)], State::Off)]
    fn reads_the_sign_in_gate(#[case] routes: Vec<Route>, #[case] expected: State) {
        let names = vec![workload("gate")];
        assert_eq!(sign_in(&routes, String::new(), names).state, expected);
    }

    #[rstest::rstest]
    #[case::tls(vec![route(false, false, true, false)], State::On)]
    #[case::a_redirect(vec![route(false, false, false, true)], State::Declared)]
    #[case::tls_and_a_redirect(vec![route(false, false, true, false), route(false, false, false, true)], State::Declared)]
    #[case::one_with_neither(vec![route(false, false, true, false), route(false, false, false, false)], State::Half)]
    #[case::neither(vec![route(false, false, false, false)], State::Off)]
    fn reads_the_tls_gate(#[case] routes: Vec<Route>, #[case] expected: State) {
        let count = routes.iter().filter(|r| r.tls).count();
        let state = match count {
            0 => State::Off,
            n if n == routes.len() => State::On,
            _ => State::Half,
        };
        assert_eq!(tls(&routes, 80, state).state, expected);
    }

    /// The sign-in's own page is a way in; its callback on the shop's
    /// host is not.
    #[test]
    fn leaves_out_only_the_sign_ins_callback() {
        let ingresses = vec![
            ingress("sign-in-page", "login.example.test", "sign-in-svc"),
            ingress("sign-in-callback", "shop.example.test", "sign-in-svc"),
            ingress("shop", "shop.example.test", "shop-svc"),
        ];
        let selected = |service: &Object| match service.name.as_str() {
            "sign-in-svc" => vec![workload("sign-in")],
            _ => vec![workload("shop")],
        };
        let signs_in = BTreeSet::from([workload("sign-in")]);
        let none = |_: &Object| Vec::new();
        let isolation = |_: &Object| Some(Isolation::Open);
        let admissions = |_: &Object| Vec::new();
        let workload_of = |_: &k8s_openapi::api::core::v1::Pod| None;
        let controller = |_: &Ingress| Controller {
            workload: Some(workload("controller")),
            entry_points: None,
            declared_by: None,
        };
        let declares = |_: &Object| None;
        let sources = Sources {
            ingresses: &ingresses,
            services: &[],
            pods: &[],
            daemon_sets: &[],
            nodes: &[],
            load_balancers: &[],
            workload_of: &workload_of,
            selected: &selected,
            isolation: &isolation,
            admissions: &admissions,
            sign_in: &none,
            controller: &controller,
            signs_in: &signs_in,
            declares: &declares,
        };
        let ways = read(&sources);
        let callbacks: Vec<&str> = ways
            .callbacks
            .iter()
            .map(|(o, _)| o.name.as_str())
            .collect();
        let hosts: BTreeSet<&str> = ways.rows.iter().map(|row| row.name.as_str()).collect();
        assert_eq!(
            (callbacks, hosts),
            (
                vec!["sign-in-callback"],
                BTreeSet::from(["login.example.test", "shop.example.test"])
            )
        );
    }
}
