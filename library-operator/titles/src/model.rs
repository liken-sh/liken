// The model file: the forest, the temperature that `place` divides by, the
// columns the forest was fitted on, and what it was fitted on and how well
// it placed the titles it did not see. Each cluster fits its own model
// from its own library, so the file carries what a person needs to judge
// it: the counts and the held-out score.

use std::fs;
use std::path::Path;

use serde::{Deserialize, Serialize};

use crate::Error;
use crate::signals;
use crate::trees::Forest;

pub const FORMAT: &str = "liken.sh/titles/model/v1";

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Model {
    pub format: String,
    pub trained: Trained,
    pub held_out: Score,
    pub temperature: f64,
    pub columns: Vec<String>,
    pub forest: Forest,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct Trained {
    // The videos and the groups (films and series) the forest was fitted
    // on, the seconds of them, and the videos left out because a first
    // model placed their titles far from their mark and was sure of it.
    pub videos: usize,
    pub groups: usize,
    pub seconds: usize,
    pub left_out: usize,
}

// How close the placements of held-out videos were to their marks. Each
// video was placed by a forest fitted without its film or series.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct Score {
    pub videos: usize,
    pub within_3: f64,
    pub within_10: f64,
    pub within_30: f64,
    pub median: f64,
}

impl Score {
    // `errors` are the distances in seconds from each placement to its
    // mark.
    pub fn of(errors: &[f64]) -> Score {
        if errors.is_empty() {
            return Score::default();
        }
        let mut sorted = errors.to_vec();
        sorted.sort_by(f64::total_cmp);
        let within = |limit: f64| {
            sorted.iter().filter(|e| **e <= limit).count() as f64 / sorted.len() as f64
        };
        Score {
            videos: sorted.len(),
            within_3: within(3.0),
            within_10: within(10.0),
            within_30: within(30.0),
            median: sorted[sorted.len() / 2],
        }
    }
}

impl Model {
    pub fn load(path: &Path) -> Result<Model, Error> {
        let text = fs::read_to_string(path).map_err(|e| format!("{}: {e}", path.display()))?;
        let model: Model = serde_json::from_str(&text)?;
        if model.format != FORMAT {
            return Err(format!("{}: the format is {}", path.display(), model.format).into());
        }
        if model.columns != signals::columns() {
            return Err(
                format!("{}: the model was fitted on other columns", path.display()).into(),
            );
        }
        Ok(model)
    }

    // Writes the model under its name in one rename, so a `place` that
    // starts during a `train` reads the old model or the new one.
    pub fn save(&self, path: &Path) -> Result<(), Error> {
        let name = path.file_name().unwrap_or_default().to_string_lossy();
        let partial = path.with_file_name(format!(".partial-{}-{name}", std::process::id()));
        fs::write(&partial, serde_json::to_string(self)?)?;
        fs::rename(&partial, path)?;
        Ok(())
    }
}

#[cfg(test)]
pub mod tests {
    use super::*;
    use crate::reading::tests::scratch;
    use crate::trees::Node;

    // A model of one tree that answers from the `text` column alone: sure
    // of titles in a second that holds text.
    pub fn model() -> Model {
        let columns = signals::columns();
        let column = columns.iter().position(|c| c == "text").unwrap();
        Model {
            format: FORMAT.into(),
            trained: Trained::default(),
            held_out: Score::default(),
            temperature: 1.0,
            columns,
            forest: Forest {
                base: 0.0,
                trees: vec![vec![
                    Node::Split {
                        column,
                        threshold: 0.5,
                        left: 1,
                        right: 2,
                    },
                    Node::Leaf { value: -3.0 },
                    Node::Leaf { value: 3.0 },
                ]],
            },
        }
    }

    #[test]
    fn score_counts_the_errors_within_each_limit() {
        let score = Score::of(&[0.0, 2.0, 8.0, 50.0]);
        assert_eq!(
            (
                score.videos,
                score.within_3,
                score.within_10,
                score.within_30
            ),
            (4, 0.5, 0.75, 0.75)
        );
        assert_eq!(score.median, 8.0);
    }

    #[test]
    fn a_saved_model_loads_as_it_was() {
        let path = scratch("model").join("model.json");
        model().save(&path).unwrap();
        assert_eq!(Model::load(&path).unwrap(), model());
    }

    #[test]
    fn load_refuses_a_model_of_other_columns() {
        let path = scratch("model-columns").join("model.json");
        let mut other = model();
        other.columns.pop();
        other.save(&path).unwrap();
        assert!(
            Model::load(&path)
                .unwrap_err()
                .to_string()
                .contains("other columns")
        );
    }
}
