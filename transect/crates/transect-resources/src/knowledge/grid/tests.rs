use rstest::rstest;

use super::*;

fn grid(sketch: &str) -> Grid {
    let rows = sketch.lines().map(|line| {
        let cells = line.split_whitespace();
        cells
            .map(|cell| (cell != ".").then(|| cell.to_string()))
            .collect()
    });
    Grid {
        rows: rows.collect(),
    }
}

const PATH: &str = "hopper sifter bin\nscoop sifter .\n. store .";

#[rstest]
#[case::to_the_right("hopper", "sifter", Some((Edge::Right, Edge::Left)))]
#[case::to_the_left("bin", "sifter", Some((Edge::Left, Edge::Right)))]
#[case::to_an_area_across_two_rows("scoop", "sifter", Some((Edge::Right, Edge::Left)))]
#[case::across_a_column_between("hopper", "bin", Some((Edge::Right, Edge::Left)))]
#[case::down_a_shared_column("sifter", "store", Some((Edge::Bottom, Edge::Top)))]
#[case::up_a_shared_column("store", "sifter", Some((Edge::Top, Edge::Bottom)))]
#[case::to_a_corner("hopper", "store", Some((Edge::Right, Edge::Left)))]
#[case::a_role_not_placed("hopper", "chute", None)]
fn joins_the_facing_edges_of_two_areas(
    #[case] from: &str,
    #[case] to: &str,
    #[case] expected: Option<(Edge, Edge)>,
) {
    assert_eq!(grid(PATH).facing(from, to), expected);
}
