use super::*;

#[test]
fn the_browser_reports_its_poster_stores_counts() {
    let browser = Browser::new(
        Fake::default(),
        NoPosters {
            counts: PosterCounts {
                from_cache: 7,
                from_source: 11,
            },
            ..NoPosters::default()
        },
    );

    assert_eq!(
        browser.poster_counts(),
        PosterCounts {
            from_cache: 7,
            from_source: 11,
        }
    );
}
