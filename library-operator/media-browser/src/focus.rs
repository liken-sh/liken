// Focus movement as pure functions of an index and a count, so the
// arrow logic is tested with numbers and never a window.

/// The next focus on a wall of `columns` columns; left and right walk
/// the whole order, up and down move by a row, and every move clamps inside
/// the count.
pub fn wall(index: usize, count: usize, columns: usize, key: &str) -> usize {
    if count == 0 {
        return 0;
    }
    let last = count - 1;
    match key {
        "left" => index.saturating_sub(1),
        "right" => (index + 1).min(last),
        "up" => {
            if index >= columns {
                index - columns
            } else {
                index
            }
        }
        "down" => {
            // A move down from the last row would leave the wall, and
            // a move into a shorter last row lands on its last slot.
            if index / columns < last / columns {
                (index + columns).min(last)
            } else {
                index
            }
        }
        _ => index,
    }
}

/// One run of slots in a wall of runs, such as one season's episodes:
/// where the run starts in the whole order, and how many slots it holds.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Run {
    /// The index the run's first slot has in the whole order.
    pub first: usize,
    /// How many slots the run holds.
    pub count: usize,
}

impl Run {
    // The index of the run's last slot.
    fn last(&self) -> usize {
        self.first + self.count - 1
    }

    // The slot of this run at this row and column, or the run's last slot
    // where that row is shorter.
    fn column(&self, row: usize, column: usize, columns: usize) -> usize {
        (self.first + row * columns + column).min(self.last())
    }

    // How many rows the run fills at this column count.
    fn rows(&self, columns: usize) -> usize {
        self.count.div_ceil(columns)
    }
}

/// The next focus on a wall of runs. Left and right stay inside the run
/// that holds focus. Up and down move by a row and cross into the run
/// above or below at the same column, so a divider between two runs stops
/// nothing.
pub fn sectioned(index: usize, runs: &[Run], columns: usize, key: &str) -> usize {
    let Some(here) = runs
        .iter()
        .position(|run| run.count > 0 && index >= run.first && index <= run.last())
    else {
        return index;
    };
    let run = runs[here];
    let local = index - run.first;
    let (row, column) = (local / columns, local % columns);
    match key {
        "left" => index.saturating_sub(1).max(run.first),
        "right" => (index + 1).min(run.last()),
        "up" if row > 0 => index - columns,
        "up" => match runs[..here].iter().rev().find(|run| run.count > 0) {
            Some(above) => above.column(above.rows(columns) - 1, column, columns),
            None => index,
        },
        "down" if row + 1 < run.rows(columns) => run.column(row + 1, column, columns),
        "down" => match runs[here + 1..].iter().find(|run| run.count > 0) {
            Some(below) => below.column(0, column, columns),
            None => index,
        },
        _ => index,
    }
}

/// The next focus on a row of buttons, controls, or strip posters; only
/// left and right move, clamped inside the count.
pub fn row(index: usize, count: usize, key: &str) -> usize {
    if count == 0 {
        return 0;
    }
    match key {
        "left" => index.saturating_sub(1),
        "right" => (index + 1).min(count - 1),
        _ => index,
    }
}

/// The next focus on a list; only up and down move, clamped inside
/// the count.
pub fn list(index: usize, count: usize, key: &str) -> usize {
    if count == 0 {
        return 0;
    }
    match key {
        "up" => index.saturating_sub(1),
        "down" => (index + 1).min(count - 1),
        _ => index,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn right_and_left_walk_the_wall() {
        assert_eq!(wall(0, 10, 3, "right"), 1);
        assert_eq!(wall(2, 10, 3, "right"), 3);
        assert_eq!(wall(9, 10, 3, "right"), 9);
        assert_eq!(wall(3, 10, 3, "left"), 2);
        assert_eq!(wall(0, 10, 3, "left"), 0);
    }

    #[test]
    fn up_and_down_move_by_a_row() {
        assert_eq!(wall(4, 10, 3, "down"), 7);
        assert_eq!(wall(4, 10, 3, "up"), 1);
        assert_eq!(wall(1, 10, 3, "up"), 1);
    }

    #[test]
    fn down_into_a_short_last_row_lands_on_the_last_slot() {
        assert_eq!(wall(8, 10, 3, "down"), 9);
    }

    #[test]
    fn down_from_the_last_row_stays() {
        assert_eq!(wall(9, 10, 3, "down"), 9);
    }

    #[test]
    fn an_empty_wall_holds_focus_at_zero() {
        assert_eq!(wall(0, 0, 3, "down"), 0);
    }

    #[test]
    fn an_unknown_key_moves_nothing_on_the_wall() {
        assert_eq!(wall(4, 10, 3, "x"), 4);
    }

    // Three seasons of eight, nine, and ten episodes, four across, which
    // is what a series page holds.
    fn seasons() -> Vec<Run> {
        vec![
            Run { first: 0, count: 8 },
            Run { first: 8, count: 9 },
            Run {
                first: 17,
                count: 10,
            },
        ]
    }

    #[test]
    fn left_and_right_stay_inside_one_run() {
        let runs = seasons();
        assert_eq!(sectioned(0, &runs, 4, "right"), 1);
        assert_eq!(sectioned(7, &runs, 4, "right"), 7);
        assert_eq!(sectioned(3, &runs, 4, "left"), 2);
        assert_eq!(sectioned(8, &runs, 4, "left"), 8);
    }

    #[test]
    fn up_and_down_move_by_a_row_inside_a_run() {
        let runs = seasons();
        assert_eq!(sectioned(0, &runs, 4, "down"), 4);
        assert_eq!(sectioned(6, &runs, 4, "up"), 2);
    }

    #[test]
    fn down_and_up_cross_into_the_next_run_at_the_same_column() {
        let runs = seasons();
        assert_eq!(sectioned(5, &runs, 4, "down"), 9);
        assert_eq!(sectioned(9, &runs, 4, "up"), 5);
        assert_eq!(sectioned(16, &runs, 4, "down"), 17);
    }

    #[test]
    fn a_crossing_into_a_shorter_row_lands_on_its_last_slot() {
        let runs = seasons();
        assert_eq!(sectioned(15, &runs, 4, "down"), 16);
        assert_eq!(sectioned(3, &runs, 4, "down"), 7);
        assert_eq!(sectioned(24, &runs, 4, "down"), 26);
    }

    #[test]
    fn the_first_and_the_last_row_of_a_page_hold_focus() {
        let runs = seasons();
        assert_eq!(sectioned(2, &runs, 4, "up"), 2);
        assert_eq!(sectioned(26, &runs, 4, "down"), 26);
        assert_eq!(sectioned(2, &runs, 4, "x"), 2);
    }

    #[test]
    fn a_run_with_no_slots_is_crossed_over() {
        let runs = vec![
            Run { first: 0, count: 4 },
            Run { first: 4, count: 0 },
            Run { first: 4, count: 4 },
        ];
        assert_eq!(sectioned(0, &runs, 4, "down"), 4);
        assert_eq!(sectioned(4, &runs, 4, "up"), 0);
    }

    #[test]
    fn a_page_with_no_runs_holds_focus() {
        assert_eq!(sectioned(0, &[], 4, "down"), 0);
    }

    #[test]
    fn a_row_moves_left_and_right_and_clamps() {
        assert_eq!(row(0, 3, "right"), 1);
        assert_eq!(row(2, 3, "right"), 2);
        assert_eq!(row(1, 3, "left"), 0);
        assert_eq!(row(0, 3, "left"), 0);
    }

    #[test]
    fn a_row_ignores_up_and_down() {
        assert_eq!(row(1, 3, "up"), 1);
        assert_eq!(row(1, 3, "down"), 1);
    }

    #[test]
    fn an_empty_row_holds_focus_at_zero() {
        assert_eq!(row(0, 0, "right"), 0);
    }

    #[test]
    fn a_list_moves_up_and_down_and_clamps() {
        assert_eq!(list(0, 3, "down"), 1);
        assert_eq!(list(2, 3, "down"), 2);
        assert_eq!(list(1, 3, "up"), 0);
        assert_eq!(list(0, 3, "up"), 0);
    }

    #[test]
    fn a_list_ignores_left_and_right() {
        assert_eq!(list(1, 3, "left"), 1);
        assert_eq!(list(1, 3, "right"), 1);
    }

    #[test]
    fn an_empty_list_holds_focus_at_zero() {
        assert_eq!(list(0, 0, "down"), 0);
    }
}
