//! The fields that the library declares for the current item, such as
//! its title, logo, and marks. The command sidecar sends them as one JSON
//! object, the block, in a `script-message` when the playlist reaches a
//! new item. The title, artist, album, and music year fall back to what
//! mpv reads from the file when the block does not declare them.

use serde_json::{Map, Value};

use crate::film::Film;
use crate::marks::Marks;

/// The kind of item that the block's `type` declares. The scrubber, the
/// strip, and the header change their layout by it.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum Kind {
    #[default]
    Other,
    /// A still photo.
    Image,
    /// An album.
    Music,
}

/// The block for the current item. `receive` parses the kind, the line
/// under a series title, and the marks once, because the display reads
/// them on every redraw and they change only when a new block arrives.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Presentation {
    block: Map<String, Value>,
    kind: Kind,
    second: Option<String>,
    marks: Marks,
}

impl Presentation {
    /// Replace the block with a JSON string. Text that is not a JSON
    /// object, including an empty string, reads as an empty block.
    pub fn receive(&mut self, text: &str) {
        self.block = serde_json::from_str::<Value>(text)
            .ok()
            .and_then(|parsed| match parsed {
                Value::Object(block) => Some(block),
                _ => None,
            })
            .unwrap_or_default();
        self.kind = match self.word("type") {
            Some("image") => Kind::Image,
            Some("music") => Kind::Music,
            _ => Kind::Other,
        };
        self.second = crate::header::second_line(self);
        self.marks = Marks::parse(self.field("marks"));
    }

    /// The spans of the intro, the recap, the credits, and the preview.
    /// An item with no marks has no skip control, and its up-next card
    /// appears by the time remaining instead of at the credits.
    pub fn marks(&self) -> &Marks {
        &self.marks
    }

    /// The block's title, or else mpv's `media-title` for the file.
    pub fn title(&self, film: &Film) -> Option<String> {
        self.word("title")
            .map(str::to_string)
            .or_else(|| film.media_title.clone())
    }

    /// The block's `type` field, named `kind` because `type` is a Rust
    /// keyword.
    pub fn kind(&self) -> Kind {
        self.kind
    }

    /// A still photo has no timeline, so the display draws no scrubber.
    pub fn is_image(&self) -> bool {
        self.kind == Kind::Image
    }

    /// An album is one audio stream with no track to choose, so the
    /// display draws no strip.
    pub fn is_music(&self) -> bool {
        self.kind == Kind::Music
    }

    /// The line under the title of a series: the season, the episode,
    /// the episode title, and the date.
    pub fn second(&self) -> Option<&str> {
        self.second.as_deref()
    }

    pub fn hint(&self) -> Option<&str> {
        self.word("hint")
    }

    /// `trailer` for a trailer, and `None` for the work itself.
    pub fn role(&self) -> Option<&str> {
        self.word("role")
    }

    pub fn series(&self) -> Option<&str> {
        self.word("series")
    }

    pub fn season(&self) -> Option<&Value> {
        self.field("season")
    }

    pub fn episode(&self) -> Option<&Value> {
        self.field("episode")
    }

    pub fn episode_title(&self) -> Option<&str> {
        self.word("episodeTitle")
    }

    pub fn artist(&self, film: &Film) -> Option<String> {
        self.word("artist")
            .or_else(|| film.tag("artist"))
            .map(str::to_string)
    }

    pub fn album(&self, film: &Film) -> Option<String> {
        self.word("album")
            .or_else(|| film.tag("album"))
            .map(str::to_string)
    }

    /// The block's year, or else the first four digits of the file's
    /// `date` tag, because that tag often holds a whole date. `year`
    /// reads the block alone, so a film never shows a year from a file
    /// tag that its block did not declare.
    pub fn music_year(&self, film: &Film) -> Option<Value> {
        if let Some(year) = self.year()
            && year != &Value::String(String::new())
        {
            return Some(year.clone());
        }
        let date = film.tag("date")?;
        let leading: String = date.chars().take_while(char::is_ascii_digit).collect();
        (leading.len() == 4).then_some(Value::String(leading))
    }

    pub fn year(&self) -> Option<&Value> {
        self.field("year")
    }

    pub fn date(&self) -> Option<&str> {
        self.word("date")
    }

    /// The logo reference: a path under the media mount in the pod, or
    /// an https URL.
    pub fn logo(&self) -> Option<&str> {
        self.word("logo")
    }

    /// The directory of Jellyfin's trickplay sprite sheets, which the
    /// display crops scrub thumbnails from. `None` means the item shows no
    /// thumbnail.
    pub fn trickplay(&self) -> Option<&str> {
        self.word("trickplay")
    }

    /// The path of the file that lists the people on screen in each span
    /// of the item. `None` means the item shows no cast row.
    pub fn appearances(&self) -> Option<&str> {
        self.word("appearances")
    }

    /// The library's `.contributors` directory, which holds the portrait
    /// that the appearances file names for each person.
    pub fn contributors(&self) -> Option<&str> {
        self.word("contributors")
    }

    /// The cover, a path or URL like the logo. The music layout tries it
    /// first, then the picture embedded in the file, then a cover file
    /// beside the file.
    pub fn art(&self) -> Option<&str> {
        self.word("art")
    }

    fn field(&self, name: &str) -> Option<&Value> {
        self.block.get(name).filter(|value| !value.is_null())
    }

    fn word(&self, name: &str) -> Option<&str> {
        self.field(name)?.as_str().filter(|word| !word.is_empty())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn film() -> Film {
        let mut film = Film::default();
        film.apply("media-title", &json!("the-file.mkv"));
        film.apply(
            "metadata",
            &json!({ "artist": "The Band", "album": "The Record", "date": "1979-10-05" }),
        );
        film
    }

    fn block(text: &str) -> Presentation {
        let mut presentation = Presentation::default();
        presentation.receive(text);
        presentation
    }

    #[test]
    fn a_block_that_declares_nothing_falls_back_for_every_field() {
        for text in ["", "{}", "not json", "[1, 2]", "null"] {
            let presentation = block(text);
            assert_eq!(
                presentation.title(&film()).as_deref(),
                Some("the-file.mkv"),
                "{text}"
            );
            assert_eq!(presentation.kind(), Kind::Other, "{text}");
            assert_eq!(presentation.year(), None, "{text}");
            assert_eq!(presentation.logo(), None, "{text}");
        }
    }

    #[test]
    fn the_block_wins_over_the_file() {
        let presentation = block(r#"{"title":"A Film","artist":"Someone","album":"A Record"}"#);
        assert_eq!(presentation.title(&film()).as_deref(), Some("A Film"));
        assert_eq!(presentation.artist(&film()).as_deref(), Some("Someone"));
        assert_eq!(presentation.album(&film()).as_deref(), Some("A Record"));
    }

    #[test]
    fn an_empty_field_reads_as_undeclared() {
        let presentation = block(
            r#"{"title":"","artist":"","album":"","logo":"","trickplay":"","art":"","role":""}"#,
        );
        assert_eq!(presentation.title(&film()).as_deref(), Some("the-file.mkv"));
        assert_eq!(presentation.artist(&film()).as_deref(), Some("The Band"));
        assert_eq!(presentation.album(&film()).as_deref(), Some("The Record"));
        assert_eq!(presentation.role(), None);
        assert_eq!(presentation.logo(), None);
        assert_eq!(presentation.trickplay(), None);
        assert_eq!(presentation.art(), None);
    }

    #[test]
    fn a_title_is_none_when_the_file_has_none() {
        assert_eq!(block("{}").title(&Film::default()), None);
    }

    #[test]
    fn the_declared_fields_read_as_the_block_wrote_them() {
        let presentation = block(
            r#"{"type":"series","hint":"series","role":"trailer","series":"A Show","season":2,
                "episode":7,
                "episodeTitle":"The One","date":"2017-03-05","year":2014,
                "logo":"/art/logo.png","trickplay":"/art/tiles","art":"/art/cover.jpg"}"#,
        );
        assert_eq!(presentation.kind(), Kind::Other);
        assert_eq!(presentation.hint(), Some("series"));
        assert_eq!(presentation.role(), Some("trailer"));
        assert_eq!(presentation.series(), Some("A Show"));
        assert_eq!(presentation.season(), Some(&json!(2)));
        assert_eq!(presentation.episode(), Some(&json!(7)));
        assert_eq!(presentation.episode_title(), Some("The One"));
        assert_eq!(presentation.date(), Some("2017-03-05"));
        assert_eq!(presentation.year(), Some(&json!(2014)));
        assert_eq!(presentation.logo(), Some("/art/logo.png"));
        assert_eq!(presentation.trickplay(), Some("/art/tiles"));
        assert_eq!(presentation.art(), Some("/art/cover.jpg"));
        assert!(
            !presentation.is_image(),
            "a block with no type is an image, want not"
        );
        assert!(
            !presentation.is_music(),
            "a block with no type is music, want not"
        );
    }

    #[test]
    fn the_image_and_music_types_set_the_kind() {
        assert!(
            block(r#"{"type":"image"}"#).is_image(),
            "type image is not an image"
        );
        assert!(
            block(r#"{"type":"music"}"#).is_music(),
            "type music is not music"
        );
        assert!(
            !block(r#"{"type":"music"}"#).is_image(),
            "type music is an image, want not"
        );
    }

    #[test]
    fn the_music_year_falls_back_to_the_date_tag() {
        assert_eq!(
            block(r#"{"year":1979}"#).music_year(&film()),
            Some(json!(1979))
        );
        assert_eq!(block("{}").music_year(&film()), Some(json!("1979")));
        assert_eq!(
            block(r#"{"year":""}"#).music_year(&film()),
            Some(json!("1979"))
        );
        assert_eq!(block("{}").music_year(&Film::default()), None);
        assert_eq!(block("{}").year(), None);

        let mut short = Film::default();
        short.apply("metadata", &json!({ "date": "79" }));
        assert_eq!(block("{}").music_year(&short), None);
    }

    #[test]
    fn a_second_block_replaces_the_first() {
        let mut presentation = block(r#"{"title":"A Film","year":2014}"#);
        presentation.receive(r#"{"title":"Another"}"#);
        assert_eq!(presentation.title(&film()).as_deref(), Some("Another"));
        assert_eq!(presentation.year(), None);
    }

    #[test]
    fn the_marks_come_from_the_block_and_a_new_item_replaces_them() {
        let mut presentation =
            block(r#"{"title":"A Film","marks":[{"kind":"intro","start":5.0,"end":65.0}]}"#);
        assert_eq!(
            presentation.marks().skippable(Some(10.0), None),
            Some(crate::marks::Jump {
                span: crate::marks::Span {
                    kind: crate::marks::Kind::Intro,
                    start: 5.0,
                    end: 65.0
                },
                to: 65.0
            })
        );
        presentation.receive(r#"{"title":"Another"}"#);
        assert_eq!(presentation.marks(), &Marks::default());
    }
}
