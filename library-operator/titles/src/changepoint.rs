// The second the end titles start, from the model's answer for each
// second. The model says, for each second on its own, how probable it is
// that the titles have started. The titles start once and run to the end
// of the file, so the start is the one second that best splits the
// reading into "not yet" before it and "started" from it on: the second s
// that maximizes the sum of log(1 - p) before s and of log(p) from s. One
// second that the model gets wrong moves that sum a little, where a rule
// that took the first second over a threshold would start the titles at a
// street sign.
//
// The sum is a log likelihood, so other knowledge of where the titles
// start adds to it as a log prior, and the scores of all the seconds make
// a distribution, which gives how sure the placement is.

// Another source's opinion of the start: probably at `center`, within a
// few seconds, and possibly nowhere near it. The floor is what keeps a
// wrong opinion from overruling the picture: however far the start is
// from the center, the prior costs it a fixed amount and no more.
#[derive(Clone, Copy, Debug)]
pub struct Prior {
    pub center: f64,
}

impl Prior {
    // The shares of the three parts: a peak 2 seconds wide, a peak 10
    // seconds wide, and the floor. Of the crowd marks of 401 seasons, an
    // episode's end titles were within 3 seconds of the season's median
    // length for 65%, and within 10 seconds for 81%.
    const NARROW: (f64, f64) = (0.6, 2.0);
    const WIDE: (f64, f64) = (0.2, 10.0);
    const FLOOR: f64 = 0.2;

    fn log(&self, time: f64, span: f64) -> f64 {
        let peak = |(share, width): (f64, f64)| {
            share * (-(time - self.center).abs() / width).exp() / (2.0 * width)
        };
        (peak(Self::NARROW) + peak(Self::WIDE) + Self::FLOOR / span).ln()
    }
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Placement {
    // The second the end titles start, or nothing when the reading holds
    // no end titles.
    pub start: Option<f64>,
    // The share of the distribution within 5 seconds of the start.
    pub confidence: f64,
}

// The model's answers are probabilities this close to 0 or 1 at most, so
// one confident wrong second costs a bounded amount.
const CLAMP: f64 = 1e-4;
const NEAR: usize = 5;

// `times` are the seconds of the reading, `probabilities` the model's
// answer for each, and `temperature` divides the log likelihood. The
// model's answers for neighboring seconds come from nearly the same
// numbers, so they are not independent, and a sum over them is too sure by
// about the number of seconds that share their evidence. `train` chooses
// the temperature that makes the confidence match how often the placement
// is right.
pub fn place(
    times: &[f64],
    probabilities: &[f32],
    temperature: f64,
    prior: Option<Prior>,
) -> Placement {
    let count = times.len();
    if count == 0 {
        return Placement {
            start: None,
            confidence: 0.0,
        };
    }
    let clamped: Vec<f64> = probabilities
        .iter()
        .map(|p| (*p as f64).clamp(CLAMP, 1.0 - CLAMP))
        .collect();
    // after[s] is the sum of log(p) from s to the end.
    let mut after = vec![0f64; count + 1];
    for index in (0..count).rev() {
        after[index] = after[index + 1] + clamped[index].ln();
    }
    let span = count as f64;
    let end = times[count - 1] + 1.0;
    let mut scores = Vec::with_capacity(count + 1);
    let mut before = 0f64;
    for candidate in 0..=count {
        let time = if candidate < count {
            times[candidate]
        } else {
            end
        };
        let mut score = (before + after[candidate]) / temperature;
        if let Some(prior) = prior {
            score += prior.log(time, span);
        }
        scores.push(score);
        if candidate < count {
            before += (1.0 - clamped[candidate]).ln();
        }
    }
    let best = (0..=count)
        .max_by(|a, b| scores[*a].total_cmp(&scores[*b]))
        .expect("one candidate");
    let weights: Vec<f64> = scores
        .iter()
        .map(|score| (score - scores[best]).exp())
        .collect();
    let near: f64 = weights[best.saturating_sub(NEAR)..(best + NEAR + 1).min(count + 1)]
        .iter()
        .sum();
    Placement {
        start: (best < count).then(|| times[best]),
        confidence: near / weights.iter().sum::<f64>(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn seconds(count: usize) -> Vec<f64> {
        (0..count).map(|i| 100.0 + i as f64).collect()
    }

    // The answers of a model that is sure of a start at `start`, with the
    // named seconds wrong.
    fn answers(count: usize, start: usize, wrong: &[usize]) -> Vec<f32> {
        (0..count)
            .map(|i| {
                if (i >= start) != wrong.contains(&i) {
                    0.95
                } else {
                    0.05
                }
            })
            .collect()
    }

    #[test]
    fn place_finds_the_step() {
        let placed = place(&seconds(60), &answers(60, 40, &[]), 1.0, None);
        assert_eq!(placed.start, Some(140.0));
        assert!(placed.confidence > 0.99);
    }

    #[test]
    fn place_is_not_moved_by_a_few_wrong_seconds() {
        let placed = place(&seconds(60), &answers(60, 40, &[5, 6, 20, 50]), 1.0, None);
        assert_eq!(placed.start, Some(140.0));
    }

    #[test]
    fn place_finds_no_titles_in_a_reading_of_story() {
        let placed = place(&seconds(60), &answers(60, 60, &[]), 1.0, None);
        assert_eq!(placed.start, None);
    }

    #[test]
    fn place_is_unsure_of_a_slow_change() {
        let slow: Vec<f32> = (0..60).map(|i| 0.3 + 0.4 * i as f32 / 59.0).collect();
        let sure = place(&seconds(60), &answers(60, 30, &[]), 4.0, None);
        let unsure = place(&seconds(60), &slow, 4.0, None);
        assert!(unsure.confidence < 0.5 && sure.confidence > 0.9);
    }

    #[test]
    fn a_prior_moves_an_unsure_placement_and_not_a_sure_one() {
        let prior = Some(Prior { center: 145.0 });
        let mut unsure = answers(60, 30, &[]);
        unsure[30..50].fill(0.5);
        assert_eq!(place(&seconds(60), &unsure, 1.0, prior).start, Some(145.0));
        assert_eq!(
            place(&seconds(60), &answers(60, 20, &[]), 1.0, prior).start,
            Some(120.0)
        );
    }
}
