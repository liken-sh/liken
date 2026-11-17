//! What the access lens reads from one reading of the cluster.  The
//! inputs of the RBAC reduction come with a fingerprint of them, so a
//! model reduces RBAC again only when a role, a binding, a
//! `ServiceAccount`, or what a pod runs as or mounts changed.  The ways
//! in read the controllers and the sign-ins of the Ingresses.

use std::collections::{BTreeSet, HashMap};
use std::sync::Arc;

use k8s_openapi::api::core::v1::Pod;
use std::hash::{Hash, Hasher};

use k8s_openapi::api::networking::v1::Ingress;
use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;

use super::Snapshot;
use transect_access::access::ways::Controller;
use transect_access::access::{Account, Applier, Inputs, Running};
use transect_cluster::ownership::runs_image;
use transect_graph::edges::{Kind, Object};
use transect_network::entry::Serving;
use transect_network::entry::serving::Served;
use transect_resources::knowledge;

impl Snapshot {
    /// What the access reduction reads, as of this reading.
    pub fn access_inputs(&self) -> Inputs<'_> {
        let mut gitops = Vec::new();
        let mut appliers = Vec::new();
        for (group, kind) in self.kinds.kinds() {
            let Some(knowledge) = self.kinds.of(group, kind) else {
                continue;
            };
            let Some(fields) = &knowledge.gitops else {
                continue;
            };
            if fields.applies.is_none() && fields.repository.is_empty() {
                continue;
            }
            gitops.push((group.clone(), knowledge.plural.clone()));
            if fields.applies.is_some() {
                let writers = self.grants.all_writers(group, &knowledge.plural);
                let mut controller: Vec<Account> = writers
                    .into_iter()
                    .map(|(namespace, name)| Account::service_account(namespace, name))
                    .collect();
                // A tool whose role grants every kind of its group lists
                // no writer of the kind's status, so its controller is
                // the account of each pod that runs the image its
                // knowledge names, as controller attribution finds it.
                if controller.is_empty()
                    && let Some(image) = &knowledge.controller_image
                {
                    controller = running_image(&self.pods, image);
                }
                let account = fields.applies.as_ref().and_then(|a| a.account.as_ref());
                for found in self.gitops.found_of(group, kind) {
                    let namespace = found.object.namespace.as_deref().unwrap_or_default();
                    // Without a name, the applier applies as the
                    // field's default, or else as its controller.
                    let unset = match account.and_then(|a| a.default.as_deref()) {
                        Some(name) => vec![Account::service_account(namespace, name)],
                        None => controller.clone(),
                    };
                    let (accounts, named) = match &found.account {
                        Some(name) => (vec![Account::service_account(namespace, name)], true),
                        None => (controller.clone(), false),
                    };
                    let source = self.gitops.source_of(found).and_then(|source| {
                        let key = (source.group.clone(), source.kind.clone());
                        let plural = self.plurals.get(&key)?.clone();
                        Some((source, plural))
                    });
                    appliers.push(Applier {
                        object: found.object.clone(),
                        plural: knowledge.plural.clone(),
                        fallback: if named { unset } else { Vec::new() },
                        accounts,
                        named,
                        field: account.map(|a| a.field.clone()),
                        source,
                    });
                }
            }
        }
        gitops.sort();
        appliers.sort();
        let accounts = self.paged.service_accounts.iter();
        let service_accounts = accounts.filter_map(|account| {
            let meta = &account.metadata;
            Some((meta.namespace.clone()?, meta.name.clone()?))
        });
        let mut service_accounts: Vec<(String, String)> = service_accounts.collect();
        service_accounts.sort();
        // The stores give their objects in no fixed order, so each list
        // is sorted, and two readings of one cluster fingerprint alike.
        let mut pods: Vec<Running> = self
            .pods
            .iter()
            .filter_map(|pod| Running::read(pod))
            .collect();
        pods.sort();
        Inputs {
            rbac: &self.paged.rbac,
            service_accounts,
            pods,
            gitops,
            appliers,
        }
    }
}

/// The ServiceAccounts of the pods that run `image` and have not
/// finished, each once.
fn running_image(pods: &[Arc<Pod>], image: &str) -> Vec<Account> {
    let running = pods
        .iter()
        .filter(|pod| !transect_graph::pods::finished(pod));
    let found = running.filter_map(|pod| {
        let spec = pod.spec.as_ref()?;
        let runs = spec.containers.iter().any(|c| {
            c.image
                .as_deref()
                .is_some_and(|found| runs_image(found, image))
        });
        let account = spec.service_account_name.as_deref().unwrap_or("default");
        let namespace = pod.metadata.namespace.as_deref()?;
        runs.then(|| Account::service_account(namespace, account))
    });
    let found: BTreeSet<Account> = found.collect();
    found.into_iter().collect()
}

/// A fingerprint of `inputs`: each role's and binding's name and
/// version, and everything else whole.
pub fn fingerprint(inputs: &Inputs) -> u64 {
    let mut hasher = std::collections::hash_map::DefaultHasher::new();
    let rbac = inputs.rbac;
    let mut metas: Vec<&ObjectMeta> = rbac
        .cluster_roles
        .iter()
        .map(|o| &o.metadata)
        .chain(rbac.roles.iter().map(|o| &o.metadata))
        .chain(rbac.cluster_role_bindings.iter().map(|o| &o.metadata))
        .chain(rbac.role_bindings.iter().map(|o| &o.metadata))
        .collect();
    let key = |meta: &&ObjectMeta| (meta.namespace.clone(), meta.name.clone(), meta.uid.clone());
    metas.sort_by_key(key);
    for meta in metas {
        (
            &meta.namespace,
            &meta.name,
            &meta.uid,
            &meta.resource_version,
        )
            .hash(&mut hasher);
    }
    inputs.service_accounts.hash(&mut hasher);
    inputs.pods.hash(&mut hasher);
    inputs.gitops.hash(&mut hasher);
    inputs.appliers.hash(&mut hasher);
    hasher.finish()
}

/// The annotation that marks the `IngressClass` that serves an
/// `Ingress` that names no class.
const DEFAULT_CLASS: &str = "ingressclass.kubernetes.io/is-default-class";

impl Snapshot {
    /// The objects each `Ingress`'s routes sign in through: each object
    /// that an annotation on the `Ingress` names and whose kind
    /// knowledge draws an `authenticates through` edge.
    pub fn route_sign_ins(&self) -> HashMap<Object, Vec<Object>> {
        let ingresses: Vec<(Object, &ObjectMeta)> = self
            .ingresses
            .iter()
            .map(|ingress| {
                let meta = &ingress.metadata;
                let name = meta.name.as_deref().unwrap_or_default();
                let object = Object::new(
                    "networking.k8s.io",
                    "Ingress",
                    meta.namespace.as_deref(),
                    name,
                );
                (object, meta)
            })
            .collect();
        let referrals = knowledge::referrals(&self.kinds, &self.custom_objects(), &ingresses);
        let mut found: HashMap<Object, Vec<Object>> = HashMap::new();
        for referral in referrals {
            let relations = self.relations.get(&referral.target).into_iter().flatten();
            let mut relations = relations;
            if relations.any(|relation| relation.edge == Some(Kind::AuthenticatesThrough)) {
                found
                    .entry(referral.referrer)
                    .or_default()
                    .push(referral.target);
            }
        }
        found
    }

    /// The controller that serves `ingress`, by its class, the class's
    /// annotation, or the default class: the workload `serving` finds,
    /// and the entry points its `IngressController` declares.
    pub fn ingress_controller(&self, serving: &Serving, ingress: &Ingress) -> Controller<'_> {
        let annotations = ingress.metadata.annotations.as_ref();
        let named = ingress
            .spec
            .as_ref()
            .and_then(|spec| spec.ingress_class_name.clone());
        let named = named.or_else(|| annotations?.get("kubernetes.io/ingress.class").cloned());
        let classes = self.paged.ingress_classes.iter();
        let default = || {
            let mut defaults = classes.filter(|class| {
                let annotations = class.metadata.annotations.as_ref();
                annotations
                    .and_then(|a| a.get(DEFAULT_CLASS))
                    .map(String::as_str)
                    == Some("true")
            });
            defaults
                .next()
                .and_then(|class| class.metadata.name.clone())
        };
        let class = named.or_else(default);
        let workload = serving.of(class.as_deref()).map(|served| match served {
            Served::Declared(object) | Served::Guessed(object) => object,
        });
        let identifier = self.paged.ingress_classes.iter().find_map(|found| {
            (found.metadata.name == class).then(|| found.spec.as_ref()?.controller.clone())?
        });
        let declared = self.resources.ingress_controllers.iter();
        let mut declared = declared.filter(|c| Some(&c.controller) == identifier.as_ref());
        let declared = declared.next();
        Controller {
            workload,
            entry_points: declared.and_then(|c| c.entry_points.as_ref()),
            declared_by: declared.map(|c| c.name.as_str()),
        }
    }

    /// The custom kinds whose names end in `Route` and that have
    /// objects, which the lens does not read.
    pub fn unread_routes(&self) -> Vec<String> {
        let kinds = self
            .custom
            .iter()
            .filter(|(resource, objects)| resource.kind.ends_with("Route") && !objects.is_empty());
        let mut found: Vec<String> = kinds
            .map(|(resource, _)| format!("{}.{}", resource.plural, resource.group))
            .collect();
        found.sort();
        found
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn pod(namespace: &str, account: &str, image: &str) -> Arc<Pod> {
        let text = format!(
            "{{metadata: {{namespace: {namespace}, name: p}}, spec: {{serviceAccountName: {account}, containers: [{{name: main, image: '{image}'}}]}}}}"
        );
        Arc::new(serde_yaml::from_str(&text).unwrap())
    }

    #[test]
    fn finds_the_accounts_that_run_a_controllers_image() {
        let pods = [
            pod(
                "tools",
                "applier",
                "registry.example.test/tool/applier:v1.2",
            ),
            pod(
                "tools",
                "fetcher",
                "registry.example.test/tool/fetcher:v1.2",
            ),
            pod(
                "tools",
                "applier",
                "registry.example.test/tool/applier:v1.2",
            ),
        ];
        assert_eq!(
            running_image(&pods, "tool/applier"),
            vec![Account::service_account("tools", "applier")]
        );
    }
}
