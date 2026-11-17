//! What the logs add to a reading: health's reasons, the findings for
//! patterns only worth knowing, and the conditions of searches that
//! failed.

use std::collections::HashSet;

use k8s_openapi::jiff::Timestamp;

use super::Snapshot;
use kube::ResourceExt;
use transect_graph::edges::Object;
use transect_observe::health::{self, Reason, Seen};
use transect_observe::logs::search::{Asked, ERRORS_WINDOW};
use transect_resources::resources::{Condition, DocId};
use transect_supply::findings::{self, Finding, Restarted};

impl Snapshot {
    /// The error lines by workload, counted across every pod by the log
    /// source when its search answered, and otherwise from the tails
    /// read, then each error or warning pattern's matches.
    pub(super) fn log_reasons(&self, now: Timestamp) -> Vec<(String, Reason)> {
        let searched = &self.logs.searched;
        let mut reasons = match (&searched.source, &searched.errors) {
            (Some(source), Some(Ok(counts))) => {
                let seen = Seen::Searched {
                    source: source.clone(),
                    window: ERRORS_WINDOW.into(),
                };
                let samples = searched.samples(&Asked::Errors);
                health::searched_errors(counts, &seen, samples, &self.pods, &self.ownership, now)
            }
            _ => health::logged_reasons(&self.logs.logged, &self.pods, &self.ownership, now),
        };
        reasons.extend(health::matched_reasons(
            searched,
            &self.settled_cron_jobs(),
            &self.pods,
            &self.ownership,
            now,
        ));
        reasons
    }

    /// A finding for each pattern only worth knowing that matched, and
    /// one for the machines' own error lines.
    pub fn log_findings(&self, now: Timestamp) -> Vec<Finding> {
        let namespaces = self.namespaces.iter();
        let namespaces: HashSet<&str> = namespaces
            .filter_map(|namespace| namespace.metadata.name.as_deref())
            .collect();
        let searched = &self.logs.searched;
        let machines = searched.machines(&Asked::Errors);
        let machines = findings::machine_logs(machines, ERRORS_WINDOW, now);
        let mut found = findings::worth_knowing(
            &self.logs.searched,
            &self.settled_cron_jobs(),
            &namespaces,
            &self.pods,
            &self.ownership,
            &self.plurals,
            now,
        );
        found.extend(machines);
        found
    }

    /// The containers of every workload that restarted too long ago to
    /// count toward health, each linked to its workload's page.
    pub fn earlier_restarts(&self, now: Timestamp) -> Vec<Restarted> {
        let found = health::earlier_restarts(&self.pods, &self.ownership, now);
        found
            .into_iter()
            .map(|earlier| Restarted {
                href: transect_graph::links::link(&earlier.workload, &self.plurals),
                earlier,
            })
            .collect()
    }

    /// The CronJobs whose last run succeeded, with none running, so
    /// the reader need not look at a pattern's match in an earlier
    /// attempt of that run.
    pub fn settled_cron_jobs(&self) -> HashSet<Object> {
        let settled = self
            .cron_jobs
            .iter()
            .filter(|cron_job| health::cron_job_settled(cron_job));
        settled
            .map(|cron_job| {
                let namespace = cron_job.metadata.namespace.as_deref();
                Object::new("batch", "CronJob", namespace, &cron_job.name_any())
            })
            .collect()
    }

    /// A condition on each pattern, and on the source, whose last
    /// search the store refused or missed.
    pub fn log_conditions(&self) -> Vec<(DocId, Condition)> {
        let searched = &self.logs.searched;
        let mut conditions: Vec<(DocId, Condition)> = searched
            .patterns
            .iter()
            .filter_map(|(pattern, found)| {
                let why = found.as_ref().err()?;
                let source = searched.source.as_deref().unwrap_or_default();
                let message = format!("the LogSource {source} {why}");
                Some((pattern.doc, Condition::search_failed(message)))
            })
            .collect();
        let source = self.resources.log_source.as_ref();
        if let (Some(source), Some(Err(why))) = (source, &searched.errors) {
            let message = format!("counting error lines: the log source {why}");
            conditions.push((source.doc, Condition::search_failed(message)));
        }
        conditions
    }
}

/// The patterns only worth knowing that matched in `namespace`, among
/// the log findings `found`, as the reasons health would give them, for
/// its card.
pub fn worth_knowing_in(found: &[Finding], namespace: &str) -> Vec<Reason> {
    let lines = found.iter().filter_map(|finding| {
        let Finding::Logged {
            pattern,
            meaning,
            window,
            namespaces,
        } = finding
        else {
            return None;
        };
        let here = namespaces.iter().filter(|one| one.namespace == namespace);
        let reasons = here.map(|one| Reason::Matched {
            origin: one.origin.clone(),
            pattern: pattern.clone(),
            meaning: meaning.clone(),
            matches: one.matches,
            window: window.clone(),
            minutes_ago: one.minutes_ago,
            evidence: one.evidence.clone(),
            settled: one.settled,
        });
        Some(reasons.collect::<Vec<_>>())
    });
    lines.flatten().collect()
}
