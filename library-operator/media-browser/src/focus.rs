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
