//! Renders one view: draws the view's page from the latest model on a
//! blocking thread, then writes the view's edge kinds onto the links in
//! the page.

use k8s_openapi::jiff::Timestamp;
use std::sync::Arc;
use std::time::Instant;

use axum::http::StatusCode;
use tokio::sync::watch;

use super::carry;
use super::latest::Latest;
use super::model::Model;
use super::pages::{
    Focus, Lens, ObjectAt, VolumeAt, WorkloadAt, access_page, accounts_page, changed_page,
    cluster_page, gitops_page, gone_page, ground_page, kind_page, kinds_page, known_page,
    logs_page, machine_page, manages, namespace_access_page, namespace_page, object_page,
    objects_page, pod_page, resources_page, section_page, service_account_page, subject_page,
    volume_page, vulnerabilities_page, ways_page, workload_at, workload_page,
};
use super::template::{Drawing, template};
use super::view::{Page, View};
use transect_map::map::Gone;
use transect_map::objects::reads::Reads;
use transect_observe::logs::{LogTails, Tails};
use transect_observe::usage;
use transect_supply::vulnerabilities::{IdPage, ListPage};

/// What one render reads besides the view.
#[derive(Clone)]
pub struct Sources {
    pub models: Latest<Model>,
    pub usage: watch::Receiver<usage::Reading>,
    /// The log tails that a page load read.  A later render of the same
    /// pod page reads them from this cache.
    pub logs: LogTails,
    /// The objects of kinds that no store holds.  `prepare` reads one
    /// when its page loads, and every render of that page draws it.
    pub reads: Reads,
}

/// Whether to render a whole page, or only its `main` block, which is
/// what an update pushed to the browser contains.
#[derive(Clone, Copy)]
pub enum Part {
    Whole,
    Main,
}

/// A view's HTML, and whether the object it names is in the cluster.
pub struct Rendered {
    pub html: String,
    /// False when the render drew the page that says the object is
    /// gone.  A page load responds to that page with 404.
    pub found: bool,
}

impl Sources {
    /// Reads the object `page` shows when no store holds its kind, so
    /// its renders draw what the API server returned.  Every other page
    /// reads nothing here.
    pub async fn prepare(&self, page: &Page) {
        let object_at = match page {
            Page::Workload {
                namespace,
                resource,
                name,
            } => ObjectAt {
                namespace: Some(namespace),
                resource,
                name,
            },
            Page::ClusterObject { resource, name } => ObjectAt {
                namespace: None,
                resource,
                name,
            },
            _ => return,
        };
        let Some(model) = self.models.last().await else {
            return;
        };
        if let Some(key) = super::pages::read_key(&model, object_at) {
            self.reads.read(&key).await;
        }
    }

    /// The view rendered from the latest model, the page that says the
    /// object is gone when the cluster has no such object, or
    /// `Err(INTERNAL_SERVER_ERROR)` when rendering fails.
    pub async fn render(&self, view: View, part: Part) -> Result<Rendered, StatusCode> {
        let model = self.models.get().await;
        self.render_on(model, view, part).await
    }

    /// Renders the view from `model`, which the caller got from
    /// `models`, or returns `Err(INTERNAL_SERVER_ERROR)` when `model` is
    /// `None`.  Drawing a page is CPU work that grows with the cluster,
    /// so it runs on a blocking thread and leaves the async workers free
    /// for the event streams.
    pub async fn render_on(
        &self,
        model: Option<Arc<Model>>,
        view: View,
        part: Part,
    ) -> Result<Rendered, StatusCode> {
        let model = model.ok_or(StatusCode::INTERNAL_SERVER_ERROR)?;
        let sources = self.clone();
        let drawn = tokio::task::spawn_blocking(move || {
            let started = Instant::now();
            let drawn = sources.draw(&model, &view, part);
            tracing::debug!(page = ?view.page, elapsed = ?started.elapsed(), "rendered a view");
            drawn
        });
        drawn.await.map_err(|_| StatusCode::INTERNAL_SERVER_ERROR)?
    }

    fn draw(&self, model: &Model, view: &View, part: Part) -> Result<Rendered, StatusCode> {
        // Clone the reading so that the watch channel's read lock is
        // released before the render starts.  A held lock would block the
        // sender's next update until the render finished.
        let usage = self.usage.borrow().clone();
        let logs = |namespace: &str, name: &str| self.logs.cached(namespace, name);
        draw(
            model,
            view,
            part,
            &usage,
            &self.reads,
            logs,
            Timestamp::now(),
        )
    }
}

/// Draws `view` from `model`.  `usage` fills the usage bars, and `logs`
/// returns the log tails that a pod page shows.
pub fn draw(
    model: &Model,
    view: &View,
    part: Part,
    usage: &usage::Reading,
    reads: &Reads,
    logs: impl Fn(&str, &str) -> Option<Arc<Tails>>,
    now: Timestamp,
) -> Result<Rendered, StatusCode> {
    let layers = &model.snapshot.resources.layers;
    let shown = &view.shown;
    let part = Drawing {
        part,
        vulnerabilities: model.vulnerabilities.described,
    };
    let found = match &view.page {
        Page::Cluster { select } => {
            let focus = Focus::Select(select.as_deref());
            Some(template(
                cluster_page(model, layers, usage, focus, shown, now),
                part,
            ))
        }
        Page::Loss { lose, select } => {
            let focus = Focus::Lose(lose, select.as_deref());
            let html = template(cluster_page(model, layers, usage, focus, shown, now), part);
            Some(html.map(|html| carry::carry_loss(&html, lose)))
        }
        Page::Resources => Some(template(resources_page(model, usage), part)),
        Page::Kinds => Some(template(kinds_page(model), part)),
        Page::WaysIn => Some(template(ways_page(model), part)),
        Page::Accounts => Some(template(accounts_page(model), part)),
        Page::Vulnerabilities { severities } => {
            let page = ListPage::build(&model.vulnerabilities, &model.snapshot.plurals, severities);
            Some(template(page, part))
        }
        Page::Vulnerability { id } => {
            let plurals = &model.snapshot.plurals;
            let page = IdPage::build(&model.vulnerabilities, plurals, id, now);
            page.map(|page| template(page, part))
        }
        Page::Subject { kinds, name } => {
            subject_page(model, kinds, name).map(|page| template(page, part))
        }
        Page::Kind { resource } => {
            let page = kind_page(model, resource, now);
            page.map(|page| template(page, part))
        }
        Page::Changed => Some(template(changed_page(model, shown, now), part)),
        Page::Machine { name } => {
            let page = machine_page(model, usage, name, now);
            page.map(|page| template(page, part))
        }
        Page::Ground { name } => {
            let page = ground_page(model, name);
            page.map(|page| template(page, part))
        }
        Page::Volume { name } => {
            let page = volume_page(model, layers, VolumeAt::Volume(name), now);
            page.map(|page| template(page, part))
        }
        Page::Namespace { name } => {
            let page = namespace_page(model, layers, usage, name, shown, now);
            page.map(|page| template(page, part))
        }
        Page::NamespaceAccess { name } => {
            let page = namespace_access_page(model, layers, name, now);
            page.map(|page| template(page, part))
        }
        Page::Objects {
            namespace,
            resource,
        } => {
            let page = objects_page(model, layers, namespace, resource, now);
            page.map(|page| template(page, part))
        }
        Page::Workload {
            namespace,
            resource,
            name,
        } => {
            let at = WorkloadAt {
                namespace,
                resource,
                name,
            };
            // Every object in a namespace shares this URL form, so try
            // the pages in order: a GitOps object, even when its kind has
            // `KindKnowledge`, then a `ServiceAccount`'s access drawing,
            // then a custom resource whose kind has knowledge, then a
            // workload, and last the plain object page.
            let page = gitops_page(model, layers, namespace, resource, name, shown);
            let page = page.map(|page| template(page, part));
            let page = page.or_else(|| {
                let page = (resource == "serviceaccounts")
                    .then(|| service_account_page(model, layers, namespace, name, now));
                page.flatten().map(|page| template(page, part))
            });
            page.or_else(|| {
                let known = known_page(model, layers, usage, at, shown, now);
                if let Some(known) = known {
                    return Some(template(known, part));
                }
                let page = workload_page(model, layers, usage, at, shown, now);
                let page = page.map(|page| {
                    let workload = workload_at(&model.snapshot, namespace, resource, name);
                    let manages = workload
                        .map(|workload| manages(model, &workload, shown, now))
                        .unwrap_or_default();
                    template(transect_map::map::WorkloadPage { manages, ..page }, part)
                });
                page.or_else(|| {
                    let object_at = ObjectAt {
                        namespace: Some(namespace),
                        resource,
                        name,
                    };
                    let page = object_page(model, layers, reads, object_at, now);
                    page.map(|page| template(page, part))
                })
            })
        }
        Page::ClusterObject { resource, name } => {
            let object_at = ObjectAt {
                namespace: None,
                resource,
                name,
            };
            let page = object_page(model, layers, reads, object_at, now);
            page.map(|page| template(page, part))
        }
        Page::Section {
            namespace,
            resource,
            name,
        } => {
            let at = WorkloadAt {
                namespace,
                resource,
                name,
            };
            let page = section_page(model, layers, usage, at, shown, now);
            page.map(|page| template(page, part))
        }
        Page::Access {
            namespace,
            resource,
            name,
        } => {
            let at = WorkloadAt {
                namespace,
                resource,
                name,
            };
            let page = access_page(model, layers, usage, at, shown, now);
            page.map(|page| template(page, part))
        }
        Page::WorkloadVulnerabilities {
            namespace,
            resource,
            name,
            severities,
        } => {
            let at = WorkloadAt {
                namespace,
                resource,
                name,
            };
            let page = vulnerabilities_page(model, layers, usage, at, severities, now);
            page.map(|page| template(page, part))
        }
        Page::Logs {
            namespace,
            resource,
            name,
            pod,
            container,
        } => {
            let lens = Lens {
                namespace,
                resource,
                name,
                pod: pod.as_deref(),
                container: container.as_deref(),
            };
            logs_page(model, layers, &lens, &logs, now).map(|page| template(page, part))
        }
        Page::Pod { namespace, name } => {
            let page = pod_page(model, layers, usage, namespace, name, shown, now);
            page.map(|page| template(page.with_logs(logs(namespace, name)), part))
        }
    };
    let (html, found) = match (found, gone(&view.page)) {
        (Some(html), _) => (html, true),
        (None, Some(gone)) => (template(gone_page(model, &gone), part), false),
        (None, None) => (None, false),
    };
    let html = html.ok_or(StatusCode::INTERNAL_SERVER_ERROR)?;
    let html = match view.page.in_access() {
        true => carry::carry_access(&html, model.access_paths()),
        false => html,
    };
    let html = match shown.param() {
        Some(edges) => carry::carry(&html, &edges),
        None => html,
    };
    Ok(Rendered { html, found })
}

/// The object that a page is for, so that the gone page can say what is
/// missing.  Returns `None` for a page that is not for one object.  The
/// cluster page is never gone: a selection that it does not hold draws
/// the plain cluster.
fn gone(page: &Page) -> Option<Gone> {
    Some(match page.clone() {
        Page::Cluster { .. }
        | Page::Loss { .. }
        | Page::Resources
        | Page::Changed
        | Page::Kinds
        | Page::WaysIn
        | Page::Accounts
        | Page::Vulnerabilities { .. } => {
            return None;
        }
        Page::Subject { kinds, name } => Gone::Subject { kinds, name },
        Page::Vulnerability { id } => Gone::Vulnerability(id),
        Page::Kind { resource } => Gone::AllOfKind(resource),
        Page::Namespace { name } | Page::NamespaceAccess { name } => Gone::Namespace(name),
        Page::Objects {
            namespace,
            resource,
        } => Gone::Kind {
            namespace,
            resource,
        },
        Page::Workload {
            namespace,
            resource,
            name,
        }
        | Page::Section {
            namespace,
            resource,
            name,
        }
        | Page::Access {
            namespace,
            resource,
            name,
        }
        | Page::WorkloadVulnerabilities {
            namespace,
            resource,
            name,
            ..
        } => Gone::Workload {
            namespace,
            resource,
            name,
        },
        Page::Pod { namespace, name } => Gone::Pod { namespace, name },
        Page::Logs {
            namespace,
            resource,
            name,
            ..
        } if resource == "pods" => Gone::Pod { namespace, name },
        Page::Logs {
            namespace,
            resource,
            name,
            ..
        } => Gone::Workload {
            namespace,
            resource,
            name,
        },
        Page::Machine { name } => Gone::Machine(name),
        Page::Ground { name } => Gone::Ground(name),
        Page::Volume { name } => Gone::ClusterObject {
            resource: "persistentvolumes".into(),
            name,
        },
        Page::ClusterObject { resource, name } => Gone::ClusterObject { resource, name },
    })
}
