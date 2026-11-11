// The model: a few hundred small decision trees, fitted by gradient
// boosting, that give the probability that the end titles have started at
// one second from the numbers of `signals`. Trees take the numbers as they
// are, with no scaling, and they find the combinations that matter, such
// as "text on a dark frame that goes on to the end". In the experiments
// they placed 88% of episodes within 10 seconds where a logistic
// regression on the same numbers placed 78%.
//
// The trainer is here and not in a library because the fitted model has to
// load in this tool with no second runtime, and because the method is
// small: each value is replaced by the number of its bin, each tree is
// grown one leaf at a time on the sums of each bin, and each tree corrects
// what the trees before it got wrong.

use std::thread;

use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum Node {
    // Rows whose value in the column is at most the threshold go to the
    // left node, and the others go to the right node.
    Split {
        column: usize,
        threshold: f32,
        left: usize,
        right: usize,
    },
    Leaf {
        value: f32,
    },
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Forest {
    // The log odds before any tree, and the trees whose leaves add to it.
    pub base: f32,
    pub trees: Vec<Vec<Node>>,
}

impl Forest {
    pub fn probability(&self, row: &[f32]) -> f32 {
        let mut odds = self.base;
        for tree in &self.trees {
            let mut at = 0;
            loop {
                match &tree[at] {
                    Node::Leaf { value } => {
                        odds += value;
                        break;
                    }
                    Node::Split {
                        column,
                        threshold,
                        left,
                        right,
                    } => {
                        at = if row[*column] <= *threshold {
                            *left
                        } else {
                            *right
                        }
                    }
                }
            }
        }
        1.0 / (1.0 + (-odds).exp())
    }
}

// One forest in place of several: its answer is the mean of their log
// odds. A forest fitted on a few hundred videos changes its mind about a
// hard video when a few of the others change, and the mean of forests
// fitted on different videos changes it less.
pub fn mean(forests: Vec<Forest>) -> Forest {
    let count = forests.len().max(1) as f32;
    let base = forests.iter().map(|forest| forest.base).sum::<f32>() / count;
    let mut trees = Vec::new();
    for forest in forests {
        for mut tree in forest.trees {
            for node in &mut tree {
                if let Node::Leaf { value } = node {
                    *value /= count;
                }
            }
            trees.push(tree);
        }
    }
    Forest { base, trees }
}

#[derive(Clone, Copy, Debug)]
pub struct Settings {
    pub trees: usize,
    // How much of each tree's correction the model takes.
    pub rate: f32,
    pub leaves: usize,
    // Added to the weight of every leaf, so a leaf of few rows moves the
    // model less.
    pub smoothing: f64,
    pub smallest_leaf: u32,
    pub threads: usize,
}

impl Default for Settings {
    // The settings of the experiments, where the placement changed little
    // from 100 to 300 trees and from 7 to 31 leaves.
    fn default() -> Self {
        Settings {
            trees: 200,
            rate: 0.08,
            leaves: 15,
            smoothing: 1.0,
            smallest_leaf: 20,
            threads: 4,
        }
    }
}

// The most bins of one column. A split falls between two bins, so 64 bins
// give a tree 63 places to split each column, at the column's quantiles.
const BINS: usize = 64;

// The edges that split each column into bins: bin b holds the values that
// are over edge b - 1 and at most edge b. `sample` is rows of `width`
// values.
pub fn edges(sample: &[f32], width: usize) -> Vec<Vec<f32>> {
    (0..width)
        .map(|column| {
            let mut values: Vec<f32> = sample.iter().skip(column).step_by(width).copied().collect();
            values.sort_by(f32::total_cmp);
            values.dedup();
            if values.len() <= BINS {
                return values
                    .windows(2)
                    .map(|pair| (pair[0] + pair[1]) / 2.0)
                    .collect();
            }
            let mut edges: Vec<f32> = (1..BINS)
                .map(|cut| values[cut * values.len() / BINS])
                .collect();
            edges.dedup();
            edges
        })
        .collect()
}

// The bin of each value of `rows`, by the edges of its column.
pub fn bins(rows: &[f32], edges: &[Vec<f32>]) -> Vec<u8> {
    let width = edges.len();
    rows.iter()
        .enumerate()
        .map(|(index, value)| edges[index % width].partition_point(|edge| edge < value) as u8)
        .collect()
}

// The sums of one leaf: for each bin of each column, the sum of the
// gradients and of the weights of the leaf's rows in the bin, and their
// count.
#[derive(Clone)]
struct Sums {
    gradient: Vec<f64>,
    weight: Vec<f64>,
    count: Vec<u32>,
}

impl Sums {
    fn new(width: usize) -> Sums {
        Sums {
            gradient: vec![0.0; width * BINS],
            weight: vec![0.0; width * BINS],
            count: vec![0; width * BINS],
        }
    }

    fn add(&mut self, other: &Sums) {
        for index in 0..self.gradient.len() {
            self.gradient[index] += other.gradient[index];
            self.weight[index] += other.weight[index];
            self.count[index] += other.count[index];
        }
    }

    // The sums of the rows that are in `self` and not in `part`. A split
    // sends each row of a leaf to one of two leaves, so the sums of the
    // larger are the parent's less the smaller's, with no pass over its
    // rows.
    fn less(&self, part: &Sums) -> Sums {
        let mut rest = self.clone();
        for index in 0..rest.gradient.len() {
            rest.gradient[index] -= part.gradient[index];
            rest.weight[index] -= part.weight[index];
            rest.count[index] -= part.count[index];
        }
        rest
    }
}

struct Data<'a> {
    bins: &'a [u8],
    width: usize,
    gradient: &'a [f32],
    weight: &'a [f32],
    threads: usize,
}

impl Data<'_> {
    fn sums(&self, rows: &[u32]) -> Sums {
        let count = |rows: &[u32]| {
            let mut sums = Sums::new(self.width);
            for &row in rows {
                let row = row as usize;
                let (gradient, weight) = (self.gradient[row] as f64, self.weight[row] as f64);
                for (column, &bin) in self.bins[row * self.width..(row + 1) * self.width]
                    .iter()
                    .enumerate()
                {
                    let at = column * BINS + bin as usize;
                    sums.gradient[at] += gradient;
                    sums.weight[at] += weight;
                    sums.count[at] += 1;
                }
            }
            sums
        };
        if rows.len() < 20_000 || self.threads < 2 {
            return count(rows);
        }
        let chunk = rows.len().div_ceil(self.threads);
        thread::scope(|scope| {
            let parts: Vec<_> = rows
                .chunks(chunk)
                .map(|part| scope.spawn(move || count(part)))
                .collect();
            let mut total = Sums::new(self.width);
            for part in parts {
                total.add(&part.join().expect("a counting thread panicked"));
            }
            total
        })
    }
}

struct Split {
    gain: f64,
    column: usize,
    bin: usize,
}

struct Leaf {
    rows: Vec<u32>,
    sums: Sums,
    gradient: f64,
    weight: f64,
    split: Option<Split>,
    node: usize,
}

// The best place to split a leaf: the column and bin that most lower the
// loss, or nothing when no split leaves both sides the smallest leaf's
// rows.
fn best_split(leaf: &Leaf, width: usize, settings: &Settings) -> Option<Split> {
    let score = |gradient: f64, weight: f64| gradient * gradient / (weight + settings.smoothing);
    let whole = score(leaf.gradient, leaf.weight);
    let total = leaf.rows.len() as u32;
    let mut best: Option<Split> = None;
    for column in 0..width {
        let (mut gradient, mut weight, mut count) = (0.0, 0.0, 0u32);
        for bin in 0..BINS - 1 {
            let at = column * BINS + bin;
            gradient += leaf.sums.gradient[at];
            weight += leaf.sums.weight[at];
            count += leaf.sums.count[at];
            if count < settings.smallest_leaf {
                continue;
            }
            if total - count < settings.smallest_leaf {
                break;
            }
            let gain = score(gradient, weight)
                + score(leaf.gradient - gradient, leaf.weight - weight)
                - whole;
            if gain > best.as_ref().map_or(1e-9, |b| b.gain) {
                best = Some(Split { gain, column, bin });
            }
        }
    }
    best
}

// Fits a forest. `bins` holds the bin of each value, `width` values a
// row. `rows` are the rows to fit on, so one binned matrix serves every
// fold. `labels` has one entry for each row of the matrix.
pub fn fit(
    bins: &[u8],
    edges: &[Vec<f32>],
    labels: &[bool],
    rows: &[u32],
    settings: &Settings,
) -> Forest {
    let width = edges.len();
    let positive = rows.iter().filter(|row| labels[**row as usize]).count() as f64;
    let share = (positive / rows.len().max(1) as f64).clamp(1e-3, 1.0 - 1e-3);
    let base = (share / (1.0 - share)).ln() as f32;
    let mut odds = vec![base; labels.len()];
    let mut gradient = vec![0f32; labels.len()];
    let mut weight = vec![0f32; labels.len()];
    let mut trees = Vec::with_capacity(settings.trees);
    for _ in 0..settings.trees {
        for &row in rows {
            let row = row as usize;
            let probability = 1.0 / (1.0 + (-odds[row]).exp());
            gradient[row] = probability - f32::from(labels[row]);
            weight[row] = (probability * (1.0 - probability)).max(1e-6);
        }
        let data = Data {
            bins,
            width,
            gradient: &gradient,
            weight: &weight,
            threads: settings.threads,
        };
        let (tree, leaves) = grow(&data, rows, edges, settings);
        for leaf in &leaves {
            if let Node::Leaf { value } = tree[leaf.node] {
                for &row in &leaf.rows {
                    odds[row as usize] += value;
                }
            }
        }
        trees.push(tree);
    }
    Forest { base, trees }
}

fn grow(
    data: &Data,
    rows: &[u32],
    edges: &[Vec<f32>],
    settings: &Settings,
) -> (Vec<Node>, Vec<Leaf>) {
    let leaf = |rows: Vec<u32>, sums: Sums, node: usize| {
        let gradient: f64 = rows
            .iter()
            .map(|row| data.gradient[*row as usize] as f64)
            .sum();
        let weight: f64 = rows
            .iter()
            .map(|row| data.weight[*row as usize] as f64)
            .sum();
        let mut leaf = Leaf {
            rows,
            sums,
            gradient,
            weight,
            split: None,
            node,
        };
        leaf.split = best_split(&leaf, data.width, settings);
        leaf
    };
    let mut tree = vec![Node::Leaf { value: 0.0 }];
    let mut leaves = vec![leaf(rows.to_vec(), data.sums(rows), 0)];
    while leaves.len() < settings.leaves {
        let gain = |leaf: &Leaf| leaf.split.as_ref().map_or(0.0, |split| split.gain);
        let Some(index) = (0..leaves.len())
            .filter(|index| leaves[*index].split.is_some())
            .max_by(|a, b| gain(&leaves[*a]).total_cmp(&gain(&leaves[*b])))
        else {
            break;
        };
        let parent = leaves.swap_remove(index);
        let split = parent.split.as_ref().expect("filtered above");
        let (left, right): (Vec<u32>, Vec<u32>) = parent.rows.iter().partition(|row| {
            data.bins[**row as usize * data.width + split.column] as usize <= split.bin
        });
        let (left_sums, right_sums) = if left.len() <= right.len() {
            let sums = data.sums(&left);
            let rest = parent.sums.less(&sums);
            (sums, rest)
        } else {
            let sums = data.sums(&right);
            (parent.sums.less(&sums), sums)
        };
        tree[parent.node] = Node::Split {
            column: split.column,
            threshold: edges[split.column][split.bin],
            left: tree.len(),
            right: tree.len() + 1,
        };
        leaves.push(leaf(left, left_sums, tree.len()));
        leaves.push(leaf(right, right_sums, tree.len() + 1));
        tree.push(Node::Leaf { value: 0.0 });
        tree.push(Node::Leaf { value: 0.0 });
    }
    for leaf in &leaves {
        let value = -leaf.gradient / (leaf.weight + settings.smoothing) * settings.rate as f64;
        tree[leaf.node] = Node::Leaf {
            value: value as f32,
        };
    }
    (tree, leaves)
}

#[cfg(test)]
mod tests {
    use super::*;

    // Rows of two columns from a fixed sequence, so the test needs no
    // random numbers.
    fn rows(count: usize) -> Vec<f32> {
        (0..count)
            .flat_map(|i| [(i * 37 % 101) as f32 / 100.0, (i * 53 % 89) as f32 / 88.0])
            .collect()
    }

    fn fitted(rule: impl Fn(f32, f32) -> bool) -> (Forest, Vec<f32>, Vec<bool>) {
        let values = rows(4000);
        let labels: Vec<bool> = values.chunks(2).map(|row| rule(row[0], row[1])).collect();
        let edges = edges(&values, 2);
        let binned = bins(&values, &edges);
        let all: Vec<u32> = (0..labels.len() as u32).collect();
        let settings = Settings {
            trees: 60,
            ..Settings::default()
        };
        (
            fit(&binned, &edges, &labels, &all, &settings),
            values,
            labels,
        )
    }

    fn accuracy(forest: &Forest, values: &[f32], labels: &[bool]) -> f64 {
        let right = values
            .chunks(2)
            .zip(labels)
            .filter(|(row, label)| (forest.probability(row) > 0.5) == **label)
            .count();
        right as f64 / labels.len() as f64
    }

    #[test]
    fn edges_fall_between_the_values_of_a_column_with_few_values() {
        assert_eq!(
            edges(&[0.0, 1.0, 0.0, 3.0, 0.0, 1.0], 1),
            vec![vec![0.5, 2.0]]
        );
        assert_eq!(
            bins(&[0.0, 1.0, 3.0, 9.0], &[vec![0.5, 2.0]]),
            vec![0, 1, 2, 2]
        );
    }

    #[test]
    fn edges_are_quantiles_of_a_column_with_many_values() {
        let values: Vec<f32> = (0..1000).map(|i| i as f32).collect();
        let edges = edges(&values, 1);
        assert_eq!(edges[0].len(), BINS - 1);
        assert_eq!(edges[0][31], 500.0);
    }

    #[test]
    fn fit_learns_a_threshold_on_one_column() {
        let (forest, values, labels) = fitted(|a, _| a > 0.6);
        assert!(accuracy(&forest, &values, &labels) > 0.99);
    }

    #[test]
    fn fit_learns_a_rule_that_needs_both_columns() {
        let (forest, values, labels) = fitted(|a, b| (a > 0.5) != (b > 0.5));
        assert!(accuracy(&forest, &values, &labels) > 0.97);
    }

    #[test]
    fn the_mean_of_forests_answers_with_the_mean_of_their_log_odds() {
        let leaf = |value: f32| Forest {
            base: value,
            trees: vec![vec![Node::Leaf { value }]],
        };
        let mean = mean(vec![leaf(1.0), leaf(3.0)]);
        // The two forests answer 2 and 6 in log odds, and the mean is 4.
        let expected = 1.0 / (1.0 + (-4.0f32).exp());
        assert!((mean.probability(&[]) - expected).abs() < 1e-6);
    }

    #[test]
    fn a_forest_survives_a_round_trip_through_json() {
        let (forest, values, _) = fitted(|a, _| a > 0.6);
        let loaded: Forest =
            serde_json::from_str(&serde_json::to_string(&forest).unwrap()).unwrap();
        assert_eq!(
            loaded.probability(&values[..2]),
            forest.probability(&values[..2])
        );
    }
}
