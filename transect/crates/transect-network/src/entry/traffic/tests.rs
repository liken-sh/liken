use rstest::rstest;

use super::*;
use crate::entry::controller::fixtures::controller;

fn sample(backend: &str, value: f64) -> Sample {
    Sample {
        labels: HashMap::from([("backend".to_string(), backend.to_string())]),
        value,
    }
}

fn measured(backends: HashMap<String, Figures>) -> Measured {
    let spec = "{controller: example.io/proxy, image: example/proxy, entry: {backend: {label: backend, name: '{namespace}-{service}-{port}'}, requests: r, latency: l, errors: e, window: 5m}}";
    Measured {
        controller: "proxy".into(),
        queries: controller(spec).entry.unwrap(),
        backends,
    }
}

fn figures(requests: f64, p95: Option<f64>, errors: f64) -> Figures {
    Figures {
        requests,
        p95,
        errors,
    }
}

#[rstest]
fn reads_each_backends_figures_from_the_query_results() {
    let found = measure(
        "backend",
        &[sample("shop-web-80", 3.0), sample("shop-api-80", 1.0)],
        &[sample("shop-web-80", 0.12), sample("shop-api-80", f64::NAN)],
        &[sample("shop-web-80", 0.03)],
    );
    assert_eq!(
        (found.get("shop-web-80"), found.get("shop-api-80")),
        (
            Some(&figures(3.0, Some(0.12), 0.03)),
            Some(&figures(1.0, None, 0.0))
        )
    );
}

#[rstest]
fn reads_the_backends_an_ingress_routes_to() {
    let ingress: Ingress = serde_yaml::from_str(
        "{metadata: {namespace: shop, name: web}, spec: {defaultBackend: {service: {name: web, port: {number: 80}}}, rules: [{host: a.example, http: {paths: [{path: /, pathType: Prefix, backend: {service: {name: web, port: {number: 80}}}}, {path: /api, pathType: Prefix, backend: {service: {name: api, port: {name: http}}}}]}}]}}",
    )
    .unwrap();
    assert_eq!(
        backends(&ingress),
        vec![("web".into(), "80".into()), ("api".into(), "http".into())]
    );
}

fn routes(backends: &[(&str, &str)]) -> Vec<(String, String)> {
    let backends = backends.iter().map(|(s, p)| (s.to_string(), p.to_string()));
    backends.collect()
}

fn shop() -> Measured {
    measured(HashMap::from([
        ("shop-web-80".to_string(), figures(3.0, Some(0.12), 0.03)),
        ("shop-api-http".to_string(), figures(1.0, Some(2.4), 0.0)),
    ]))
}

/// Each line's backend label and figures text. The label is `None`
/// when the traffic has a single backend.
fn lines(traffic: &Traffic) -> Vec<(Option<String>, String)> {
    let lines = traffic.lines("shop", false).into_iter();
    lines
        .map(|l| (l.backend.map(|b| b.text), l.figures))
        .collect()
}

#[rstest]
#[case::one_backend(
    &[("web", "80")],
    vec![(None, "3.0 requests/s · p95 120 ms · 1.0% 5xx, over 5m".to_string())]
)]
#[case::two_backends(
    &[("web", "80"), ("api", "http")],
    vec![
        (Some("web:80".to_string()), "3.0 requests/s · p95 120 ms · 1.0% 5xx, over 5m".to_string()),
        (Some("api:http".to_string()), "1.0 requests/s · p95 2.4 s · no 5xx, over 5m".to_string()),
    ]
)]
#[case::a_backend_with_no_samples(
    &[("web", "80"), ("blog", "80")],
    vec![
        (Some("web:80".to_string()), "3.0 requests/s · p95 120 ms · 1.0% 5xx, over 5m".to_string()),
        (Some("blog:80".to_string()), "no requests counted over 5m".to_string()),
    ]
)]
fn gives_each_backend_its_own_line(
    #[case] backends: &[(&str, &str)],
    #[case] expected: Vec<(Option<String>, String)>,
) {
    let measured = [shop()];
    let results = TrafficResults {
        measured: &measured,
        declared: true,
        unloaded: None,
    };
    assert_eq!(lines(&of("shop", &routes(backends), results)), expected);
}

#[rstest]
#[case::no_controller_declares_it(false, vec![shop()], &[("web", "80")], "An IngressController with entry")]
#[case::no_results_yet(true, vec![], &[("web", "80")], "have not returned results")]
#[case::no_result_names_it(true, vec![shop()], &[("blog", "80")], "name none of its backends")]
fn explains_why_it_has_no_figures(
    #[case] declared: bool,
    #[case] measured: Vec<Measured>,
    #[case] backends: &[(&str, &str)],
    #[case] says: &str,
) {
    let results = TrafficResults {
        measured: &measured,
        declared,
        unloaded: None,
    };
    let traffic = of("shop", &routes(backends), results);
    let note = traffic.note().unwrap().text;
    assert!(note.contains(says), "{note:?} lacks {says:?}");
}

#[rstest]
#[case::idle(figures(0.0, None, 0.0), "no requests over 5m")]
#[case::quiet(figures(0.004, None, 0.0), "under 0.01 requests/s · no 5xx, over 5m")]
#[case::busy(
    figures(42.0, Some(0.0451), 0.0001),
    "42 requests/s · p95 45 ms · under 0.1% 5xx, over 5m"
)]
#[case::failing(
    figures(2.0, Some(0.2), 0.5),
    "2.0 requests/s · p95 200 ms · 25% 5xx, over 5m"
)]
fn formats_figures_on_one_line(#[case] figures: Figures, #[case] expected: &str) {
    assert_eq!(figures.line("5m"), expected);
}

#[rstest]
fn notes_an_ingress_controller_that_did_not_load() {
    let results = TrafficResults {
        measured: &[],
        declared: false,
        unloaded: Some("proxy"),
    };
    let note = of("shop", &routes(&[("web", "80")]), results)
        .note()
        .unwrap();
    assert_eq!(
        (note.text.as_str(), note.see_resources),
        ("No traffic: the IngressController proxy did not load", true)
    );
}
