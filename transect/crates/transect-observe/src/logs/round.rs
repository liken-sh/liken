//! One round of the searches across the cluster.  A round searches the
//! error lines and each pattern.  It counts and samples each search in
//! the pods' lines, and again in the machines' own lines when the
//! source declares which lines those are.  All queries of a round run
//! at the same time, and the timeout bounds each one.

use std::time::Duration;

use super::machines::{self, MachineCount};
use super::patterns::LogPattern;
use super::sample::{self, Sample};
use super::search::{Asked, Count, ERRORS_WINDOW, Searched, counts, search_query};
use super::source::Reached;
use super::store::Store;

/// One search: the thing it looks for, its filter, and its window.
struct Search {
    asked: Asked,
    filter: String,
    window: String,
}

/// The results of one search: the pods' counts or the reason there are
/// none, the machines' counts, and the samples of the pods' lines and
/// of the machines' lines.
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
        asked: Asked::Errors,
        filter: source.language.at_least_error(level, &source.levels.error),
        window: ERRORS_WINDOW.into(),
    });
    let errors = errors.filter(|_| !source.levels.error.is_empty());
    let searches: Vec<Search> = errors
        .into_iter()
        .chain(patterns.iter().map(|pattern| Search {
            asked: Asked::Pattern(pattern.name.clone()),
            filter: pattern.matches.clone(),
            window: pattern.window.clone(),
        }))
        .collect();
    let asks = searches
        .iter()
        .map(|search| found(store, reached, search, timeout));
    let found = futures::future::join_all(asks).await;
    let mut searched = Searched {
        source: Some(source.name.clone()),
        ..Searched::default()
    };
    for (search, found) in searches.into_iter().zip(found) {
        let asked = search.asked;
        searched.samples.push((asked.clone(), found.samples));
        searched.machines.push((asked.clone(), found.machines));
        searched
            .machine_samples
            .push((asked.clone(), found.machine_samples));
        match asked {
            Asked::Errors => searched.errors = Some(found.pods),
            Asked::Pattern(name) => {
                if let Err(why) = &found.pods {
                    tracing::warn!(pattern = %name, %why, "a log pattern's search failed");
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
/// machines.  When the store refuses the search of the machines' own
/// lines, the machines keep only the counts from the pods' rows.
async fn found(store: &Store, reached: &Reached, search: &Search, timeout: Duration) -> Found {
    let source = &reached.source;
    let (pods, own) = machines::split(&search.filter, source);
    let ask = |filter: String| async move {
        let query = search_query(reached, &filter, &search.window);
        store.ask(reached, &query, timeout).await
    };
    let lines = |filter: String| async move {
        let Some(query) = sample::query(reached, &filter, &search.window) else {
            return Vec::new();
        };
        let rows = store.ask(reached, &query, timeout).await;
        rows.map_or_else(|_| Vec::new(), |rows| sample::read(&rows, &source.fields))
    };
    let own_counts = async {
        match &own {
            Some(own) => ask(own.clone()).await.ok(),
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
        ask(pods.clone()),
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
