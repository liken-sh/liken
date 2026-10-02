// The document `match` prints on standard output: the inputs of the match
// and, for each video, how many faces each pass named and how many people.
// The operator writes it into the ledger, .liken/appearances.yaml, beside
// the attempt. The observations stay in each video's matches file
// (matches.rs), because a walk reads every ledger of a folder on each pass,
// and a film holds thousands of observations.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::matches::{By, Inputs, Matches};

pub const FORMAT: &str = "liken.sh/appearances/summary/v1";

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Summary {
    pub format: String,
    #[serde(flatten)]
    pub inputs: Inputs,
    // Each video file's counts, by the file's name in the title's folder.
    pub files: BTreeMap<String, FileSummary>,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct FileSummary {
    // The size in bytes of the file the detections record came from.
    pub size: u64,
    pub named: Named,
    // How many different people the faces name.
    pub people: usize,
}

// How many faces each pass named.
#[derive(Clone, Copy, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct Named {
    pub headshot: usize,
    pub film: usize,
}

pub fn summary(inputs: &Inputs, files: &[Matches]) -> Summary {
    let files = files
        .iter()
        .map(|file| {
            let mut named = Named::default();
            for observation in &file.observations {
                match observation.by {
                    By::Headshot => named.headshot += 1,
                    By::Film => named.film += 1,
                }
            }
            let people: BTreeSet<&str> = file
                .observations
                .iter()
                .map(|o| o.contributor.as_str())
                .collect();
            let counts = FileSummary {
                size: file.size,
                named,
                people: people.len(),
            };
            (file.video.clone(), counts)
        })
        .collect();
    Summary {
        format: FORMAT.into(),
        inputs: inputs.clone(),
        files,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::matches::{self, Observation};
    use crate::record::Model;

    fn inputs() -> Inputs {
        Inputs {
            embedder: Model {
                name: "m".into(),
                sha256: "00".into(),
            },
            threshold: 0.363,
            margin: 0.05,
            gallery: vec![],
            unmatched: vec![],
        }
    }

    fn seen(time: f64, who: &str, by: By) -> Observation {
        Observation {
            time,
            face: 0,
            contributor: who.into(),
            by,
            similarity: 0.5,
            runner_up: None,
        }
    }

    #[test]
    fn a_summary_counts_the_faces_of_each_pass_and_the_people() {
        let file = Matches {
            format: matches::FORMAT.into(),
            video: "film.mkv".into(),
            size: 1234,
            inputs: inputs(),
            observations: vec![
                seen(1.0, "jane", By::Headshot),
                seen(2.0, "jane", By::Film),
                seen(2.0, "john", By::Headshot),
            ],
        };
        let summary = summary(&inputs(), &[file]);
        assert_eq!(
            summary.files["film.mkv"],
            FileSummary {
                size: 1234,
                named: Named {
                    headshot: 2,
                    film: 1
                },
                people: 2,
            }
        );
    }

    #[test]
    fn a_summary_holds_no_observation() {
        let text = serde_json::to_string(&summary(&inputs(), &[])).unwrap();
        assert_eq!(
            text,
            r#"{"format":"liken.sh/appearances/summary/v1","embedder":{"name":"m","sha256":"00"},"threshold":0.363,"margin":0.05,"gallery":[],"unmatched":[],"files":{}}"#
        );
    }
}
