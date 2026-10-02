// The film pass, `knn-guard`: name a face that the headshots did not name
// from the faces of the same film that they did name. A headshot is one
// photograph, often decades from the film, lit for a camera and facing it.
// The film shows the same person in its own lighting, in makeup, and in
// profile, and many of those faces fall under the threshold. On the bench
// of nine films, the older the film, the fewer faces its headshots named.
//
// An unnamed face takes a name only when three conditions are true:
//
// - At least AGREE faces that the headshots named, from other samples, have
//   a similarity of NEIGHBOUR or more to it. Two faces in one picture are
//   two people, so a face of the same sample is no evidence.
// - All of those named faces name one person.
// - The named faces are at least SHARE of all its neighbours at NEIGHBOUR,
//   named or not. A group of faces that the headshots almost never name,
//   such as a character in heavy prosthetic makeup, takes no name from the
//   few of its faces that the headshots named wrong. Without this
//   condition, a plain vote named one character's faces for another actor.
//
// The pass reads the headshot names only, so a name it gives never counts
// as evidence for another face, and one wrong name cannot spread through a
// chain of similar faces. Of 1,085 of its names checked by eye on the
// bench, 16 were wrong.

use crate::runtime::Error;

pub const NEIGHBOUR: f32 = 0.5;
pub const AGREE: usize = 2;
pub const SHARE: f64 = 0.3;

// The person each face was named for, by sample and by the face's place in
// its sample, or None.
pub type Persons = Vec<Vec<Option<usize>>>;

// The names this pass adds: for each face that `headshots` leaves unnamed,
// the person its named neighbours agree on, or None. A face the headshots
// named is None here too.
pub fn names(faces: &[Vec<Vec<f32>>], headshots: &Persons) -> Result<Persons, Error> {
    let all = Flat::new(faces, headshots)?;
    let mut added: Persons = faces.iter().map(|line| vec![None; line.len()]).collect();
    let unnamed: Vec<usize> = (0..all.len())
        .filter(|&i| all.person[i].is_none())
        .collect();
    // The rows go in blocks, so each face of the film is read from memory
    // once per block and not once per unnamed face. A film of 18,000 faces
    // holds 9 MB of vectors, more than a core's cache.
    for block in unnamed.chunks(BLOCK) {
        let mut tallies = [Tally::default(); BLOCK];
        for j in 0..all.len() {
            let other = all.vector(j);
            for (tally, &i) in tallies.iter_mut().zip(block) {
                if all.sample[i] != all.sample[j] && dot(all.vector(i), other) >= NEIGHBOUR {
                    tally.add(all.person[j]);
                }
            }
        }
        for (tally, &i) in tallies.iter().zip(block) {
            if let Some(person) = tally.agreed() {
                added[all.sample[i]][all.place[i]] = Some(person);
            }
        }
    }
    Ok(added)
}

const BLOCK: usize = 16;

// One unnamed face's neighbours: how many there are, how many of them the
// headshots named, and the person they named, or Mixed when they named two.
#[derive(Clone, Copy, Default)]
struct Tally {
    neighbours: usize,
    named: usize,
    person: Agreement,
}

#[derive(Clone, Copy, Default, PartialEq)]
enum Agreement {
    #[default]
    Nobody,
    One(usize),
    Mixed,
}

impl Tally {
    fn add(&mut self, person: Option<usize>) {
        self.neighbours += 1;
        let Some(person) = person else {
            return;
        };
        self.named += 1;
        self.person = match self.person {
            Agreement::Nobody => Agreement::One(person),
            Agreement::One(held) if held == person => Agreement::One(held),
            _ => Agreement::Mixed,
        };
    }

    fn agreed(&self) -> Option<usize> {
        let Agreement::One(person) = self.person else {
            return None;
        };
        let share = self.named as f64 / self.neighbours as f64;
        (self.named >= AGREE && share >= SHARE).then_some(person)
    }
}

// Every face of the film in one array, with its sample, its place in the
// sample, and its headshot name beside it.
struct Flat {
    width: usize,
    vectors: Vec<f32>,
    sample: Vec<usize>,
    place: Vec<usize>,
    person: Vec<Option<usize>>,
}

impl Flat {
    fn new(faces: &[Vec<Vec<f32>>], headshots: &Persons) -> Result<Flat, Error> {
        let width = faces.iter().flatten().map(Vec::len).next().unwrap_or(0);
        let mut flat = Flat {
            width,
            vectors: Vec::new(),
            sample: Vec::new(),
            place: Vec::new(),
            person: Vec::new(),
        };
        for (sample, (line, names)) in faces.iter().zip(headshots).enumerate() {
            for (place, (vector, person)) in line.iter().zip(names).enumerate() {
                if vector.len() != width {
                    return Err(format!(
                        "a face's vector has {} values, and the first has {width}",
                        vector.len()
                    )
                    .into());
                }
                flat.vectors.extend_from_slice(vector);
                flat.sample.push(sample);
                flat.place.push(place);
                flat.person.push(*person);
            }
        }
        Ok(flat)
    }

    fn len(&self) -> usize {
        self.sample.len()
    }

    fn vector(&self, index: usize) -> &[f32] {
        &self.vectors[index * self.width..(index + 1) * self.width]
    }
}

// The dot product in eight lanes, which the compiler turns into vector
// instructions. A plain sum fixes the order of the additions, and the
// compiler then adds one product at a time.
fn dot(a: &[f32], b: &[f32]) -> f32 {
    let mut lanes = [0.0f32; 8];
    let (a_chunks, a_rest) = a.as_chunks::<8>();
    let (b_chunks, b_rest) = b.as_chunks::<8>();
    for (x, y) in a_chunks.iter().zip(b_chunks) {
        for lane in 0..8 {
            lanes[lane] += x[lane] * y[lane];
        }
    }
    let rest: f32 = a_rest.iter().zip(b_rest).map(|(x, y)| x * y).sum();
    lanes.iter().sum::<f32>() + rest
}

#[cfg(test)]
mod tests {
    use super::*;

    // Unit vectors at a known similarity to A: 1 for A itself, 0.6 for
    // NEAR, 0.4 for FAR, and 0.766 for TURNED, which is under 0.5 from
    // NEAR.
    const A: [f32; 3] = [1.0, 0.0, 0.0];
    const NEAR: [f32; 3] = [0.6, 0.8, 0.0];
    const FAR: [f32; 3] = [0.4, 0.9165, 0.0];
    const TURNED: [f32; 3] = [0.766, -0.6428, 0.0];

    // A face: its vector and the person its headshot pass named.
    type Face = ([f32; 3], Option<usize>);

    // A case: its name, its samples, and the name the first face takes.
    type Case = (&'static str, Vec<&'static [Face]>, Option<usize>);

    const UNNAMED: Face = (A, None);
    const JANE: Face = (A, Some(0));
    const JOHN: Face = (A, Some(1));

    // The name this pass gives the first face of the first sample. Each
    // sample is a list of faces.
    fn first(samples: &[&[Face]]) -> Option<usize> {
        let faces: Vec<Vec<Vec<f32>>> = samples
            .iter()
            .map(|line| line.iter().map(|(v, _)| v.to_vec()).collect())
            .collect();
        let headshots: Persons = samples
            .iter()
            .map(|line| line.iter().map(|(_, p)| *p).collect())
            .collect();
        names(&faces, &headshots).unwrap()[0][0]
    }

    #[test]
    fn an_unnamed_face_takes_the_name_its_named_neighbours_agree_on() {
        let cases: [Case; 9] = [
            (
                "two named neighbours of one person",
                vec![&[UNNAMED], &[JANE], &[JANE]],
                Some(0),
            ),
            ("one named neighbour", vec![&[UNNAMED], &[JANE]], None),
            (
                "named neighbours of two people",
                vec![&[UNNAMED], &[JANE], &[JANE], &[JOHN]],
                None,
            ),
            (
                "named neighbours only in its own sample",
                vec![&[UNNAMED, JANE, JANE]],
                None,
            ),
            (
                "named faces 2 of 7 neighbours",
                vec![
                    &[UNNAMED],
                    &[JANE],
                    &[JANE],
                    &[UNNAMED],
                    &[UNNAMED],
                    &[UNNAMED],
                    &[UNNAMED],
                    &[UNNAMED],
                ],
                None,
            ),
            (
                "named faces 2 of 6 neighbours",
                vec![
                    &[UNNAMED],
                    &[JANE],
                    &[JANE],
                    &[UNNAMED],
                    &[UNNAMED],
                    &[UNNAMED],
                    &[UNNAMED],
                ],
                Some(0),
            ),
            (
                "named faces at a similarity of 0.6",
                vec![&[UNNAMED], &[(NEAR, Some(0))], &[(NEAR, Some(0))]],
                Some(0),
            ),
            (
                "named faces at a similarity of 0.4",
                vec![&[UNNAMED], &[(FAR, Some(0))], &[(FAR, Some(0))]],
                None,
            ),
            // The two A faces take Jane's name from the NEAR faces in this
            // pass, but the pass reads only the headshot names, so the
            // TURNED face, near the A faces and not the NEAR faces, has no
            // named neighbour.
            (
                "a face whose neighbours this pass names",
                vec![
                    &[(TURNED, None)],
                    &[UNNAMED],
                    &[UNNAMED],
                    &[(NEAR, Some(0))],
                    &[(NEAR, Some(0))],
                ],
                None,
            ),
        ];
        for (case, samples, named) in cases {
            assert_eq!(first(&samples), named, "{case}");
        }
    }

    #[test]
    fn a_face_the_headshots_named_takes_nothing_from_this_pass() {
        assert_eq!(first(&[&[JANE], &[JANE], &[JANE]]), None);
    }

    #[test]
    fn dot_adds_every_value_past_the_last_full_lane() {
        let a: Vec<f32> = (1..=11).map(|v| v as f32).collect();
        let ones = vec![1.0; 11];
        assert_eq!(dot(&a, &ones), 66.0);
    }
}
