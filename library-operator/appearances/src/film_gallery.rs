// The film's own gallery, an experiment that `--self-gallery` turns on. A
// headshot is one photograph, often from another decade, lit for a camera
// and facing it. The film shows the same person in its own lighting, in
// makeup, in a helmet, and in profile, and those faces fall under the
// threshold. So the match names the faces once from the headshots, takes
// each person's most confident faces in this film as extra vectors for
// that person, and names every face again.
//
// The risk is a wrong name that the second pass repeats, so only a face far
// over the threshold and far ahead of every other person joins the
// gallery.

use std::collections::BTreeSet;

use crate::gallery::Gallery;
use crate::matcher::{self, Candidates, Closest, Faces, Rule};
use crate::runtime::Error;

// The least similarity to the headshot a face needs to join the gallery.
// The wrong names found by eye in a 1080p and a 4K film were all under
// 0.41.
pub const MINIMUM: f32 = 0.5;

// The most faces one person adds. Consecutive keyframes of one shot give
// nearly the same face, so the faces come from different keyframes.
pub const PER_PERSON: usize = 10;

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Naming {
    pub rule: Rule,
    pub self_gallery: bool,
}

// The closest person for every face from the headshots alone, and again
// with the film's own faces added when the experiment is on.
pub struct Passes {
    pub headshots: Closest,
    pub film: Option<Closest>,
}

impl Passes {
    pub fn last(&self) -> &Closest {
        self.film.as_ref().unwrap_or(&self.headshots)
    }
}

pub fn name(faces: &Faces, gallery: &Gallery, naming: Naming) -> Result<Passes, Error> {
    let mut candidates = Candidates::new(gallery)?;
    let headshots = matcher::closest(faces, &candidates);
    if !naming.self_gallery {
        return Ok(Passes {
            headshots,
            film: None,
        });
    }
    for (person, keyframe, face) in choose(&headshots, naming.rule, gallery.people.len()) {
        candidates.add(person, faces[keyframe][face].clone());
    }
    let film = matcher::closest(faces, &candidates);
    Ok(Passes {
        headshots,
        film: Some(film),
    })
}

// Each person's most confident faces, as (person, keyframe, face), at most
// one from each keyframe and PER_PERSON in all.
fn choose(closest: &Closest, rule: Rule, people: usize) -> Vec<(usize, usize, usize)> {
    let mut confident: Vec<Vec<(f32, usize, usize)>> = vec![Vec::new(); people];
    for (keyframe, line) in closest.iter().enumerate() {
        for (face, best) in line.iter().enumerate() {
            if let Some(best) = best
                && best.names(rule)
                && best.similarity >= MINIMUM
            {
                confident[best.person].push((best.similarity, keyframe, face));
            }
        }
    }
    let mut chosen = Vec::new();
    for (person, mut faces) in confident.into_iter().enumerate() {
        faces.sort_by(|a, b| b.0.total_cmp(&a.0));
        let mut keyframes = BTreeSet::new();
        chosen.extend(
            faces
                .into_iter()
                .filter(|f| keyframes.insert(f.1))
                .take(PER_PERSON)
                .map(|(_, keyframe, face)| (person, keyframe, face)),
        );
    }
    chosen
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::gallery::{Headshot, Person};
    use crate::record::{Model, encode};

    fn person(name: &str, embedding: &[f32]) -> Person {
        Person {
            contributor: format!(".contributors/{name}"),
            name: name.into(),
            role: String::new(),
            headshot: Headshot::Found,
            headshot_sha256: None,
            embedding: Some(encode(embedding)),
        }
    }

    fn gallery() -> Gallery {
        Gallery {
            embedder: Model {
                name: "m".into(),
                sha256: "00".into(),
            },
            people: vec![
                person("east", &[1.0, 0.0, 0.0]),
                person("up", &[0.0, 0.0, 1.0]),
            ],
        }
    }

    // A face close to the headshot, and a face the headshot misses that
    // lies close to the first face.
    fn faces() -> Faces {
        vec![vec![vec![0.8, 0.6, 0.0]], vec![vec![0.3, 0.954, 0.0]]]
    }

    fn named(passes: &Closest, rule: Rule) -> Vec<Option<usize>> {
        passes
            .iter()
            .flatten()
            .map(|b| b.filter(|b| b.names(rule)).map(|b| b.person))
            .collect()
    }

    #[test]
    fn the_headshots_alone_miss_the_second_face() {
        let naming = Naming {
            rule: Rule::default(),
            self_gallery: false,
        };
        let passes = name(&faces(), &gallery(), naming).unwrap();
        assert_eq!(named(passes.last(), naming.rule), [Some(0), None]);
    }

    #[test]
    fn the_films_own_faces_name_the_second_face() {
        let naming = Naming {
            rule: Rule::default(),
            self_gallery: true,
        };
        let passes = name(&faces(), &gallery(), naming).unwrap();
        assert_eq!(named(passes.last(), naming.rule), [Some(0), Some(0)]);
    }
}
