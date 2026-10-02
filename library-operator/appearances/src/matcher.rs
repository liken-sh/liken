// The headshot pass: compare each face's vector with each person's headshot
// vector, and name the face for the closest person when the cosine
// similarity reaches the threshold and leads every other person by the
// margin. Both vectors are unit vectors, so the cosine is their dot product.
// knn_guard.rs names more faces after this pass, from the faces this pass
// named.

use crate::gallery::Gallery;
use crate::record::{self, Record};
use crate::runtime::Error;

// The cosine threshold OpenCV publishes for SFace. On the experiments' film,
// 84 of 84 matches checked by eye at this threshold were the right person.
pub const THRESHOLD: f32 = 0.363;

// How far the closest person must lead the next closest. A face that sits
// near two headshots at once is not evidence for either. On a 1080p and a
// 4K film, the 64 named faces with the smallest leads were checked by eye.
// Under a lead of 0.05, 2 of 5 names were wrong. From 0.05 to 0.10, 3 of
// 43 were wrong. So the margin removes the faces where the answer is close
// to a coin toss, and costs 1 or 2 right names in a film. A face in heavy
// makeup named for another actor can lead by more than 0.10, and the
// margin does not remove it.
pub const MARGIN: f32 = 0.05;

// The rule that turns a closest person into a name.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Rule {
    pub threshold: f32,
    pub margin: f32,
}

impl Default for Rule {
    fn default() -> Self {
        Rule {
            threshold: THRESHOLD,
            margin: MARGIN,
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Best {
    pub person: usize,
    pub similarity: f32,
    // The similarity of the next closest person, or None in a gallery with
    // one person. Each person counts once, at their closest vector.
    pub runner_up: Option<f32>,
}

impl Best {
    pub fn names(&self, rule: Rule) -> bool {
        self.similarity >= rule.threshold
            && self
                .runner_up
                .is_none_or(|second| self.similarity - second >= rule.margin)
    }
}

pub fn cosine(a: &[f32], b: &[f32]) -> f32 {
    a.iter().zip(b).map(|(x, y)| x * y).sum()
}

// The headshot vectors of the people who have one, beside each person's
// index in the gallery, so a person with no headshot never takes a place.
pub struct Candidates {
    vectors: Vec<(usize, Vec<f32>)>,
}

impl Candidates {
    pub fn new(gallery: &Gallery) -> Result<Self, Error> {
        let mut vectors = Vec::new();
        for (index, person) in gallery.people.iter().enumerate() {
            if let Some(text) = &person.embedding {
                vectors.push((index, record::decode(text)?));
            }
        }
        Ok(Candidates { vectors })
    }

    // The closest person, named or not. A caller that names faces asks
    // `Best::names`. The review tool shows the closest person for an
    // unnamed face too, so a near miss is visible.
    pub fn best(&self, face: &[f32]) -> Option<Best> {
        let mut ranked: Vec<(usize, f32)> = self
            .vectors
            .iter()
            .map(|(person, vector)| (*person, cosine(face, vector)))
            .collect();
        ranked.sort_by(|a, b| b.1.total_cmp(&a.1));
        let (person, similarity) = *ranked.first()?;
        Some(Best {
            person,
            similarity,
            runner_up: ranked.get(1).map(|r| r.1),
        })
    }

    // The face's similarity to one person's headshot, with the closest other
    // person's similarity as the runner-up. A face that knn_guard.rs names
    // can be closer to another person's headshot than to its own, so the
    // runner-up can exceed the similarity.
    pub fn against(&self, face: &[f32], person: usize) -> Option<Best> {
        let mut similarity = None;
        let mut runner_up: Option<f32> = None;
        for (other, vector) in &self.vectors {
            let s = cosine(face, vector);
            if *other == person {
                similarity = Some(s);
            } else {
                runner_up = Some(runner_up.map_or(s, |r| r.max(s)));
            }
        }
        Some(Best {
            person,
            similarity: similarity?,
            runner_up,
        })
    }
}

// Every face's vector in a record, decoded once, by sample and by the
// face's place in its sample.
pub type Faces = Vec<Vec<Vec<f32>>>;

pub fn faces(record: &Record) -> Result<Faces, Error> {
    record
        .samples
        .iter()
        .map(|line| {
            line.faces
                .iter()
                .map(|d| record::decode(&d.embedding))
                .collect()
        })
        .collect()
}

// The closest person for every face, in the same shape as `Faces`.
pub type Closest = Vec<Vec<Option<Best>>>;

pub fn closest(faces: &Faces, candidates: &Candidates) -> Closest {
    faces
        .iter()
        .map(|line| line.iter().map(|v| candidates.best(v)).collect())
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::gallery::{Headshot, Person};
    use crate::record::{Model, encode};

    fn person(name: &str, embedding: Option<&[f32]>) -> Person {
        Person {
            contributor: format!(".contributors/{name}"),
            name: name.into(),
            role: String::new(),
            headshot: if embedding.is_some() {
                Headshot::Found
            } else {
                Headshot::Missing
            },
            headshot_sha256: None,
            embedding: embedding.map(encode),
        }
    }

    fn gallery() -> Gallery {
        Gallery {
            embedder: Model {
                name: "m".into(),
                sha256: "00".into(),
            },
            people: vec![
                person("nobody", None),
                person("east", Some(&[1.0, 0.0])),
                person("north", Some(&[0.0, 1.0])),
            ],
        }
    }

    #[test]
    fn best_picks_the_closest_headshot_by_its_gallery_index() {
        let best = Candidates::new(&gallery())
            .unwrap()
            .best(&[0.6, 0.8])
            .unwrap();
        assert_eq!(best.person, 2);
        assert!((best.similarity - 0.8).abs() < 1e-3);
        assert!((best.runner_up.unwrap() - 0.6).abs() < 1e-3);
    }

    #[test]
    fn best_finds_nobody_in_a_gallery_with_no_headshots() {
        let mut empty = gallery();
        empty.people.truncate(1);
        assert_eq!(Candidates::new(&empty).unwrap().best(&[1.0, 0.0]), None);
    }

    #[test]
    fn against_scores_one_person_with_the_closest_other_as_runner_up() {
        let candidates = Candidates::new(&gallery()).unwrap();
        let best = candidates.against(&[0.6, 0.8], 1).unwrap();
        assert_eq!(best.person, 1);
        assert!((best.similarity - 0.6).abs() < 1e-3);
        assert!((best.runner_up.unwrap() - 0.8).abs() < 1e-3);
    }

    #[test]
    fn against_finds_nothing_for_a_person_with_no_headshot() {
        let candidates = Candidates::new(&gallery()).unwrap();
        assert_eq!(candidates.against(&[0.6, 0.8], 0), None);
    }

    #[test]
    fn a_gallery_of_one_has_no_runner_up() {
        let mut one = gallery();
        one.people.truncate(2);
        let best = Candidates::new(&one).unwrap().best(&[1.0, 0.0]).unwrap();
        assert_eq!(best.runner_up, None);
    }

    const RULE: Rule = Rule {
        threshold: 0.4,
        margin: 0.05,
    };

    #[test]
    fn names_needs_the_threshold_and_the_margin() {
        let cases = [
            (0.50, Some(0.40), true),
            (0.50, Some(0.46), false),
            (0.39, Some(0.10), false),
            (0.41, None, true),
        ];
        for (similarity, runner_up, named) in cases {
            let best = Best {
                person: 0,
                similarity,
                runner_up,
            };
            assert_eq!(best.names(RULE), named, "{similarity} over {runner_up:?}");
        }
    }
}
