//! The background task that reads the labels of the images that the
//! cluster runs, from their registries.
//!
//! The task wakes when the cluster changes, once a minute to retry
//! failed reads whose wait has ended, and when a read completes.  On a
//! cluster change or a tick, it lists the images that pods run and
//! starts a read for each image whose read is due, at most `CONCURRENT`
//! at a time.
//!
//! When a read completes, the task publishes the labels read so far and
//! marks `changed`, so the pages render again with them.

use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use k8s_openapi::api::core::v1::{Node, Pod};
use tokio::sync::{Notify, Semaphore, watch};
use tokio::time::MissedTickBehavior;

use super::state::State;
use super::{Known, Platform, Registries, Want, parse};
use transect_cluster::cluster::Cluster;
use transect_graph::pods::finished;

/// The most reads that run at once.  A registry that also serves the
/// cluster's image pulls should not receive hundreds of requests when
/// transect starts.
const CONCURRENT: usize = 4;
/// How often the task wakes to retry failed reads whose wait has ended,
/// when the cluster has not changed.
const RETRY_EVERY: Duration = Duration::from_secs(60);
/// How long the task waits after a read completes before it publishes,
/// so that reads that complete at about the same time share a publish.
const GATHER: Duration = Duration::from_millis(250);

/// Starts the task that reads the labels of every image that `cluster`
/// runs, from `registries`.  It marks `changed` after reads complete,
/// and returns the receiver of the labels read so far.
pub fn watch(
    cluster: Cluster,
    registries: Registries,
    changed: watch::Sender<()>,
) -> watch::Receiver<Arc<Known>> {
    let (publish, latest) = watch::channel(Arc::new(Known::default()));
    let reader = Reader {
        state: Arc::new(Mutex::new(State::default())),
        registries: Arc::new(registries),
        permits: Arc::new(Semaphore::new(CONCURRENT)),
        completed: Arc::new(Notify::new()),
    };
    let mut changes = changed.subscribe();
    tokio::spawn(async move {
        let mut ticks = tokio::time::interval(RETRY_EVERY);
        ticks.set_missed_tick_behavior(MissedTickBehavior::Delay);
        let mut mirrors = reader.registries.mirrors();
        loop {
            tokio::select! {
                result = changes.changed() => if result.is_err() { return },
                _ = ticks.tick() => {}
                _ = reader.completed.notified() => {
                    tokio::time::sleep(GATHER).await;
                    let known = lock(&reader.state).known();
                    let known = known.with_mirrors(reader.registries.mirrors());
                    publish.send_replace(Arc::new(known));
                    // Marking `changed` also wakes this task's own
                    // receiver.  Clear that mark, unless the cluster
                    // changed during the wait, so that the task lists
                    // the pods again only when the pods changed.
                    let pending = changes.has_changed().unwrap_or(false);
                    changed.send_replace(());
                    if !pending {
                        changes.borrow_and_update();
                    }
                    continue;
                }
            }
            // A host whose mirror changed is read from another address,
            // so a failure recorded for the old address says nothing
            // about the new one.
            let now = reader.registries.mirrors();
            if now != mirrors {
                let mut state = lock(&reader.state);
                let hosts = now.keys().chain(mirrors.keys());
                for host in hosts.filter(|host| now.get(*host) != mirrors.get(*host)) {
                    state.forget(host);
                }
                drop(state);
                mirrors = now;
            }
            let wanted = wanted(&cluster.pods(), &cluster.nodes());
            let mut state = lock(&reader.state);
            state.keep_only(wanted.iter().map(|want| want.image.digest.as_str()));
            let due = state.due(wanted, Instant::now());
            drop(state);
            for want in due {
                tokio::spawn(reader.clone().read(want));
            }
        }
    });
    latest
}

/// The parts of the task that every read uses.
#[derive(Clone)]
struct Reader {
    state: Arc<Mutex<State>>,
    registries: Arc<Registries>,
    permits: Arc<Semaphore>,
    completed: Arc<Notify>,
}

impl Reader {
    /// Reads `want` once a permit is free, records the result, and wakes
    /// the task to publish the labels.
    async fn read(self, want: Want) {
        let Ok(_permit) = self.permits.acquire().await else {
            return;
        };
        if lock(&self.state).drop_if_held(&want, Instant::now()) {
            return;
        }
        let result = self.registries.provenance(&want).await;
        let (image, registry) = (&want.image.digest, &want.image.registry);
        match &result {
            Ok(read) => tracing::debug!(
                image,
                registry,
                source = read.source.is_some(),
                revision = read.revision.is_some(),
                "Read the labels of an image"
            ),
            Err(failure) => tracing::debug!(
                image,
                registry,
                reason = failure.note(),
                "The registry returned no labels for an image"
            ),
        }
        let mut state = lock(&self.state);
        if let Some(failure) = state.land(&want, result, Instant::now()) {
            tracing::warn!(
                registry,
                reason = failure.note(),
                "The registry returned no image labels"
            );
        }
        drop(state);
        self.completed.notify_one();
    }
}

/// Locks `state` even when another thread panicked while holding it, so
/// one failed read does not stop the others.
fn lock(state: &Mutex<State>) -> std::sync::MutexGuard<'_, State> {
    state
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

/// Every image that a pod that has not finished runs, by the digest in
/// the pod's status, with the platform of the node it runs on.  A pod
/// on a node that is not in `nodes`, or with no node, gets the fallback
/// platform.
pub fn wanted(pods: &[Arc<Pod>], nodes: &[Arc<Node>]) -> Vec<Want> {
    let platforms: HashMap<&str, Platform> = nodes
        .iter()
        .filter_map(|node| {
            let info = node.status.as_ref()?.node_info.as_ref()?;
            let platform = Platform::new(&info.operating_system, &info.architecture);
            Some((node.metadata.name.as_deref()?, platform))
        })
        .collect();
    let platform = |node: Option<&str>| {
        let found = node.and_then(|node| platforms.get(node));
        found.cloned().unwrap_or_else(Platform::fallback)
    };
    let mut wanted = Vec::new();
    for pod in pods.iter().filter(|pod| !finished(pod)) {
        let node = pod.spec.as_ref().and_then(|spec| spec.node_name.as_deref());
        let Some(status) = &pod.status else { continue };
        let statuses = status
            .container_statuses
            .iter()
            .chain(&status.init_container_statuses);
        for status in statuses.flatten() {
            if let Some(image) = parse(&status.image_id) {
                wanted.push(Want {
                    image,
                    platform: platform(node),
                });
            }
        }
    }
    wanted
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::ImageRef;
    use super::super::reference::fixtures::digest;
    use super::*;

    fn pod(name: &str, spec: &str, status: &str) -> Pod {
        serde_yaml::from_str(&format!(
            "metadata: {{namespace: shop, name: {name}}}\nspec: {spec}\nstatus: {status}"
        ))
        .unwrap()
    }

    fn node(name: &str, architecture: &str) -> Arc<Node> {
        Arc::new(
            serde_yaml::from_str(&format!(
                "metadata: {{name: {name}}}\nstatus: {{nodeInfo: {{operatingSystem: linux, architecture: {architecture}, \
                 machineID: '', systemUUID: '', bootID: '', kernelVersion: '', osImage: '', containerRuntimeVersion: '', \
                 kubeletVersion: '', kubeProxyVersion: ''}}}}"
            ))
            .unwrap(),
        )
    }

    fn running(name: &str, node: &str, image_id: &str) -> Arc<Pod> {
        Arc::new(pod(
            name,
            &format!("{{nodeName: {node}, containers: [{{name: app}}]}}"),
            &format!(
                "{{phase: Running, containerStatuses: [{{name: app, ready: true, restartCount: 0, image: app, imageID: '{image_id}'}}]}}"
            ),
        ))
    }

    #[rstest]
    fn wants_each_running_image_for_its_machines_platform() {
        let pods = [
            running(
                "web-1",
                "alpha",
                &format!("registry.example/team/app@{}", digest('a')),
            ),
            running(
                "web-2",
                "beta",
                &format!("registry.example/team/app@{}", digest('b')),
            ),
            running(
                "web-3",
                "gone",
                &format!("registry.example/team/app@{}", digest('c')),
            ),
            running("local", "alpha", &digest('d')),
        ];
        let nodes = [node("alpha", "amd64"), node("beta", "arm64")];
        let found: Vec<(String, String)> = wanted(&pods, &nodes)
            .into_iter()
            .map(|want| (want.image.digest, want.platform.to_string()))
            .collect();
        assert_eq!(
            found,
            vec![
                (digest('a'), "linux/amd64".into()),
                (digest('b'), "linux/arm64".into()),
                (digest('c'), "linux/amd64".into()),
            ]
        );
    }

    #[rstest]
    fn wants_nothing_from_a_finished_pod() {
        let mut done = (*running(
            "job-1",
            "alpha",
            &format!("registry.example/team/app@{}", digest('a')),
        ))
        .clone();
        done.status.as_mut().unwrap().phase = Some("Succeeded".into());
        let found: Vec<ImageRef> = wanted(&[Arc::new(done)], &[])
            .into_iter()
            .map(|w| w.image)
            .collect();
        assert_eq!(found, vec![]);
    }
}
