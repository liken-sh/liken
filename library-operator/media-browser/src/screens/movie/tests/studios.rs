// The studios on the movie page's foot: each one a stop under the
// stripes, which opens that studio's wall.

use super::*;

#[test]
fn down_from_the_last_stripe_reaches_the_first_studio_and_up_returns() {
    let (mut page, mut source) = credited();
    page.focus = Focus::Stripe(1, 0);
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Studio(0));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Stripe(1, 0));
}

#[test]
fn down_from_a_stripe_over_another_moves_to_that_stripe() {
    let (mut page, mut source) = credited();
    page.focus = Focus::Stripe(0, 0);
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Stripe(1, 0));
}

#[test]
fn left_and_right_move_across_the_studios_and_down_holds() {
    let (mut page, mut source) = credited();
    page.focus = Focus::Studio(0);
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::Studio(1));
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::Studio(1));
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Studio(1));
    page.key("left", &mut source);
    assert_eq!(page.focus, Focus::Studio(0));
}

#[test]
fn a_select_on_a_studio_opens_that_studio_s_wall_over_the_page() {
    let (mut page, mut source) = credited();
    page.focus = Focus::Studio(1);
    let Step::Open(Screen::Wall(wall)) = page.key("enter", &mut source) else {
        panic!("a studio opens a wall");
    };
    assert_eq!(wall.slots.query, Query::studio("Another Studio"));
}

#[test]
fn a_select_past_the_last_studio_opens_nothing() {
    let (mut page, mut source) = credited();
    page.focus = Focus::Studio(9);
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
}

// A film that credits nobody reaches its studios from the marks, and up
// from them returns to the marks.
#[test]
fn a_film_with_no_stripe_reaches_its_studios_from_the_marks() {
    let (mut page, mut source) = page(Films::default());
    page.key("down", &mut source);
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Studio(0));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Marks(0));
}

#[test]
fn the_studios_follow_the_set_strip_and_the_franchise_strip() {
    let (mut set, mut source) = page(Films {
        set: true,
        ..Films::default()
    });
    set.focus = Focus::Strip(1);
    set.key("down", &mut source);
    assert_eq!(set.focus, Focus::Studio(0));

    let (mut ordered, mut source) = page(Films {
        franchise: true,
        ..Films::default()
    });
    ordered.focus = Focus::Franchise(0, Place::Member(1));
    ordered.key("down", &mut source);
    assert_eq!(ordered.focus, Focus::Studio(0));
}

#[test]
fn a_reread_holds_the_studio_and_lands_on_play_where_none_is_left() {
    let (mut page, mut source) = credited();
    page.focus = Focus::Studio(9);
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Studio(1));

    source.unproduced = true;
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Buttons(0));
}
