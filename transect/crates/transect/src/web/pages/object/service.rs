//! The sections of an object page for a `Service` (its type and ports,
//! selector, and endpoints) and for an `Ingress` (its hosts, backends,
//! class, TLS, controller, and traffic). The routes between them are
//! edges, which every object page lists.

use kube::ResourceExt;

use super::super::super::snapshot::Snapshot;
use super::refer_object;
use transect_graph::edges::Object;
use transect_map::objects::{Section, Side, words};
use transect_network::entry::{Endpoints, IngressBox, ServiceBox, Tls};

pub fn service(snapshot: &Snapshot, object: &Object) -> Vec<Section> {
    let here = object.namespace.as_deref();
    let Some(service) = snapshot.services.iter().find(|service| {
        service.namespace().as_deref() == here && service.name_any() == object.name
    }) else {
        return Vec::new();
    };
    let endpoints = Endpoints::build(&snapshot.endpoint_slices);
    let shown = ServiceBox::build(service, &endpoints);
    let selector = service
        .spec
        .as_ref()
        .and_then(|spec| spec.selector.as_ref());
    let selector = selector.into_iter().flatten();
    let selector: Vec<String> = selector
        .map(|(key, value)| format!("{key}={value}"))
        .collect();
    vec![
        Section::new("Service", Side::Own)
            .text("type", Some(shown.kind_line()))
            .literals("selects pods labeled", selector)
            .text("endpoints", Some(shown.endpoints_line())),
    ]
}

pub fn ingress(snapshot: &Snapshot, object: &Object) -> Vec<Section> {
    let here = object.namespace.as_deref();
    let Some(ingress) = snapshot.ingresses.iter().find(|ingress| {
        ingress.namespace().as_deref() == here && ingress.name_any() == object.name
    }) else {
        return Vec::new();
    };
    let issuer = snapshot.issuers.get(object);
    let issuer = issuer.map(|issuer| transect_network::entry::issued_by(issuer, &snapshot.plurals));
    let shown = IngressBox::build(ingress, issuer)
        .with_traffic(snapshot.traffic())
        .served_by(&snapshot.serving(), &snapshot.plurals);
    let hosts = shown.host_lines().into_iter().map(words).collect();
    let backends = shown.backends.iter().map(|(service, port)| {
        let target = Object::new("", "Service", here, service);
        let mut reference = refer_object(snapshot, &target, here);
        reference.text = format!("{service}:{port}");
        reference
    });
    let class = shown.class.as_ref().map(|class| {
        let target = Object::new("networking.k8s.io", "IngressClass", None, class);
        refer_object(snapshot, &target, None)
    });
    let tls = match &shown.tls {
        Tls::None => vec![words("none: it serves plain HTTP")],
        Tls::Secret => vec![words("from a Secret")],
        Tls::Issuer(issuer) => vec![words("from"), issuer.clone()],
    };
    vec![
        Section::new("Ingress", Side::Own)
            .fact("hosts", hosts)
            .fact("backends", backends.collect())
            .fact("class", class.into_iter().collect())
            .fact("TLS", tls)
            .fact("served by", served_by(&shown)),
        traffic(&shown),
    ]
}

/// A link to the workload that runs the `Ingress`'s controller. Its text
/// ends in "?" when the controller was guessed.
fn served_by(shown: &IngressBox) -> Vec<transect_graph::links::Reference> {
    let Some(controller) = &shown.controller else {
        return Vec::new();
    };
    let mut controller = controller.clone();
    if shown.controller_guessed {
        controller.text.push('?');
    }
    vec![controller]
}

/// The Traffic section: one "requests to" fact for each backend, with
/// the backend's link and its figures. When there are no figures, the
/// section shows the traffic's note, which says why.
fn traffic(shown: &IngressBox) -> Section {
    let lines = shown.traffic.lines(&shown.namespace, true).into_iter();
    let section = lines.fold(Section::new("Traffic", Side::Out), |section, line| {
        let values = line.backend.into_iter().chain([words(line.figures)]);
        section.fact("requests to", values.collect())
    });
    match shown.traffic.note() {
        Some(note) => section.or_off(note),
        None => section,
    }
}
