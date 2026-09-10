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

#[test]
fn the_browser_reports_its_art_stores_cache_bytes() {
    let browser = Browser::new(
        Fake::default(),
        NoArt {
            cache_bytes: Some(4_096),
            ..NoArt::default()
        },
    );

    assert_eq!(browser.art_cache_bytes(), Some(4_096));
}

#[test]
fn a_store_with_no_cache_of_its_own_reports_none() {
    let browser = Browser::new(Fake::default(), NoArt::default());
    assert_eq!(browser.art_cache_bytes(), None);
}
