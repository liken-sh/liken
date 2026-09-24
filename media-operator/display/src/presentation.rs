//! The presentation module holds the current item's declared fields and
//! resolves each one for the header. The command sidecar hands it a block over
//! a script-message whenever the playlist reaches a new item.

use serde_json::{Map, Value};

use crate::film::Film;
use crate::marks::Marks;

/// What kind of item the block declares, which the scrubber, the strip, and
/// the header each ask about on every rebuild.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum Kind {
    #[default]
    Other,
    /// A still photo, which has no timeline.
    Image,
    /// An album, which is one audio stream with nothing to choose.
    Music,
}

/// The current item's declared fields, and the three readings that do not
/// change between two blocks: what kind of item it is, the season line a
/// series draws, and the marks. Each is resolved when the block arrives,
/// because the display reads them many times for one item and the block is
/// read once.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Presentation {
    block: Map<String, Value>,
    kind: Kind,
    second: Option<String>,
    marks: Marks,
}

impl Presentation {
    /// Take the block as one JSON string. An empty object, or text that does
    /// not parse, means the item declared nothing, so every field falls
    /// through to its next tier.
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

    /// The spans the library found for the intro, the recap, the credits, and
    /// the preview. An item with none plays with no skip control, and its card
    /// rises by the time that remains.
    pub fn marks(&self) -> &Marks {
        &self.marks
    }

    /// The title resolves in three tiers: the block's own title, then mpv's
    /// `media-title` read from the file, then nothing. The other fields have no
    /// file to fall back to, so they come from the block alone.
    pub fn title(&self, film: &Film) -> Option<String> {
        self.word("title")
            .map(str::to_string)
            .or_else(|| film.media_title.clone())
    }

    /// The item's type. The block names this field `type`, which Rust keeps
    /// for itself.
    pub fn kind(&self) -> Kind {
        self.kind
    }

    /// A still photo has no timeline, so it shows no scrubber and no strip.
    pub fn is_image(&self) -> bool {
        self.kind == Kind::Image
    }

    /// An album is one audio stream with nothing to choose, so it shows no
    /// strip.
    pub fn is_music(&self) -> bool {
        self.kind == Kind::Music
    }

    /// The line under the title of a series: the season, the episode, and the
    /// date the block names. Every part of it comes from the block, so it is
    /// resolved once for the item.
    pub fn second(&self) -> Option<&str> {
        self.second.as_deref()
    }

    pub fn hint(&self) -> Option<&str> {
        self.word("hint")
    }

    /// The item's part in the work: `trailer`, or nothing for the work
    /// itself.
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

    /// The music year takes the block's own year first, then the leading four
    /// digits of the file's date tag, which often carries a whole date. The
    /// film year below keeps the block as its only tier, so a film file with a
    /// date tag never shows a year its block did not declare.
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

    /// The logo is a resolved reference: an in-pod path on the media mount, or
    /// an https URL. The header decodes it to the box it draws it in.
    pub fn logo(&self) -> Option<&str> {
        self.word("logo")
    }

    /// The trickplay reference, the sprite-sheet directory the scan crops tiles
    /// from. Nothing means the item shows no thumbnail.
    pub fn trickplay(&self) -> Option<&str> {
        self.word("trickplay")
    }

    /// The cover reference, resolved the way the logo is. It is the first tier
    /// of the art the music layout draws, and the picture inside the file and a
    /// cover beside it follow.
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
    fn a_block_that_declares_nothing_falls_through_every_tier() {
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
    fn an_empty_field_falls_through_to_the_tier_below() {
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
    fn a_title_resolves_to_nothing_when_the_file_carries_none() {
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
        assert!(!presentation.is_image());
        assert!(!presentation.is_music());
    }

    #[test]
    fn the_image_and_music_types_read_off_the_block() {
        assert!(block(r#"{"type":"image"}"#).is_image());
        assert!(block(r#"{"type":"music"}"#).is_music());
        assert!(!block(r#"{"type":"music"}"#).is_image());
    }

    /// The music year takes the block first and the date tag's leading four
    /// digits next. The film year has the block as its only tier.
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

    /// A second block replaces the first, so a new item carries none of the
    /// last one's fields.
    #[test]
    fn a_second_block_replaces_the_first() {
        let mut presentation = block(r#"{"title":"A Film","year":2014}"#);
        presentation.receive(r#"{"title":"Another"}"#);
        assert_eq!(presentation.title(&film()).as_deref(), Some("Another"));
        assert_eq!(presentation.year(), None);
    }

    /// The marks read off the block once, and the next item's block, which
    /// may carry none, replaces them.
    #[test]
    fn the_marks_read_off_the_block_and_a_new_item_replaces_them() {
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
