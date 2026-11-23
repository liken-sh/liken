//! Reads a pod's last lines from the `pods/log` subresource of the
//! Kubernetes API. It needs no log store, so transect reads the tails
//! here when no `LogSource` exists, and when the log store's query fails.

use futures::future::join_all;
use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::jiff::Timestamp;
use kube::api::LogParams;
use kube::core::Status;
use kube::{Api, Client};

use super::READ_TIMEOUT;
use super::tail::{self, Read, Tail, Tails};

/// Reads the tail of each of `containers` in the pod `namespace/name`.
pub async fn read(client: &Client, namespace: &str, name: &str, containers: Vec<String>) -> Tails {
    let api: Api<Pod> = Api::namespaced(client.clone(), namespace);
    let reads = containers.iter().map(|container| {
        let params = LogParams {
            container: Some(container.clone()),
            tail_lines: Some(tail::TAIL_LINES),
            limit_bytes: Some(tail::LIMIT_BYTES),
            ..LogParams::default()
        };
        let api = api.clone();
        async move { tokio::time::timeout(READ_TIMEOUT, api.logs(name, &params)).await }
    });
    let results = join_all(reads).await;
    let mut tails = Tails {
        read_at: Timestamp::now(),
        ..Tails::default()
    };
    for (container, result) in containers.into_iter().zip(results) {
        let result = result.unwrap_or_else(|_| {
            let message = format!("no reply in {}s", READ_TIMEOUT.as_secs());
            Err(kube::Error::Api(Box::new(Status::failure(
                &message, "Timeout",
            ))))
        });
        let Some(read) = read_of(result) else {
            tails.forbidden = true;
            tails.containers.clear();
            break;
        };
        tails.containers.push(Tail { container, read });
    }
    tails
}

/// The `Read` for one container's result, or `None` when the API
/// returned Forbidden. RBAC cannot grant `pods/log` for one container
/// alone, so a Forbidden for one container applies to all of them, and
/// `read` stops there.
fn read_of(result: Result<String, kube::Error>) -> Option<Read> {
    match result {
        Ok(text) => Some(tail::lines(&text)),
        // When the error body is not a `Status`, kube sets the reason to
        // "Failed to parse error data" and keeps the HTTP code.
        // `is_forbidden` compares only the reason, so check the code too.
        Err(kube::Error::Api(status)) if status.is_forbidden() || status.code == 403 => None,
        Err(kube::Error::Api(status)) => Some(Read::Failed(status.message.clone())),
        Err(error) => Some(Read::Failed(error.to_string())),
    }
}

/// The containers whose logs the pod page reads: every app container,
/// then each init container that has started. The API returns an error
/// for an init container that has not started, because it has no log.
pub fn containers(pod: &Pod) -> Vec<String> {
    let Some(spec) = &pod.spec else {
        return Vec::new();
    };
    let statuses = pod.status.as_ref();
    let init_statuses = statuses.and_then(|s| s.init_container_statuses.as_ref());
    let started = |name: &str| {
        let status = init_statuses
            .into_iter()
            .flatten()
            .find(|status| status.name == name);
        let state = status.and_then(|status| status.state.as_ref());
        let last = status.and_then(|status| status.last_state.as_ref());
        state.is_some_and(|s| s.running.is_some() || s.terminated.is_some())
            || last.is_some_and(|s| s.terminated.is_some())
    };
    let app = spec.containers.iter().map(|c| c.name.clone());
    let init = spec.init_containers.iter().flatten();
    let init = init.filter(|c| started(&c.name)).map(|c| c.name.clone());
    app.chain(init).collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::logs::level;

    fn pod(init_statuses: &str) -> Pod {
        serde_yaml_ng::from_str(&format!(
            "metadata: {{namespace: shop, name: web-1}}\nspec: {{containers: [{{name: web}}, {{name: proxy}}], initContainers: [{{name: migrate}}, {{name: warm}}]}}\nstatus: {{initContainerStatuses: {init_statuses}}}"
        ))
        .unwrap()
    }

    fn status(name: &str, state: &str) -> String {
        format!(
            "{{name: {name}, image: x, imageID: '', ready: false, restartCount: 0, state: {state}}}"
        )
    }

    #[rstest]
    #[case::no_init_has_started("[]", &["web", "proxy"])]
    #[case::one_finished(
        &format!("[{}, {}]", status("migrate", "{terminated: {exitCode: 0}}"), status("warm", "{waiting: {}}")),
        &["web", "proxy", "migrate"]
    )]
    #[case::a_sidecar_running(
        &format!("[{}]", status("warm", "{running: {}}")),
        &["web", "proxy", "warm"]
    )]
    fn reads_every_container_that_has_run(#[case] init: &str, #[case] names: &[&str]) {
        assert_eq!(containers(&pod(init)), names);
    }

    fn refused(code: u16, message: &str) -> Result<String, kube::Error> {
        let status = Status {
            code,
            ..Status::failure(message, "")
        };
        Err(kube::Error::Api(Box::new(status)))
    }

    #[rstest]
    #[case::lines(Ok("ready\n".into()), Some(Read::Lines(vec![tail::Line { text: "ready".into(), level: level::Level::Plain, time: None, tags: Vec::new() }])))]
    #[case::forbidden(refused(403, "forbidden"), None)]
    #[case::waiting(
        refused(400, "container is waiting to start"),
        Some(Read::Failed("container is waiting to start".into()))
    )]
    fn a_read_reflects_the_apis_result(
        #[case] result: Result<String, kube::Error>,
        #[case] read: Option<Read>,
    ) {
        assert_eq!(read_of(result), read);
    }
}
