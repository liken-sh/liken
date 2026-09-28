// Where the room and each person in it stand in one order, and the bars
// under its members, over a fake store of plays.

use super::*;
use crate::screens::franchise::tests::{CYCLE, FIRST, Night, ORDERS, Orders, RUNTIME, SHOW};

// The room the reads are made for, one person to a letter.
const ROOM: [&str; 3] = ["A", "B", "C"];

fn room() -> Vec<String> {
    ROOM.iter().map(|person| person.to_string()).collect()
}

// The page of the split order, read for the room over these plays.
fn page(nights: &[Night]) -> Franchise {
    let mut source = Orders {
        split: true,
        nights: nights.to_vec(),
        ..Orders::default()
    };
    let mut page = Franchise::open(ORDERS, CYCLE, &mut source).expect("the fake holds the order");
    read(&mut page, &mut source, &room(), &room());
    page
}

// One play of one work: its aired numbers, the people on it, the second
// it was recorded, and how far it reached.
fn night(
    work: &'static str,
    numbers: (i64, i64),
    people: &'static str,
    recorded: i64,
    position: i64,
) -> Night {
    Night {
        work,
        numbers,
        people,
        recorded,
        position,
    }
}

// The room partway into the second run, one person alone and unfinished
// on the first film, one person alone where the room stands, and one
// person alone and finished with the last leaf.
fn nights() -> Vec<Night> {
    vec![
        night(SHOW, (2, 2), "ABC", 40, RUNTIME / 2),
        night(FIRST, (0, 0), "A", 30, RUNTIME / 4),
        night(SHOW, (2, 2), "B", 20, RUNTIME / 2),
        night(SHOW, (2, 5), "C", 10, RUNTIME),
    ]
}

#[test]
fn the_room_stands_on_the_row_of_the_run_it_is_partway_into() {
    let page = page(&nights());

    assert_eq!(page.marks.room, Some(3));
    assert_eq!(page.marks.letters, room());
}

#[test]
fn a_person_alone_draws_a_circle_only_where_their_own_thread_stands_elsewhere() {
    let page = page(&nights());

    assert_eq!(page.marks.solo, [("A".to_string(), 0)]);
}

#[test]
fn an_order_no_play_names_draws_no_marker_and_earns_no_column() {
    let page = page(&[]);

    assert_eq!(page.marks.room, None);
    assert_eq!(page.marks.letters, [] as [String; 0]);
    assert_eq!(page.marks.solo, []);
    assert!(!page.marks.marked());
}

#[test]
fn the_first_read_lands_focus_on_the_rooms_row_and_later_reads_leave_it() {
    let mut source = Orders {
        split: true,
        nights: nights(),
        ..Orders::default()
    };
    let mut page = Franchise::open(ORDERS, CYCLE, &mut source).expect("the fake holds the order");
    assert_eq!(page.focus, 0);

    read(&mut page, &mut source, &room(), &room());
    assert_eq!(page.focus, 3);

    page.key("up", &mut source);
    read(&mut page, &mut source, &room(), &room());
    assert_eq!(page.focus, 2);
}

#[test]
fn a_reread_holds_the_marks_and_the_row_focus_was_placed_on() {
    let mut source = Orders {
        split: true,
        nights: nights(),
        ..Orders::default()
    };
    let mut page = Franchise::open(ORDERS, CYCLE, &mut source).expect("the fake holds the order");
    read(&mut page, &mut source, &room(), &room());
    page.key("up", &mut source);
    page.reread(&mut source);

    assert_eq!(page.focus, 2);
    assert_eq!(page.marks.room, Some(3));
    assert!(page.placed);
}

#[test]
fn every_held_member_draws_the_bar_the_room_reached_and_a_gap_draws_none() {
    let finished = |episode| night(SHOW, (2, episode), "ABC", 10 + episode, RUNTIME);
    let mut nights = vec![
        night(FIRST, (0, 0), "ABC", 10, RUNTIME / 4),
        night(SHOW, (2, 4), "ABC", 20, RUNTIME / 2),
    ];
    nights.extend((1..=3).map(finished));
    let mut source = Orders {
        nights,
        ..Orders::default()
    };
    let mut page = Franchise::open(ORDERS, CYCLE, &mut source).expect("the fake holds the order");
    read(&mut page, &mut source, &room(), &room());

    assert_eq!(
        page.marks.bars,
        [Some(0.25), None, None, None, Some(0.7), None]
    );
    assert!(!page.rows[3].cell.held());
}

#[test]
fn a_run_counts_a_finished_episode_whole_and_the_one_in_the_middle_by_its_share() {
    let reached = |position: i64| Progress {
        position,
        duration: RUNTIME,
        finished: crate::catalog::progress::finished(position, RUNTIME),
        ..Progress::default()
    };
    let cases = [
        (vec![RUNTIME, RUNTIME, RUNTIME, RUNTIME / 2], 5, Some(0.7)),
        (vec![RUNTIME / 2], 5, Some(0.1)),
        (vec![RUNTIME], 1, Some(1.0)),
        (Vec::new(), 5, None),
        (vec![RUNTIME], 0, None),
    ];
    for (positions, covered, share) in cases {
        let inside: Vec<Progress> = positions.iter().map(|at| reached(*at)).collect();
        assert_eq!(run(&inside, covered), share, "{positions:?} of {covered}");
    }
}

#[test]
fn a_film_draws_the_share_of_its_running_time_the_latest_play_reached() {
    let played = |recorded: i64, position: i64| Resume {
        progress: Progress {
            position,
            duration: RUNTIME,
            recorded,
            ..Progress::default()
        },
        ..Resume::default()
    };

    assert_eq!(film(&[played(10, RUNTIME / 4)]), Some(0.25));
    assert_eq!(
        film(&[played(10, RUNTIME / 4), played(20, RUNTIME / 2)]),
        Some(0.5)
    );
    assert_eq!(film(&[]), None);
}
