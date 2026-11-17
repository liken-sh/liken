//! `transect`: a live, read-only map of a Kubernetes cluster.

mod web;

use std::net::SocketAddr;
use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

use anyhow::Context;
use clap::Parser;
use kube::config::{KubeConfigOptions, Kubeconfig};

use transect_cluster::cluster::{self, Cluster};
use transect_cluster::connect;
use transect_graph::upstream;
use transect_ground::{devices, loss};
use transect_map::objects;
use transect_network::{addresses, entry};
use transect_observe::{alerts, health, logs, metrics, usage};
use transect_resources::resources::{self, Field};
use transect_supply::images;

use web::loaded::Loaded;

/// How long the first full list of the cluster may take before
/// `transect` gives up.  The watches retry an unreachable API server
/// forever, so without a limit a wrong kubeconfig would look like a
/// slow start.
const SYNC_TIMEOUT: Duration = Duration::from_secs(30);

/// How often to read the firing alerts.  Prometheus sends an alert to
/// Alertmanager on its evaluation interval, so reading Alertmanager
/// more often finds nothing new.
const ALERTS_INTERVAL: Duration = Duration::from_secs(30);

/// How often to read where Prometheus sends alerts, which changes only
/// when an Alertmanager's pod moves.
const PROMETHEUS_POD_INTERVAL: Duration = Duration::from_secs(60);

/// How often the ingress controllers' traffic is read.  Their queries
/// take rates over minutes, so a reading a minute old gives the same
/// numbers.
const FRONT_DOOR_INTERVAL: Duration = Duration::from_secs(60);

/// How often the bytes on each volume are read.  The kubelet measures a
/// volume once a minute, so a reading more often finds nothing new.
const VOLUMES_USED_INTERVAL: Duration = Duration::from_secs(60);

/// Every flag also reads an environment variable, `TRANSECT_` and the
/// flag's name in capitals, so a Deployment sets them in `env`.
#[derive(Parser)]
#[command(about = "A live, read-only map of a Kubernetes cluster")]
struct Args {
    /// The kubeconfig that transect reads.  Without this flag, transect
    /// uses $KUBECONFIG, ~/.kube/config, or the in-cluster
    /// ServiceAccount.
    #[arg(long, env = "TRANSECT_KUBECONFIG")]
    kubeconfig: Option<PathBuf>,

    /// The kubeconfig context that transect uses, in place of the
    /// kubeconfig's current context.
    #[arg(long, env = "TRANSECT_CONTEXT")]
    context: Option<String>,

    /// The Prometheus that transect queries, such as a port-forward to
    /// it, in place of the address that the MetricsSource resource
    /// gives.  The queries that transect sends come from the
    /// MetricsSource.
    #[arg(long, env = "TRANSECT_PROMETHEUS", value_name = "URL", value_parser = upstream::base_url)]
    prometheus: Option<String>,

    /// The Alertmanager that transect reads the firing alerts from, such
    /// as a port-forward to it, in place of the address that the alerts
    /// section of the MetricsSource gives.  transect reads only the
    /// alerts that are neither silenced nor inhibited, and writes
    /// nothing to Alertmanager.
    #[arg(long, env = "TRANSECT_ALERTMANAGER", value_name = "URL", value_parser = upstream::base_url)]
    alertmanager: Option<String>,

    /// The URL where a browser opens Alertmanager's interface, for the
    /// link on each alert, when that URL differs from --alertmanager.
    #[arg(long, env = "TRANSECT_ALERTMANAGER_URL_EXTERNAL", value_name = "URL")]
    alertmanager_url_external: Option<String>,

    /// How often transect reads each machine's usage from Prometheus,
    /// in seconds.
    #[arg(long, env = "TRANSECT_USAGE_INTERVAL", default_value_t = 30)]
    usage_interval: u64,

    /// A directory of transect's own resources as YAML files, such as
    /// the Layers resource, and of overlays.  transect reads the
    /// directory recursively, and reads it again when a file under it
    /// changes.
    #[arg(long, env = "TRANSECT_RESOURCES")]
    resources: Option<PathBuf>,

    /// The cluster's DNS domain, which ends each Service's longest name,
    /// as in db.shop.svc.cluster.local.
    #[arg(long, env = "TRANSECT_CLUSTER_DOMAIN", default_value = addresses::DEFAULT_DOMAIN)]
    cluster_domain: String,

    /// A Docker config.json whose auths entries contain the credentials
    /// for registries that refuse anonymous requests.  transect first
    /// reads an image's labels anonymously, and sends a registry its
    /// credential only when the registry refuses the anonymous request.
    /// transect never reads a pod's imagePullSecrets.
    #[arg(long, env = "TRANSECT_REGISTRY_AUTH", value_name = "PATH")]
    registry_auth: Option<PathBuf>,

    /// Read a registry host's image labels from a mirror, written as
    /// <host>=<mirror>.  The mirror is an http or https URL, or
    /// api-server for the registry that the API server's address also
    /// serves, such as the embedded registry of k3s, which transect
    /// reads with its credentials for the API.  Use this flag for a host
    /// that does not answer as a registry from where transect runs: a
    /// host whose images the machines pull from a mirror, or a host
    /// whose images the machines already hold and no registry serves.
    /// Repeat the flag for several hosts, or separate the hosts with
    /// commas.  The flag takes precedence over a RegistryMirror
    /// resource.
    #[arg(long, env = "TRANSECT_REGISTRY_MIRROR", value_name = "HOST=MIRROR", value_parser = images::mirror::flag, value_delimiter = ',')]
    registry_mirror: Vec<(String, images::mirror::Mirror)>,

    /// The base URL of the log store, such as a port-forward to it, in
    /// place of the address that its LogSource resource gives.  transect
    /// sends the store no credentials.
    #[arg(long, env = "TRANSECT_LOG_SOURCE_URL", value_name = "URL", value_parser = upstream::base_url)]
    log_source_url: Option<String>,

    /// Read the intent annotations on Secrets themselves, through a
    /// watch of their metadata.  Kubernetes has no permission for a
    /// Secret's metadata alone, so use this flag only with an identity
    /// that may already read Secrets, such as a workstation's admin
    /// kubeconfig.  Without this flag, transect reads a Secret's intent
    /// from the object that writes the Secret, which the writesSecret
    /// field of its kind knowledge names.  With or without this flag,
    /// transect never reads a Secret's data.
    #[arg(long, env = "TRANSECT_SECRET_ANNOTATIONS")]
    secret_annotations: bool,

    /// The address that transect serves the map on.  In a container, use
    /// 0.0.0.0:8080, because the kubelet's probes and the Service reach
    /// the pod's own address.
    #[arg(long, env = "TRANSECT_LISTEN", default_value = "127.0.0.1:8080")]
    listen: SocketAddr,

    /// For development: transect treats this machine as not ready from
    /// the moment it starts, so the map draws the loss that a real
    /// outage would cause.  The flag changes only transect's own copy of
    /// the Node.  Repeat the flag for several machines.  This flag
    /// exists only in debug builds.
    #[cfg(debug_assertions)]
    #[arg(
        long,
        env = "TRANSECT_PRETEND_NOT_READY",
        value_name = "MACHINE",
        value_delimiter = ','
    )]
    pretend_not_ready: Vec<String>,

    /// For development: transect serves the fixture cluster that the
    /// tests build, which never changes, and contacts nothing: no API
    /// server, no Prometheus, and no log store.  transect reads no other
    /// flag but --listen.  This flag exists only in debug builds.
    #[cfg(debug_assertions)]
    #[arg(long, env = "TRANSECT_FIXTURE")]
    fixture: bool,

    /// For development: transect opens at most this many streams on one
    /// connection to the API server, below the number that the server
    /// allows, so a test can watch more kinds than one connection has
    /// streams for.  This flag exists only in debug builds.
    #[cfg(debug_assertions)]
    #[arg(long, env = "TRANSECT_MOST_STREAMS", value_name = "STREAMS")]
    most_streams: Option<usize>,
}

impl Args {
    #[cfg(debug_assertions)]
    fn pretend(&self) -> cluster::Pretend {
        let machines = self.pretend_not_ready.clone();
        if !machines.is_empty() {
            tracing::warn!(
                ?machines,
                "drawing these machines as not ready, as --pretend-not-ready names them"
            );
        }
        cluster::Pretend::new(machines, k8s_openapi::jiff::Timestamp::now())
    }

    #[cfg(not(debug_assertions))]
    fn pretend(&self) -> cluster::Pretend {
        cluster::Pretend::default()
    }

    #[cfg(debug_assertions)]
    fn most_streams(&self) -> Option<usize> {
        self.most_streams
    }

    #[cfg(not(debug_assertions))]
    fn most_streams(&self) -> Option<usize> {
        None
    }
}

/// The threads that run the watches, the event streams, and HTTP, which
/// wait far more than they compute.
const WORKERS: usize = 2;

/// The most threads tokio starts for blocking work, which is mostly
/// rendering pages, and also resolving names.
const BLOCKING: usize = 4;

/// Every thread that allocates holds a cache of freed memory, so the
/// runtime starts a fixed few threads rather than tokio's default of a
/// worker per core and up to 512 blocking threads.
#[global_allocator]
static ALLOCATOR: tikv_jemallocator::Jemalloc = tikv_jemallocator::Jemalloc;

fn main() -> anyhow::Result<()> {
    tokio::runtime::Builder::new_multi_thread()
        .worker_threads(WORKERS)
        .max_blocking_threads(BLOCKING)
        .enable_all()
        .build()?
        .block_on(run())
}

async fn run() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env().unwrap_or_else(|_| "info".into()),
        )
        .init();
    let args = Args::parse();
    #[cfg(debug_assertions)]
    if args.fixture {
        tracing::warn!("serving the fixture cluster; nothing is read from the network");
        return web::serve_fixture(args.listen).await;
    }

    let client = connect::client(kube_config(&args).await?, args.most_streams()).await?;
    let cluster = Cluster::watch(
        client.clone(),
        args.cluster_domain.clone(),
        args.pretend(),
        args.secret_annotations,
    );
    tokio::time::timeout(SYNC_TIMEOUT, cluster.synced())
        .await
        .with_context(|| {
            format!("the cluster's first list did not finish in {SYNC_TIMEOUT:?}; is the API server reachable?")
        })??;
    tracing::info!(namespaces = cluster.namespaces().len(), "cluster synced");
    let reads = objects::reads::Reads::new(client.clone());

    health::recheck(cluster.change_signal());

    let resources: tokio::sync::watch::Receiver<Arc<Loaded>> = match &args.resources {
        Some(dir) => resources::watch(dir.clone(), cluster.change_signal()),
        None => tokio::sync::watch::channel(Default::default()).1,
    };
    // Each domain reads its own field of the resources.
    let field = Fields(&resources);

    // Every Prometheus poller queries the Prometheus that `--prometheus`
    // names, or else the one the `MetricsSource` gives at each round.
    let reaching = metrics::Reaching::new(
        args.prometheus.clone(),
        field.of(|loaded| loaded.metrics_source.clone()),
        args.cluster_domain.clone(),
    );
    let usage = usage::poll(
        reaching.clone(),
        Duration::from_secs(args.usage_interval.max(1)),
        cluster.change_signal(),
    );
    let alerts = alerts::poll(
        alerts::Sourcing {
            source: field.of(|loaded| loaded.metrics_source.clone()),
            url: args.alertmanager.clone(),
            ui: args.alertmanager_url_external.clone(),
            domain: args.cluster_domain.clone(),
        },
        ALERTS_INTERVAL,
        cluster.change_signal(),
    );
    let entry = entry::poll::poll(
        reaching.clone(),
        field.of(|loaded| loaded.ingress_controllers.clone()),
        FRONT_DOOR_INTERVAL,
        cluster.change_signal(),
    );
    let volumes_used = metrics::poll::poll(
        reaching.clone(),
        VOLUMES_USED_INTERVAL,
        cluster.change_signal(),
    );
    let prometheus_pod =
        loss::blind::poll::poll(reaching, PROMETHEUS_POD_INTERVAL, cluster.change_signal());
    cluster.ignore_changes_from(field.of(|loaded| loaded.kinds.clone()));
    cluster.watch_custom_kinds(
        client.clone(),
        field.of(|loaded| loaded.kinds.clone()),
        field.of(|loaded| {
            let matched = devices::device_matched(&loaded.devices);
            matched.map(|(group, kind, _)| (group, kind)).collect()
        }),
    );
    let sourcing = logs::Sourcing {
        source: field.of(|loaded| loaded.log_source.clone()),
        patterns: field.of(|loaded| loaded.log_patterns.clone()),
        url: args.log_source_url.clone(),
        domain: args.cluster_domain.clone(),
    };
    let searched = logs::search(sourcing.clone(), cluster.change_signal())?;
    let logs = logs::LogTails::new(client.clone(), sourcing, cluster.change_signal())?;

    let credentials = match &args.registry_auth {
        Some(path) => images::Credentials::load(path)?,
        None => images::Credentials::default(),
    };
    if credentials.ignored_helpers {
        tracing::warn!(
            "ignoring credsStore and credHelpers in the registry credentials: transect reads only auths entries"
        );
    }
    tracing::info!(with_credentials = ?credentials.registries(), "reading image labels from registries");
    let mirrors = args.registry_mirror.iter().cloned().collect();
    let registries = images::Registries::new(credentials)?.with_mirrors(
        mirrors,
        field.of(|loaded| loaded.mirrors.clone()),
        client,
    );
    let images = images::watch(cluster.clone(), registries, cluster.change_signal());

    let sources = web::Inputs {
        resources,
        usage,
        alerts,
        logs,
        searched,
        images,
        entry,
        volumes_used,
        prometheus_pod,
        reads,
    };
    web::serve(args.listen, cluster, sources).await
}

/// The resources each domain reads, from the watch of every resource.
struct Fields<'a>(&'a tokio::sync::watch::Receiver<Arc<Loaded>>);

impl Fields<'_> {
    fn of<T: 'static>(&self, read: fn(&Loaded) -> T) -> Field<T> {
        Field::new(self.0.clone(), read)
    }
}

async fn kube_config(args: &Args) -> anyhow::Result<kube::Config> {
    let options = KubeConfigOptions {
        context: args.context.clone(),
        ..Default::default()
    };
    let config = match (&args.kubeconfig, &args.context) {
        (Some(path), _) => {
            let kubeconfig = Kubeconfig::read_from(path)
                .with_context(|| format!("reading {}", path.display()))?;
            kube::Config::from_custom_kubeconfig(kubeconfig, &options).await?
        }
        (None, Some(_)) => kube::Config::from_kubeconfig(&options).await?,
        (None, None) => kube::Config::infer().await?,
    };
    Ok(config)
}

#[cfg(test)]
mod tests {
    use clap::CommandFactory;

    use super::Args;

    #[test]
    fn every_flag_reads_its_environment_variable() {
        let command = Args::command();
        let mismatched: Vec<_> = command
            .get_arguments()
            .filter(|arg| arg.get_long().is_some())
            .map(|arg| {
                let long = arg.get_long().unwrap();
                let expected = format!("TRANSECT_{}", long.replace('-', "_").to_uppercase());
                let env = arg.get_env().map(|env| env.to_string_lossy().into_owned());
                (long, env, expected)
            })
            .filter(|(_, env, expected)| env.as_deref() != Some(expected.as_str()))
            .collect();
        assert_eq!(mismatched, []);
    }
}
