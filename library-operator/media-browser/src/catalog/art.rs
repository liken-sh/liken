// Which file an item's art is, out of the list of every art file beside
// it. The catalog holds the primary art in one column and every art file
// in another, under the names the scanners write, so the choice of file
// is a rule over those names and not a column of its own.

/// The 16:9 art of one item, out of the list of every art file beside it:
/// the landscape file first, then the fanart, then the backdrop, which is
/// the other name the scanners of this catalog write the same 16:9 art
/// under. Nothing where the item holds none of the three.
pub fn landscape(arts: &[String]) -> Option<&str> {
    LANDSCAPE
        .iter()
        .find_map(|name| arts.iter().find(|art| named(art, name)))
        .map(String::as_str)
}

/// The art an episode's card draws in its 16:9 slot: the episode's own
/// still, and where the catalog holds none for it, the series' own art,
/// its 16:9 art first and its poster last. The fallback is the drawing
/// alone. The episode's still column stays empty, so the enricher's gap
/// stays open and the still itself takes the slot once the enricher
/// writes one. Empty where the series holds no art either, and the card
/// then draws its title on an empty slot.
pub fn still(episode: &str, series_art: &str, series_arts: &[String]) -> String {
    if !episode.is_empty() {
        return episode.to_string();
    }
    landscape(series_arts).unwrap_or(series_art).to_string()
}

// The names an item's 16:9 art is written under, in the order the ladder
// takes them.
const LANDSCAPE: [&str; 3] = ["landscape.jpg", "fanart.jpg", "backdrop.jpg"];

// Whether one art path is the file of this name. The paths are relative
// to the library's volume, so the name is the last part of the path.
fn named(art: &str, name: &str) -> bool {
    art.rsplit('/').next().is_some_and(|file| file == name)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn arts(paths: &[&str]) -> Vec<String> {
        paths.iter().map(|path| (*path).to_string()).collect()
    }

    #[test]
    fn an_item_draws_its_landscape_then_its_fanart_then_its_backdrop() {
        for (files, drawn) in [
            (
                &["Show/poster.jpg", "Show/fanart.jpg", "Show/landscape.jpg"][..],
                Some("Show/landscape.jpg"),
            ),
            (
                &["Show/poster.jpg", "Show/backdrop.jpg", "Show/fanart.jpg"][..],
                Some("Show/fanart.jpg"),
            ),
            (
                &["Show/poster.jpg", "Show/backdrop.jpg"][..],
                Some("Show/backdrop.jpg"),
            ),
            (&["Show/poster.jpg", "Show/clearlogo.png"][..], None),
            (&[][..], None),
        ] {
            assert_eq!(landscape(&arts(files)), drawn);
        }
    }

    #[test]
    fn a_name_a_title_carries_is_not_the_art_of_that_name() {
        assert_eq!(landscape(&arts(&["Landscape (1999)/poster.jpg"])), None);
    }

    #[test]
    fn an_episode_with_a_still_of_its_own_draws_it() {
        let files = arts(&["Show/landscape.jpg"]);
        assert_eq!(
            still("Show/Season 11/S11E01-thumb.jpg", "Show/poster.jpg", &files),
            "Show/Season 11/S11E01-thumb.jpg"
        );
    }

    #[test]
    fn an_episode_with_no_still_draws_the_art_of_its_series() {
        for (files, drawn) in [
            (
                &["Show/landscape.jpg", "Show/fanart.jpg"][..],
                "Show/landscape.jpg",
            ),
            (&["Show/fanart.jpg"][..], "Show/fanart.jpg"),
            (&["Show/clearlogo.png"][..], "Show/poster.jpg"),
            (&[][..], "Show/poster.jpg"),
        ] {
            assert_eq!(still("", "Show/poster.jpg", &arts(files)), drawn);
        }
    }

    #[test]
    fn an_episode_of_a_series_with_no_art_draws_nothing() {
        assert_eq!(still("", "", &[]), "");
    }
}
