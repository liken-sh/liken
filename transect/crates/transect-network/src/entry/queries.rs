//! The `entry` section of an `IngressController`: the Prometheus
//! queries for each backend's request rate, 95th-percentile latency,
//! and 5xx rate, and how to find a backend in their results.  Each
//! controller names its metrics differently, and transect has none of
//! those names built in, so the resource declares them.

use serde::Deserialize;

/// The placeholders a backend's name may contain.
const BACKEND_PLACEHOLDERS: [&str; 3] = ["{namespace}", "{service}", "{port}"];

/// The `entry` section, validated.
#[derive(Clone, Debug, PartialEq)]
pub struct EntryQueries {
    /// The label that names the backend in each result.
    pub label: String,
    /// How the controller names an `Ingress`'s backend in that label,
    /// such as `{namespace}-{service}-{port}@kubernetes`.
    pub backend: String,
    /// Requests per second, by backend.
    pub requests: String,
    /// The 95th-percentile latency in seconds, by backend.
    pub latency: String,
    /// Responses with a 5xx status per second, by backend.
    pub errors: String,
    /// The time range of the queries' rates, as text for the page,
    /// such as `5m`.
    pub window: String,
}

/// The section as it is in the YAML.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Written {
    backend: Backend,
    requests: String,
    latency: String,
    errors: String,
    window: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Backend {
    label: String,
    name: String,
}

pub fn read(written: Written) -> Result<EntryQueries, String> {
    let name = &written.backend.name;
    if !name.contains("{service}") {
        return Err(format!(
            "entry.backend.name {name:?} has no {{service}}, so every backend would get the same name"
        ));
    }
    let mut rest = name.clone();
    for placeholder in BACKEND_PLACEHOLDERS {
        rest = rest.replace(placeholder, "");
    }
    if rest.contains('{') {
        return Err(format!(
            "entry.backend.name {name:?} has a placeholder other than {{namespace}}, {{service}}, and {{port}}"
        ));
    }
    Ok(EntryQueries {
        label: written.backend.label,
        backend: written.backend.name,
        requests: written.requests,
        latency: written.latency,
        errors: written.errors,
        window: written.window,
    })
}

impl EntryQueries {
    /// The value of `label` for the backend `service` on `port` in
    /// `namespace`.
    pub fn backend(&self, namespace: &str, service: &str, port: &str) -> String {
        self.backend
            .replace("{namespace}", namespace)
            .replace("{service}", service)
            .replace("{port}", port)
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::controller::fixtures::controller;
    use super::super::controller::parse;
    use transect_resources::resources::DocId;

    const SPEC: &str = "{controller: example.io/proxy, image: example/proxy, entry: {backend: {label: backend, name: '{namespace}-{service}-{port}'}, requests: r, latency: l, errors: e, window: 5m}}";

    #[rstest]
    fn names_a_backend_by_its_template() {
        let queries = controller(SPEC).entry.unwrap();
        assert_eq!(queries.backend("shop", "web", "80"), "shop-web-80");
    }

    #[rstest]
    #[case::no_service("'{namespace}-{port}'", "no {service}")]
    #[case::another_placeholder("'{namespace}-{service}-{host}'", "other than")]
    fn rejects_an_invalid_backend_name_with_the_reason(#[case] name: &str, #[case] message: &str) {
        let spec = SPEC.replace("'{namespace}-{service}-{port}'", name);
        let text = format!(
            "metadata: {{name: proxy}}
spec: {spec}
"
        );
        let error = parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap()).unwrap_err();
        assert!(error.contains(message), "{error:?} lacks {message:?}");
    }
}
