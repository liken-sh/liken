use super::*;
use crate::focus::Run;
use crate::screens::franchise::strips::Place;

// A frame of the size the screens this browser draws on hold.
const WIDTH: f32 = 1920.0;
const HEIGHT: f32 = 1080.0;

// Three seasons of eight, nine, and ten episodes.
fn seasons() -> Vec<Season> {
    [(1, 0, 8), (2, 8, 9), (3, 17, 10)]
        .iter()
        .map(|(number, first, count)| Season {
            number: *number,
            name: format!("Season {number} (2004)"),
            run: Run {
                first: *first,
                count: *count,
            },
        })
        .collect()
}

fn frame() -> Rectangle {
    area(0.0, 0.0, WIDTH, HEIGHT)
}

fn cells() -> wall::Cells {
    wall::lined(WIDTH, wall::STILL, COLUMNS, card::LINES)
}

fn layout() -> Layout {
    Layout::of(&seasons(), cells(), 0, 0, false, 0.0)
}

// The same wall with the three stripes of a title's credits
// after it.
fn with_stripes() -> Layout {
    Layout::of(&seasons(), cells(), 0, 3, false, 0.0)
}

// The same wall, its stripes, and a foot of two lines under them.
fn with_foot() -> Layout {
    Layout::of(&seasons(), cells(), 0, 3, false, FOOT_LINES)
}

// The height of a foot of two lines.
const FOOT_LINES: f32 = 48.0;

#[test]
fn the_header_takes_the_top_of_the_frame_and_the_wall_takes_the_rest() {
    let header = header(frame());
    let region = region(frame());
    assert_eq!(header.y, 0.0);
    assert_eq!(region.y, header.height);
    assert_eq!(header.height + region.height, HEIGHT);
}

#[test]
fn an_episodes_row_and_its_status_line_fit_inside_the_header() {
    let foot = episode_row() + crate::views::buttons::HEIGHT + crate::views::watch::height();
    assert!(foot <= head() - FOOT, "{foot} past {}", head() - FOOT);
}

#[test]
fn the_header_is_as_tall_as_the_blocks_it_draws() {
    assert_eq!(header(frame()).height, head());
    assert!(head() < HEIGHT / 2.0, "{}", head());
}

#[test]
fn a_still_carries_the_two_lines_of_a_card_under_it() {
    assert_eq!(
        cells().height,
        wall::cells(WIDTH, wall::STILL, COLUMNS).height + text::height(1, look::FACE)
    );
}

#[test]
fn a_divider_stands_at_the_top_of_its_own_season_while_that_top_is_in_view() {
    let layout = layout();
    let region = region(frame());
    let first = divider_box(region, &layout.bands[0], 0.0);
    assert_eq!(first.y, region.y + layout.bands[0].top);
    assert_eq!(first.x, region.x);
    assert_eq!(first.width, region.width);
    assert_eq!(first.height, divider::HEIGHT);
}

#[test]
fn a_divider_holds_at_the_top_of_the_wall_while_its_rows_scroll_under_it() {
    let layout = layout();
    let region = region(frame());
    let seasons = seasons();
    let offset = layout
        .rest(Focus::Still(7), &seasons, region.height + head())
        .offset;
    assert!(offset > layout.bands[0].top);
    assert_eq!(divider_box(region, &layout.bands[0], offset).y, region.y);
}

#[test]
fn the_next_season_pushes_the_divider_over_it_off() {
    let layout = layout();
    let region = region(frame());
    let band = layout.bands[0];
    // The scroll that leaves the first season a divider short of its
    // foot, where the second season's rows reach the top.
    let offset = band.top + band.height - divider::HEIGHT / 2.0;
    let held = divider_box(region, &band, offset);
    assert!(held.y < region.y);
    assert_eq!(held.y + held.height, region.y + divider::HEIGHT / 2.0);
}

#[test]
fn the_stills_draw_under_the_band_a_held_divider_keeps() {
    let region = region(frame());
    let stills = stills(region);
    assert_eq!(stills.y, region.y + divider::HEIGHT);
    assert_eq!(stills.y + stills.height, region.y + region.height);
    assert_eq!(stills.x, region.x);
    assert_eq!(stills.width, region.width);
}

#[test]
fn a_divider_a_row_of_stills_and_the_art_of_the_next_fit_under_the_header() {
    let cells = cells();
    let takes = HEAD + divider::HEIGHT + REACH + cells.height + cells.poster_height;
    assert!(takes <= region(frame()).height, "{takes}");
}

#[test]
fn the_wall_starts_a_clear_gap_under_the_header() {
    const { assert!(HEAD > REACH) };
    assert_eq!(layout().bands[0].top, HEAD);
}

#[test]
fn every_season_takes_a_divider_and_its_rows() {
    let layout = layout();
    assert_eq!(layout.bands.len(), 3);
    assert_eq!(layout.bands[0].rows, 2);
    assert_eq!(layout.bands[1].rows, 3);
    assert_eq!(layout.bands[1].top, HEAD + layout.bands[0].height);
    assert_eq!(layout.content, layout.bands[2].top + layout.bands[2].height);
}

#[test]
fn the_stripes_follow_the_last_season_of_the_wall() {
    let plain = layout();
    let layout = with_stripes();
    assert_eq!(layout.bands, plain.bands);
    assert_eq!(layout.stripes.len(), 3);
    assert_eq!(layout.stripes[0], plain.content + STRIPE_GAP);
    assert_eq!(
        layout.stripes[1],
        layout.stripes[0] + people::HEIGHT + STRIPE_GAP
    );
    assert_eq!(
        layout.content,
        layout.stripes[2] + people::HEIGHT + STRIPE_GAP
    );
}

// The same wall with one franchise strip between the seasons and
// the stripes.
fn with_franchise() -> Layout {
    Layout::of(&seasons(), cells(), 1, 3, false, 0.0)
}

#[test]
fn a_franchise_strip_stands_as_tall_as_the_card_its_slots_draw() {
    assert_eq!(strip_height(), strip::height(card::LINES));
    assert!(strip_height() > strip::height(1));
}

#[test]
fn a_franchise_strip_stands_between_the_last_season_and_the_stripes() {
    let plain = with_stripes();
    let layout = with_franchise();
    assert_eq!(layout.bands, plain.bands);
    assert_eq!(layout.franchises.len(), 1);
    assert_eq!(layout.franchises[0], plain.stripes[0]);
    assert_eq!(
        layout.stripes[0],
        layout.franchises[0] + strip_height() + STRIPE_GAP
    );
    assert!((layout.content - plain.content - strip_height() - STRIPE_GAP).abs() < 1e-3);
}

// The same wall with its stripes, a More from strip, and a foot.
fn with_more() -> Layout {
    Layout::of(&seasons(), cells(), 0, 3, true, FOOT_LINES)
}

#[test]
fn the_more_from_strip_stands_between_the_last_stripe_and_the_foot() {
    let plain = with_foot();
    let layout = with_more();
    let top = layout.more.expect("the layout places the strip");
    assert_eq!(plain.more, None);
    assert_eq!(top, plain.stripes[2] + people::HEIGHT + STRIPE_GAP);
    assert_eq!(layout.foot, top + strip_height() + STRIPE_GAP);
    assert!((layout.content - plain.content - strip_height() - STRIPE_GAP).abs() < 1e-3);
}

// Where a block that starts this far down the wall draws in the frame
// with the page at rest: under the header's foot, which has risen by the
// lift, and up by the wall's own scroll.
fn drawn(top: f32, rest: Rest) -> f32 {
    head() - rest.lift + top - rest.offset
}

// A series with no stripe still keeps a gap between the strip and the
// foot, and the strip pulls the foot into view.
#[test]
fn the_more_from_strip_pulls_the_foot_into_view() {
    for layout in [
        with_more(),
        Layout::of(&seasons(), cells(), 0, 0, true, FOOT_LINES),
    ] {
        let top = layout.more.expect("the layout places the strip");
        assert_eq!(layout.foot, top + strip_height() + STRIPE_GAP);
        let rest = layout.rest(Focus::More(0), &seasons(), HEIGHT);
        assert!(drawn(top, rest) >= VIEWPORT_TOP);
        assert!(drawn(layout.foot + FOOT_LINES, rest) <= HEIGHT);
    }
    assert_eq!(
        layout().rest(Focus::More(0), &seasons(), HEIGHT),
        Rest::default()
    );
}

#[test]
fn the_page_scrolls_the_focused_franchise_strip_into_view() {
    let layout = with_franchise();
    let rest = layout.rest(Focus::Franchise(0, Place::Heading), &seasons(), HEIGHT);
    assert!(drawn(layout.franchises[0], rest) >= VIEWPORT_TOP);
    assert!(drawn(layout.franchises[0] + strip_height(), rest) <= HEIGHT);
    assert_eq!(
        layout.rest(Focus::Franchise(9, Place::Heading), &seasons(), HEIGHT),
        Rest::default()
    );
}

#[test]
fn the_foot_follows_the_last_stripe_and_ends_the_wall() {
    let plain = with_stripes();
    let layout = with_foot();
    assert_eq!(layout.stripes, plain.stripes);
    assert_eq!(layout.foot, plain.content);
    assert_eq!(layout.content, layout.foot + FOOT_LINES + STRIPE_GAP);
}

#[test]
fn a_series_with_no_episodes_says_so_where_its_first_season_would_start() {
    let empty = Layout::of(&[], cells(), 0, 3, false, FOOT_LINES);
    let line = empty.empty.expect("a wall of no seasons draws the line");

    assert_eq!(line, HEAD);
    assert!(empty.stripes[0] >= line + text::height(1, look::FACTS) + STRIPE_GAP);
    assert_eq!(
        empty.empty,
        Layout::of(&[], cells(), 0, 0, false, 0.0).empty
    );
    assert_eq!(layout().empty, None);
}

#[test]
fn a_wall_with_no_stripe_still_leaves_a_gap_over_its_foot() {
    let layout = Layout::of(&seasons(), cells(), 0, 0, false, FOOT_LINES);
    assert_eq!(
        layout.foot,
        layout.bands[2].top + layout.bands[2].height + STRIPE_GAP
    );
}

#[test]
fn the_last_stripe_pulls_the_foot_into_view() {
    let layout = with_foot();
    let rest = layout.rest(Focus::Stripe(2, 0), &seasons(), HEIGHT);
    assert!(drawn(layout.foot + FOOT_LINES, rest) <= HEIGHT);
}

// Focus on a stripe rests it whole between the strip's line and the foot
// of the frame. The header has scrolled up out of the way first, as far
// as the line and no further, and the wall's own scroll does the rest.
#[test]
fn the_page_scrolls_the_focused_stripe_into_view() {
    let layout = with_stripes();
    let over = head() - VIEWPORT_TOP;
    let mut pages = Vec::new();
    for stripe in 0..3 {
        let rest = layout.rest(Focus::Stripe(stripe, 0), &seasons(), HEIGHT);
        assert!(drawn(layout.stripes[stripe], rest) >= VIEWPORT_TOP);
        assert!(drawn(layout.stripes[stripe] + people::HEIGHT, rest) <= HEIGHT);
        assert_eq!(rest.lift, over);
        pages.push(rest.lift + rest.offset);
    }
    let wall = layout.rest(Focus::Still(26), &seasons(), HEIGHT);
    assert_eq!(wall.lift, 0.0);
    assert!(pages[0] > wall.offset);
    assert!(pages[2] > pages[1]);
    assert_eq!(pages[2], over + layout.content - (HEIGHT - VIEWPORT_TOP));
}

// A page whose rows fit under a header lifted part of the way lifts it
// only that far, as a movie's short page scrolls its head part way.
#[test]
fn a_short_page_lifts_the_header_only_as_far_as_its_rows_need() {
    let layout = Layout::of(&[], cells(), 0, 2, false, 0.0);
    let rest = layout.rest(Focus::Stripe(1, 0), &[], HEIGHT);
    assert!(
        rest.lift > 0.0 && rest.lift < head() - VIEWPORT_TOP,
        "{rest:?}"
    );
    assert_eq!(rest.offset, 0.0);
    assert_eq!(drawn(layout.content, rest), HEIGHT);
}

#[test]
fn the_rows_start_under_the_divider() {
    let layout = layout();
    assert_eq!(
        layout.bands[0].rows_top,
        layout.bands[0].top + divider::HEIGHT + REACH
    );
}

#[test]
fn the_header_stands_still_while_the_wall_scrolls_under_it() {
    let layout = layout();
    let region = region(frame());
    assert_eq!(
        layout
            .rest(Focus::Still(0), &seasons(), region.height + head())
            .offset,
        0.0
    );
    let deep = layout
        .rest(Focus::Still(20), &seasons(), region.height + head())
        .offset;
    assert!(deep > 0.0);
    assert_eq!(header(frame()), header(frame()));
    assert_eq!(super::region(frame()), region);
}

#[test]
fn the_wall_scrolls_as_focus_moves_down() {
    let layout = layout();
    let height = region(frame()).height;
    assert_eq!(
        layout
            .rest(Focus::Still(0), &seasons(), height + head())
            .offset,
        0.0
    );
    let deep = layout
        .rest(Focus::Still(20), &seasons(), height + head())
        .offset;
    let deeper = layout
        .rest(Focus::Still(26), &seasons(), height + head())
        .offset;
    assert!(deep > 0.0);
    assert!(deeper > deep);
}

#[test]
fn the_scroll_stops_at_the_foot_of_the_wall() {
    let layout = layout();
    let height = region(frame()).height;
    assert_eq!(
        layout
            .rest(Focus::Still(26), &seasons(), height + head())
            .offset,
        layout.content - height
    );
}

#[test]
fn a_wall_shorter_than_its_region_never_scrolls() {
    let layout = Layout::of(&seasons()[..1], cells(), 0, 0, false, 0.0);
    assert_eq!(
        layout
            .rest(Focus::Still(7), &seasons()[..1], 2000.0 + head())
            .offset,
        0.0
    );
}

#[test]
fn a_series_with_no_episodes_never_scrolls() {
    let layout = Layout::of(&[], cells(), 0, 0, false, 0.0);
    assert_eq!(layout.content, HEAD + text::height(1, look::FACTS));
    assert_eq!(
        layout.rest(Focus::Still(0), &[], 1080.0 + head()).offset,
        0.0
    );
}

#[test]
fn a_band_shows_only_where_the_region_reaches_it() {
    let layout = layout();
    let height = region(frame()).height;
    assert!(layout.bands[0].shows(0.0, height));
    assert!(!layout.bands[2].shows(0.0, height));
    assert!(layout.bands[2].shows(layout.content - height, height));
}

#[test]
fn the_rail_reaches_this_measure_as_the_still_its_bar_lands_on() {
    let layout = layout();
    let height = region(frame()).height;
    assert_eq!(
        layout
            .rest(Focus::Rail(2), &seasons(), height + head())
            .offset,
        0.0
    );
}

#[test]
fn the_wall_stands_at_its_top_while_the_button_row_holds_focus() {
    let layout = layout();
    let height = region(frame()).height;
    assert_eq!(
        layout
            .rest(Focus::Buttons(0), &seasons(), height + head())
            .offset,
        0.0
    );
}

#[test]
fn the_rail_is_counted_against_the_region_the_page_draws_in() {
    let drawn = region(area(0.0, 0.0, 1920.0, 1080.0));
    assert_eq!(rail_region().height, drawn.height);
    assert_eq!(rail_region().height, 1080.0 - head());
}

#[test]
fn the_rail_draws_in_a_clip_that_holds_the_mark_on_its_first_bar() {
    let region = region(frame());
    let clip = rail_clip(region);
    assert!(clip.y < region.y);
    assert!(clip.y + clip.height > region.y + region.height);
    assert!(clip.x < region.x);
    assert!(clip.x + clip.width > region.x + region.width);
    assert_eq!(region.y - clip.y, look::MARK_GAP + look::MARK);
}
