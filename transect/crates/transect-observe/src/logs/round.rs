//! One round of log searches across the cluster. A round searches for
//! error lines and for each pattern. Each search counts and samples the
//! pods' lines, and also the machines' own lines when the log source
//! declares which lines those are. A round runs all its queries at the
//! same time, and each query has its own timeout.

use std::time::Duration;

use super::machines::{self, MachineCount};
use super::patterns::LogPattern;
use super::sample::{self, Sample};
use super::search::{Count, ERRORS_WINDOW, SearchTarget, Searched, counts, search_query};
use super::source::Reached;
use super::store::Store;

/// One search: what it looks for, its filter, and its time window.
struct Search {
    target: SearchTarget,
    filter: String,
    window: String,
}

/// The results of one search: the counts for the pods, or the error
/// that prevented them, the counts for the machines, and the samples of
/// the pods' lines and of the machines' lines.
struct Found {
    pods: Result<Vec<Count>, String>,
    machines: Vec<MachineCount>,
    samples: Vec<Sample>,
    machine_samples: Vec<Sample>,
}

pub async fn round(
    store: &Store,
    (reached, patterns): &(Option<Reached>, Vec<LogPattern>),
    timeout: Duration,
) -> Searched {
    let Some(reached) = reached else {
        return Searched::default();
    };
    let source = &reached.source;
    let errors = source.fields.level.as_ref().map(|level| Search {
        target: SearchTarget::Errors,
        filter: source.language.at_least_error(level, &source.levels.error),
        window: ERRORS_WINDOW.into(),
    });
    let errors = errors.filter(|_| !source.levels.error.is_empty());
    let searches: Vec<Search> = errors
        .into_iter()
        .chain(patterns.iter().map(|pattern| Search {
            target: SearchTarget::Pattern(pattern.name.clone()),
            filter: pattern.matches.clone(),
            window: pattern.window.clone(),
        }))
        .collect();
    let runs = searches
        .iter()
        .map(|search| found(store, reached, search, timeout));
    let found = futures::future::join_all(runs).await;
    let mut searched = Searched {
        source: Some(source.name.clone()),
        ..Searched::default()
    };
    for (search, found) in searches.into_iter().zip(found) {
        let target = search.target;
        searched.samples.push((target.clone(), found.samples));
        searched.machines.push((target.clone(), found.machines));
        searched
            .machine_samples
            .push((target.clone(), found.machine_samples));
        match target {
            SearchTarget::Errors => searched.errors = Some(found.pods),
            SearchTarget::Pattern(name) => {
                if let Err(error) = &found.pods {
                    tracing::warn!(pattern = %name, %error, "The search for a log pattern returned an error");
                }
                let pattern = patterns.iter().find(|pattern| pattern.name == name);
                searched
                    .patterns
                    .extend(pattern.cloned().map(|p| (p, found.pods)));
            }
        }
    }
    searched
}

/// The counts and samples of one search, for the pods and for the
/// machines. When the store returns an error for the query of the
/// machines' own lines, the machine counts come only from the pods'
/// rows.
async fn found(store: &Store, reached: &Reached, search: &Search, timeout: Duration) -> Found {
    let source = &reached.source;
    let (pods, own) = machines::split(&search.filter, source);
    let count = |filter: String| async move {
        let query = search_query(reached, &filter, &search.window);
        store.run_query(reached, &query, timeout).await
    };
    let lines = |filter: String| async move {
        let Some(query) = sample::query(reached, &filter, &search.window) else {
            return Vec::new();
        };
        let rows = store.run_query(reached, &query, timeout).await;
        rows.map_or_else(|_| Vec::new(), |rows| sample::read(&rows, &source.fields))
    };
    let own_counts = async {
        match &own {
            Some(own) => count(own.clone()).await.ok(),
            None => None,
        }
    };
    let own_lines = async {
        match &own {
            Some(own) => lines(own.clone()).await,
            None => Vec::new(),
        }
    };
    let (rows, samples, own_rows, machine_samples) = futures::join!(
        count(pods.clone()),
        lines(pods.clone()),
        own_counts,
        own_lines
    );
    let mut machine_counts = match &rows {
        Ok(rows) => machines::counts(rows, source, false),
        Err(_) => Vec::new(),
    };
    if let Some(rows) = own_rows {
        machine_counts.extend(machines::counts(&rows, source, true));
    }
    Found {
        pods: rows.map(|rows| counts(&rows, &source.fields)),
        machines: machine_counts,
        samples,
        machine_samples,
    }
}
