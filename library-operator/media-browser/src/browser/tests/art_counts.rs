use super::*;

#[test]
fn the_browser_reports_its_art_stores_counts() {
    let browser = Browser::new(
        Fake::default(),
        NoArt {
            counts: ArtCounts {
                from_cache: 7,
                from_source: 11,
            },
            ..NoArt::default()
        },
    );

    assert_eq!(
        browser.art_counts(),
        ArtCounts {
            from_cache: 7,
            from_source: 11,
        }
    );
}
