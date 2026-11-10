// The logo a play list carries, which the player's header draws in place
// of the title: a film's own, a trailer's title's, and an episode's
// series'. A title with no logo file carries none, and the header then
// draws the title as text.

use super::*;

// One title's logo file, in the role the scanner gives a clearlogo.png.
fn insert_logo(path: &Path, library: &str, file: &str, item: &str) {
    insert_file(path, library, file, item, "image", "logo");
}

// A film with its main file and its trailer, and the logo file where
// `logo` names one.
fn a_film(path: &Path, logo: Option<&str>) {
    insert_movie(
        path,
        "default/films",
        "movie:tmdb:7001",
        "The Lantern",
        "lantern",
    );
    insert_main_file(
        path,
        "default/films",
        "The Lantern/The Lantern.mkv",
        "movie:tmdb:7001",
    );
    insert_file(
        path,
        "default/films",
        "The Lantern/trailer.mkv",
        "movie:tmdb:7001",
        "video",
        "trailer",
    );
    if let Some(file) = logo {
        insert_logo(path, "default/films", file, "movie:tmdb:7001");
    }
}

const FILM_LOGO: &str = "The Lantern/clearlogo.png";

fn trailer_chosen() -> Selection {
    Selection::Trailer {
        id: "movie:tmdb:7001".into(),
    }
}

#[test]
fn a_film_and_its_trailer_carry_the_films_logo() {
    for (logo, carried) in [(Some(FILM_LOGO), FILM_LOGO), (None, "")] {
        let dir = TempDir::new().unwrap();
        let path = fixture(&dir);
        a_film(&path, logo);

        let mut source = LocalCatalog::new(&path, NO_AGENT);
        for chosen in [movie_chosen(), trailer_chosen()] {
            let items = source.play("default/films", &chosen);
            assert_eq!(items[0].presentation.logo, carried, "{chosen:?}");
        }
    }
}

#[test]
fn an_episode_carries_its_series_logo() {
    for (logo, carried) in [
        (Some("Harrow/clearlogo.png"), "Harrow/clearlogo.png"),
        (None, ""),
    ] {
        let dir = TempDir::new().unwrap();
        let path = fixture(&dir);
        a_season(&path);
        if let Some(file) = logo {
            insert_logo(&path, "default/shows", file, SERIES);
        }

        let mut source = LocalCatalog::new(&path, NO_AGENT);
        let items = source.play("default/shows", &episode_chosen(2));
        assert_eq!(items[0].presentation.logo, carried);
    }
}
