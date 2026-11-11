// The text reader's output, turned into a string. The reader returns, for
// each of a line's time steps, a probability for every character of its
// list and for a blank. The text is the most probable entry of each step,
// with a run of one entry counted once and the blanks dropped, which is
// the greedy decode that PP-OCR ships.

// The characters the reader can write, in the order of its outputs. Output
// 0 is the blank, outputs 1 to n are the lines of the list file, and the
// last output is a space.
pub struct Characters(Vec<String>);

impl Characters {
    pub fn parse(list: &str) -> Characters {
        let mut characters = vec![String::new()];
        characters.extend(list.lines().map(String::from));
        characters.push(" ".into());
        Characters(characters)
    }

    pub fn len(&self) -> usize {
        self.0.len()
    }

    pub fn is_empty(&self) -> bool {
        self.0.is_empty()
    }
}

// The text of one line and the mean probability of its characters.
// `probabilities` holds one row of `characters.len()` values for each time
// step.
pub fn decode(probabilities: &[f32], characters: &Characters) -> (String, f32) {
    let mut text = String::new();
    let (mut sum, mut count) = (0.0f32, 0usize);
    let mut previous = 0;
    for step in probabilities.chunks_exact(characters.len()) {
        let (best, probability) = step.iter().enumerate().fold(
            (0, f32::MIN),
            |best, (index, &p)| if p > best.1 { (index, p) } else { best },
        );
        if best != 0 && best != previous {
            text.push_str(&characters.0[best]);
            sum += probability;
            count += 1;
        }
        previous = best;
    }
    (
        text.trim().to_string(),
        if count == 0 { 0.0 } else { sum / count as f32 },
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    // Rows for a list of "a" and "b": blank, a, b, space.
    fn steps(entries: &[usize]) -> Vec<f32> {
        entries
            .iter()
            .flat_map(|&entry| {
                let mut row = vec![0.1 / 3.0; 4];
                row[entry] = 0.9;
                row
            })
            .collect()
    }

    #[test]
    fn decode_counts_a_run_once_and_drops_the_blanks() {
        let characters = Characters::parse("a\nb\n");
        let (text, score) = decode(&steps(&[1, 1, 0, 1, 2, 2, 3, 1, 0]), &characters);
        assert_eq!(text, "aab a");
        assert!((score - 0.9).abs() < 1e-6);
    }

    #[test]
    fn decode_of_blanks_is_empty_with_no_score() {
        let characters = Characters::parse("a\nb\n");
        assert_eq!(
            decode(&steps(&[0, 0, 0]), &characters),
            (String::new(), 0.0)
        );
    }
}
