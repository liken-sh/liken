//! The display decodes its own art. Each consumer asks for a picture at a
//! pixel size, a decode task on the blocking pool reads the file or fetches
//! the URL and scales it, and the answer comes back as the pixels the toolkit
//! draws.
// One module holds the four pictures the display draws, because it draws
// them in its own z order instead of handing four overlay ids to mpv.

use std::sync::{Arc, Mutex};

use iced::Point;

use crate::canvas::Canvas;
use crate::film::Film;
use crate::presentation::Presentation;
use crate::theme;

pub mod bitmap;
pub mod cover;
pub mod decode;
pub mod trickplay;

pub use bitmap::Bitmap;

use trickplay::Sheets;

/// The logo's largest box, in canvas coordinates. It sits where the title line
/// does, and is about that line's height, so the second line below it stays
/// clear. The logo scales to fit inside this box.
const LOGO_MAX_W: f32 = 760.0;
const LOGO_MAX_H: f32 = 110.0;

/// The tile's box in canvas coordinates. The tile fits inside the box and
/// keeps the aspect, so the bitmap may be smaller.
const TILE_W: f32 = 360.0;
const TILE_H: f32 = 220.0;

/// The tile's bottom edge in canvas coordinates. The tile sits above the time
/// label and the bar, and grows upward from this line.
const TILE_BOTTOM_Y: f32 = 836.0;

/// The region the cover fits in: between the left margin and the right one,
/// and from under the header block to above the scrubber.
const REGION_TOP: f32 = 300.0;
const REGION_BOTTOM: f32 = 832.0;
const BOX_H: f32 = REGION_BOTTOM - REGION_TOP;
const CENTER_Y: f32 = REGION_TOP + BOX_H / 2.0;

/// One canvas length in real pixels, the way every request and every placement
/// rounds one.
fn pixels(length: f32, canvas: &Canvas) -> i32 {
    canvas.to_pixels(length) as i32
}

/// The pixel box a request asks for, and nothing for a screen that gives no
/// box at all.
fn pixel_box(width: i32, height: i32) -> Option<(i32, i32)> {
    (width > 0 && height > 0).then_some((width, height))
}

/// What one request asks for, as the name an answer carries back. A slot
/// takes an answer only for the key it asks for now, so a decode the state
/// outran lands on nothing.
#[derive(Debug, Clone, PartialEq, Eq)]
enum Key {
    /// The logo and the cover, which the pixel box alone names.
    Box(i32, i32),
    /// The offer's picture. The reference names it as well as the box,
    /// because every offer of a Play draws its own picture in the same box.
    Picture(String, i32, i32),
    /// One tile, at the whole second the scan previews.
    Tile(i64, i32, i32),
}

/// The four pictures the display decodes, one name per consumer.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Kind {
    Logo,
    Trickplay,
    Album,
    Next,
}

/// What one decode reads.
#[derive(Debug, Clone)]
enum Source {
    /// One reference the display opens: the item's logo, or the offer's art.
    Picture(String),
    /// One cell of a sprite sheet, at the millisecond the scan previews.
    Tile { dir: String, ms: i64 },
    /// The playing track's cover, which settles through its tiers on the pool
    /// because every tier opens a file.
    Cover {
        art: Option<String>,
        file: Option<String>,
    },
}

/// One decode the display asks for. It carries the item it was asked for, so
/// an answer the item swap outran is dropped.
#[derive(Debug, Clone)]
pub struct Job {
    kind: Kind,
    item: u64,
    key: Key,
    source: Source,
    width: u32,
    height: u32,
    sheets: Arc<Mutex<Sheets>>,
}

impl Job {
    /// Read the source and scale it into the box. This runs on the blocking
    /// pool, because a decode and a network fetch must never hold up a frame.
    pub fn run(self) -> Answer {
        let (width, height) = (self.width, self.height);
        let bitmap = match &self.source {
            Source::Picture(reference) => {
                decode::open(reference).and_then(|bytes| decode::fit(&bytes, width, height))
            }
            Source::Tile { dir, ms } => trickplay::tile(dir, *ms, width, height, &self.sheets),
            Source::Cover { art, file } => cover::resolve(art.as_deref(), file.as_deref())
                .and_then(|tier| cover::read(&tier))
                .and_then(|bytes| decode::fit(&bytes, width, height)),
        };
        // A reference that reads back no picture is worth a line: a logo that
        // never appears reads on screen exactly like a logo the item never
        // declared.
        if bitmap.is_none() {
            eprintln!("media-display: no picture in {}", self.reference());
        }
        self.answer(bitmap)
    }

    /// The answer for a decode that never ran. It is an answer like any
    /// other, so the slot it names stops waiting on it.
    pub fn empty(self) -> Answer {
        self.answer(None)
    }

    /// What this decode reads, as the line about it names it.
    pub fn reference(&self) -> &str {
        match &self.source {
            Source::Picture(reference) => reference,
            Source::Tile { dir, .. } => dir,
            Source::Cover { art, file } => art.as_deref().or(file.as_deref()).unwrap_or_default(),
        }
    }

    fn answer(self, bitmap: Option<Bitmap>) -> Answer {
        Answer {
            kind: self.kind,
            item: self.item,
            key: self.key,
            bitmap,
        }
    }
}

/// One decode as it comes back. A bitmap of nothing is the answer for a source
/// the display found no picture in, and it is an answer like any other: silence
/// would read as a slow decode, so the display would ask again on every redraw
/// and never stop.
#[derive(Debug, Clone)]
pub struct Answer {
    kind: Kind,
    item: u64,
    key: Key,
    bitmap: Option<Bitmap>,
}

/// The logo, the cover, and the offer's picture each ask for one box and hold
/// what came back.
#[derive(Debug, Default)]
struct Slot {
    /// What was last asked for, so the same request is not made twice and an
    /// answer for anything else is dropped.
    want: Option<Key>,
    /// The decoded picture. It is nothing until an answer arrives, and a swap
    /// clears it.
    bitmap: Option<Bitmap>,
}

/// The newest tile the display asks for: its key, the target time, and the
/// pixel box.
#[derive(Debug, Clone, PartialEq, Eq)]
struct Wanted {
    key: Key,
    ms: i64,
    width: i32,
    height: i32,
}

/// What each consumer asked for, and what came back. The cache is the four
/// slots and the one held sheet: each consumer holds one picture at the box it
/// draws, and a scrub holds the sheet it crops from.
#[derive(Debug, Default)]
pub struct Art {
    /// The logo the current item declared, nothing when it has none.
    logo_uri: Option<String>,
    logo: Slot,
    /// The sprite-sheet directory the current item declared, nothing when it
    /// declares none.
    trickplay: Option<String>,
    tile: Slot,
    /// The gate. `inflight` is the key of the decode in flight, and
    /// `tile_want` is the newest tile the display asks for. One decode runs at
    /// a time, so a held scrub starts one decode per answer, and the last
    /// answer is for the newest position. Without the gate a hold starts about
    /// thirty decodes a second and the tile keeps moving after the hand lifts.
    /// An unchanged second and box are not asked for again.
    inflight: Option<Key>,
    tile_want: Option<Wanted>,
    cover: Slot,
    /// The box already answered for. A redraw then asks once, and an answer of
    /// no cover ends the asking instead of starting it again.
    answered: Option<Key>,
    next: Slot,
    /// The offer's art reference while an offer with a picture stands, so an
    /// answer for an offer that no longer stands is dropped.
    offer: Option<String>,
    // The canvas the last requests were sized for. This client learns its
    // own surface from the frame it draws.
    canvas: Option<Canvas>,
    /// How many items have played. Each decode carries the count it was asked
    /// under, so an answer the item swap outran lands on nothing.
    item: u64,
    /// The one decoded sheet a scrub crops from, shared with every decode task
    /// so a scrub within one sheet reads no second file.
    sheets: Arc<Mutex<Sheets>>,
}

impl Art {
    /// The item's logo, and where it sits in real pixels. The logo shows only
    /// while the OSD is up and a picture is ready, which the caller gates.
    pub fn logo(&self) -> Option<&Bitmap> {
        self.logo.bitmap.as_ref()
    }

    /// The thumbnail the scan previews.
    pub fn tile(&self) -> Option<&Bitmap> {
        self.tile.bitmap.as_ref()
    }

    /// The playing track's cover art, the one picture a music screen carries.
    pub fn cover(&self) -> Option<&Bitmap> {
        self.cover.bitmap.as_ref()
    }

    /// The offer's picture, which the up-next card draws in its own art box.
    pub fn next(&self) -> Option<&Bitmap> {
        self.next.bitmap.as_ref()
    }

    /// A new item. It drops the previous item's pictures and asks for the new
    /// item's logo. An item with no logo leaves the header falling back to
    /// text.
    ///
    /// A music item asks for no logo at all. The one fact its header changes is
    /// the track name, and a logo would stand in the line that carries it.
    ///
    /// A stale answer for the old item then does not land on the new one. The
    /// in-flight mark goes as well, so the new item's first request goes out
    /// even when the old item's last decode answered nothing.
    ///
    /// The offer's picture is named by its box alone and serves the whole run,
    /// so an item swap leaves it where it is.
    pub fn on_item(&mut self, presentation: &Presentation, canvas: &Canvas) {
        self.item += 1;
        self.logo_uri = match presentation.is_music() {
            true => None,
            false => presentation.logo().map(str::to_string),
        };
        self.trickplay = presentation.trickplay().map(str::to_string);
        self.logo = Slot::default();
        self.tile = Slot::default();
        self.tile_want = None;
        self.inflight = None;
        self.cover = Slot::default();
        self.answered = None;
        if let Ok(mut sheets) = self.sheets.lock() {
            *sheets = Sheets::default();
        }
        self.canvas = Some(*canvas);
    }

    /// The frame's own turn: ask for what this state needs.
    ///
    /// `preview` carries the target time while a fine scan is in flight for an
    /// item that declares trickplay, and nothing at every other moment.
    /// `offer` carries the art of an offer that stands.
    pub fn sync(
        &mut self,
        presentation: &Presentation,
        film: &Film,
        canvas: &Canvas,
        preview: Option<f64>,
        offer: Option<&str>,
    ) -> Vec<Job> {
        if self.offer.as_deref() != offer {
            self.offer = offer.map(str::to_string);
        }
        let mut jobs = Vec::new();
        self.on_resize(canvas);
        jobs.extend(self.logo_request(canvas));
        if presentation.is_music() {
            jobs.extend(self.cover_request(presentation, film, canvas));
        }
        if let Some(time) = preview {
            jobs.extend(self.tile_request(time, canvas));
        }
        if offer.is_some() {
            jobs.extend(self.next_request(canvas));
        }
        jobs
    }

    /// The screen size changed. Every consumer asks at the new box, and each
    /// keeps the picture it holds on screen until the new one arrives, so
    /// nothing blinks on a resize.
    ///
    /// The cover forgets no key. A new box differs from the one in flight and
    /// from the one answered, so its own request asks on its own, and a change
    /// that leaves the box where it was asks nothing.
    fn on_resize(&mut self, canvas: &Canvas) {
        if self.canvas == Some(*canvas) {
            return;
        }
        self.canvas = Some(*canvas);
        self.logo.want = None;
        self.tile_want = None;
        self.inflight = None;
        self.next.want = None;
    }

    /// Decode the current logo at the pixel size the screen needs. It asks for
    /// nothing when the item has no logo, when the screen size gives no box,
    /// or when this item's logo was already asked for at that size.
    fn logo_request(&mut self, canvas: &Canvas) -> Option<Job> {
        self.logo_uri.as_ref()?;
        let (width, height) = pixel_box(pixels(LOGO_MAX_W, canvas), pixels(LOGO_MAX_H, canvas))?;
        let key = Key::Box(width, height);
        if self.logo.want.as_ref() == Some(&key) {
            return None;
        }
        self.logo.want = Some(key.clone());
        let reference = self.logo_uri.clone()?;
        Some(self.job(Kind::Logo, key, Source::Picture(reference), width, height))
    }

    /// Decode the playing track's cover at the pixel box the region gives it.
    /// The cover fits inside the box and keeps the aspect, so the bitmap may be
    /// smaller. It asks for nothing before the screen size gives a box, or when
    /// the same box is already in flight or already answered.
    ///
    /// A request for an item whose file mpv has not named yet is held rather
    /// than asked, because the display asks once and keeps the answer, so an
    /// answer of no cover sent before the file arrived would leave the cover
    /// dark for the whole run.
    fn cover_request(
        &mut self,
        presentation: &Presentation,
        film: &Film,
        canvas: &Canvas,
    ) -> Option<Job> {
        if presentation.art().is_none() && film.path.is_none() {
            return None;
        }
        let (width, height) = pixel_box(
            pixels(canvas.width() - 2.0 * theme::MARGIN_X, canvas),
            pixels(BOX_H, canvas),
        )?;
        let key = Key::Box(width, height);
        if self.cover.want.as_ref() == Some(&key) || self.answered.as_ref() == Some(&key) {
            return None;
        }
        self.cover.want = Some(key.clone());
        let source = Source::Cover {
            art: presentation.art().map(str::to_string),
            file: film.path.clone(),
        };
        Some(self.job(Kind::Album, key, source, width, height))
    }

    /// Decode the tile at the target time and pixel box. Quantize the request
    /// to a whole second, so a scrub within one second asks for nothing. A new
    /// second, or a new box, asks again. While a decode is in flight, a new
    /// want is recorded and not started; the answer starts it when it arrives.
    fn tile_request(&mut self, time: f64, canvas: &Canvas) -> Option<Job> {
        self.trickplay.as_ref()?;
        let (width, height) = pixel_box(pixels(TILE_W, canvas), pixels(TILE_H, canvas))?;
        let key = Key::Tile((time + 0.5).floor() as i64, width, height);
        if self.tile_want.as_ref().is_some_and(|want| want.key == key) {
            return None;
        }
        let wanted = Wanted {
            key,
            ms: (time * 1000.0 + 0.5).floor() as i64,
            width,
            height,
        };
        self.tile_want = Some(wanted.clone());
        match self.inflight {
            Some(_) => None,
            None => Some(self.send_tile(&wanted)),
        }
    }

    /// Decode the offer's art at the pixel size the art box takes on this
    /// screen, once per size. The picture fits inside the box and keeps its
    /// ratio, so a poster comes back letterboxed.
    fn next_request(&mut self, canvas: &Canvas) -> Option<Job> {
        let (width, height) = pixel_box(
            pixels(crate::upnext::CARD_W, canvas),
            pixels(crate::upnext::ART_H, canvas),
        )?;
        let asked = matches!(
            (self.offer.as_deref(), &self.next.want),
            (Some(reference), Some(Key::Picture(had, wide, high)))
                if had == reference && *wide == width && *high == height
        );
        if asked {
            return None;
        }
        let reference = self.offer.clone()?;
        let key = Key::Picture(reference.clone(), width, height);
        self.next.want = Some(key.clone());
        Some(self.job(Kind::Next, key, Source::Picture(reference), width, height))
    }

    /// Start one tile decode and mark it in flight.
    fn send_tile(&mut self, wanted: &Wanted) -> Job {
        self.inflight = Some(wanted.key.clone());
        let source = Source::Tile {
            dir: self.trickplay.clone().unwrap_or_default(),
            ms: wanted.ms,
        };
        self.job(
            Kind::Trickplay,
            wanted.key.clone(),
            source,
            wanted.width,
            wanted.height,
        )
    }

    /// One decode, for the item that stands now and the sheet this display
    /// holds.
    fn job(&self, kind: Kind, key: Key, source: Source, width: i32, height: i32) -> Job {
        Job {
            kind,
            item: self.item,
            key,
            source,
            width: width as u32,
            height: height as u32,
            sheets: Arc::clone(&self.sheets),
        }
    }

    /// One answer. Each consumer takes its own kind and only the key it asks
    /// for now, so a misrouted answer and a decode the state outran both draw
    /// nothing. The return says whether anything the display draws changed,
    /// and the answer the tile's gate holds open may start the next decode.
    pub fn on_answer(&mut self, answer: Answer) -> (bool, Vec<Job>) {
        // An answer for an item that is no longer playing lands on nothing, so
        // the last item's art never draws over the new one. The offer's
        // picture serves the whole run, so it is the one kind an item swap
        // does not outrun.
        if answer.item != self.item && answer.kind != Kind::Next {
            return (false, Vec::new());
        }
        match answer.kind {
            // An answer for an item that no longer has a logo is dropped, and
            // so is one for a box the screen no longer asks for.
            Kind::Logo
                if self.logo_uri.is_some() && self.logo.want.as_ref() == Some(&answer.key) =>
            {
                self.logo.bitmap = answer.bitmap;
            }
            // Either answer marks the box answered, so the display asks once
            // for a box, and a missing cover ends the asking.
            Kind::Album if self.cover.want.as_ref() == Some(&answer.key) => {
                self.answered = self.cover.want.clone();
                self.cover.bitmap = answer.bitmap;
            }
            Kind::Next if self.offer.is_some() && self.next.want.as_ref() == Some(&answer.key) => {
                self.next.bitmap = answer.bitmap;
            }
            // The answer names the tile it carries. When the want names a
            // different tile, it starts here: this is the one decode per
            // answer.
            Kind::Trickplay => {
                self.inflight = None;
                self.tile.bitmap = answer.bitmap;
                let wanted = self.tile_want.clone().filter(|want| want.key != answer.key);
                return match wanted {
                    Some(wanted) => (true, vec![self.send_tile(&wanted)]),
                    None => (true, Vec::new()),
                };
            }
            _ => return (false, Vec::new()),
        }
        (true, Vec::new())
    }
}

/// Where the logo sits, in canvas units on the output pixel grid. Every
/// picture is placed in whole output pixels, because it is decoded to the
/// pixel size it draws at and a half pixel would resample it.
pub fn logo_at(canvas: &Canvas) -> Point {
    Point::new(canvas.snap(theme::MARGIN_X), canvas.snap(theme::MARGIN_Y))
}

/// Where the tile sits: centered on the playhead x, above the bar. It is
/// clamped, so it stays on screen at both ends.
pub fn tile_at(canvas: &Canvas, tile: &Bitmap, cursor_x: f32) -> Point {
    let width = tile.width as i32;
    let mut x = pixels(cursor_x, canvas) - width / 2;
    if x + width > pixels(canvas.width(), canvas) {
        x = pixels(canvas.width(), canvas) - width;
    }
    let y = (pixels(TILE_BOTTOM_Y, canvas) - tile.height as i32).max(0);
    Point::new(
        canvas.to_canvas(x.max(0) as f32),
        canvas.to_canvas(y as f32),
    )
}

/// Where the cover sits: centered in the region between the header block and
/// the scrubber.
pub fn cover_at(canvas: &Canvas, cover: &Bitmap) -> Point {
    let centre = theme::MARGIN_X + (canvas.width() - 2.0 * theme::MARGIN_X) / 2.0;
    let x = (pixels(centre, canvas) - cover.width as i32 / 2).max(0);
    let y = (pixels(CENTER_Y, canvas) - cover.height as i32 / 2).max(0);
    Point::new(canvas.to_canvas(x as f32), canvas.to_canvas(y as f32))
}

#[cfg(test)]
mod tests {
    use super::*;
    use iced::Size;

    fn canvas() -> Canvas {
        Canvas::for_output(Size::new(1920.0, 1080.0))
    }

    fn wide() -> Canvas {
        Canvas::for_output(Size::new(3840.0, 2160.0))
    }

    fn block(text: &str) -> Presentation {
        let mut presentation = Presentation::default();
        presentation.receive(text);
        presentation
    }

    /// A film whose file mpv has named, which is what the cover's file tiers
    /// need before they can answer.
    fn film() -> Film {
        Film {
            path: Some("/media/track.flac".to_string()),
            ..Film::default()
        }
    }

    /// What each decode asks for: its consumer and its pixel box.
    fn boxes(jobs: &[Job]) -> Vec<(Kind, u32, u32)> {
        jobs.iter()
            .map(|job| (job.kind, job.width, job.height))
            .collect()
    }

    /// The millisecond one tile decode was asked at.
    fn at(job: &Job) -> i64 {
        match job.source {
            Source::Tile { ms, .. } => ms,
            _ => panic!("the decode is not a tile"),
        }
    }

    /// One picture of the stated size, as a decode answers with.
    fn picture(width: u32, height: u32) -> Option<Bitmap> {
        Bitmap::from_rgba(width, height, vec![0; (width * height * 4) as usize])
    }

    /// One answer for a decode, carrying the picture the test states.
    fn answer(job: &Job, bitmap: Option<Bitmap>) -> Answer {
        Answer {
            kind: job.kind,
            item: job.item,
            key: job.key.clone(),
            bitmap,
        }
    }

    /// Every consumer asks for the box its own formula gives, in real pixels.
    #[test]
    fn every_consumer_asks_for_the_box_its_formula_gives() {
        let mut art = Art::default();
        art.on_item(&block(r#"{"logo":"/art/logo.png"}"#), &canvas());
        let jobs = art.sync(
            &block(r#"{"logo":"/art/logo.png"}"#),
            &film(),
            &canvas(),
            None,
            None,
        );
        assert_eq!(boxes(&jobs), [(Kind::Logo, 760, 110)]);

        let mut art = Art::default();
        let cover = art.sync(
            &block(r#"{"type":"music"}"#),
            &film(),
            &canvas(),
            None,
            None,
        );
        assert_eq!(boxes(&cover), [(Kind::Album, 1728, 532)]);

        let tile = art.sync(
            &block(r#"{"trickplay":"/art/tiles"}"#),
            &film(),
            &canvas(),
            Some(12.0),
            None,
        );
        assert_eq!(boxes(&tile), []);
        art.on_item(&block(r#"{"trickplay":"/art/tiles"}"#), &canvas());
        let tile = art.sync(
            &block(r#"{"trickplay":"/art/tiles"}"#),
            &film(),
            &canvas(),
            Some(12.0),
            None,
        );
        assert_eq!(boxes(&tile), [(Kind::Trickplay, 360, 220)]);
        assert_eq!(at(&tile[0]), 12_000);

        let next = art.sync(
            &block("{}"),
            &film(),
            &canvas(),
            None,
            Some("/art/next.jpg"),
        );
        assert_eq!(boxes(&next), [(Kind::Next, 440, 248)]);
    }

    /// A 4K surface asks for every box at twice the canvas measure.
    #[test]
    fn a_larger_surface_asks_for_a_larger_box() {
        let mut art = Art::default();
        art.on_item(&block(r#"{"logo":"/art/logo.png"}"#), &wide());
        let logo = art.sync(
            &block(r#"{"logo":"/art/logo.png"}"#),
            &film(),
            &wide(),
            None,
            None,
        );
        assert_eq!(boxes(&logo), [(Kind::Logo, 1520, 220)]);

        let mut art = Art::default();
        let cover = art.sync(&block(r#"{"type":"music"}"#), &film(), &wide(), None, None);
        assert_eq!(boxes(&cover), [(Kind::Album, 3456, 1064)]);
    }

    /// A music item asks for no logo at all, and an item with none asks for
    /// nothing.
    #[test]
    fn only_an_item_that_declares_a_logo_asks_for_one() {
        for text in [r#"{"type":"music","logo":"/art/logo.png"}"#, "{}"] {
            let mut art = Art::default();
            art.on_item(&block(text), &canvas());
            let jobs = art.sync(&block(text), &film(), &canvas(), None, None);
            let logos: Vec<_> = jobs.iter().filter(|job| job.kind == Kind::Logo).collect();
            assert!(logos.is_empty(), "{text}");
        }
    }

    /// The header asks once for a size, and asks again for every other item.
    #[test]
    fn the_logo_asks_once_for_a_size() {
        let mut art = Art::default();
        let item = block(r#"{"logo":"/art/logo.png"}"#);
        art.on_item(&item, &canvas());
        let jobs = art.sync(&item, &film(), &canvas(), None, None);
        assert_eq!(jobs.len(), 1);
        assert!(art.sync(&item, &film(), &canvas(), None, None).is_empty());

        art.on_answer(answer(&jobs[0], picture(1, 1)));
        assert!(art.logo().is_some());
        art.on_item(&item, &canvas());
        assert!(art.logo().is_none());
        assert_eq!(art.sync(&item, &film(), &canvas(), None, None).len(), 1);
    }

    /// The cover asks once for a box, and an answer of no cover ends the
    /// asking.
    #[test]
    fn an_empty_cover_answer_ends_the_asking() {
        let mut art = Art::default();
        let item = block(r#"{"type":"music"}"#);
        let jobs = art.sync(&item, &film(), &canvas(), None, None);
        assert_eq!(jobs.len(), 1);
        assert!(art.sync(&item, &film(), &canvas(), None, None).is_empty());

        assert!(art.on_answer(answer(&jobs[0], None)).0);
        assert!(art.cover().is_none());
        assert!(art.sync(&item, &film(), &canvas(), None, None).is_empty());
    }

    /// A cover request is held while mpv has named no file and the block names
    /// no art, because an answer of no cover would end the asking for the
    /// whole run.
    #[test]
    fn a_cover_waits_for_the_file_mpv_plays() {
        let mut art = Art::default();
        let item = block(r#"{"type":"music"}"#);
        assert!(
            art.sync(&item, &Film::default(), &canvas(), None, None)
                .is_empty()
        );
        assert_eq!(art.sync(&item, &film(), &canvas(), None, None).len(), 1);

        let mut art = Art::default();
        let named = block(r#"{"type":"music","art":"/art/cover.jpg"}"#);
        assert_eq!(
            art.sync(&named, &Film::default(), &canvas(), None, None)
                .len(),
            1
        );
    }

    /// A new item drops the cover it holds and asks again, so a film after a
    /// music item carries no cover parked over it.
    #[test]
    fn a_new_item_drops_the_cover_and_asks_again() {
        let mut art = Art::default();
        let item = block(r#"{"type":"music"}"#);
        let jobs = art.sync(&item, &film(), &canvas(), None, None);
        art.on_answer(answer(&jobs[0], picture(1, 1)));
        assert!(art.cover().is_some());

        art.on_item(&block("{}"), &canvas());
        assert!(art.cover().is_none());
        assert_eq!(art.sync(&item, &film(), &canvas(), None, None).len(), 1);
    }

    /// The offer's picture is named by its box alone and serves the whole run,
    /// so it asks once and an item swap leaves it where it is.
    #[test]
    fn the_offers_picture_asks_once_and_outlives_an_item() {
        let mut art = Art::default();
        let offer = Some("/art/next.jpg");
        let jobs = art.sync(&block("{}"), &film(), &canvas(), None, offer);
        assert_eq!(jobs.len(), 1);
        assert!(
            art.sync(&block("{}"), &film(), &canvas(), None, offer)
                .is_empty()
        );

        assert!(art.on_answer(answer(&jobs[0], picture(1, 1))).0);
        assert!(art.next().is_some());
        art.on_item(&block("{}"), &canvas());
        assert!(art.next().is_some());
    }

    /// An answer for an offer that no longer stands, and one for an item with
    /// no logo, are dropped.
    #[test]
    fn an_answer_for_something_that_no_longer_stands_is_dropped() {
        let mut art = Art::default();
        let jobs = art.sync(
            &block("{}"),
            &film(),
            &canvas(),
            None,
            Some("/art/next.jpg"),
        );
        let _ = art.sync(&block("{}"), &film(), &canvas(), None, None);
        assert!(!art.on_answer(answer(&jobs[0], picture(1, 1))).0);
        assert!(art.next().is_none());

        let mut art = Art::default();
        let item = block(r#"{"logo":"/art/logo.png"}"#);
        art.on_item(&item, &canvas());
        let jobs = art.sync(&item, &film(), &canvas(), None, None);
        art.on_item(&block("{}"), &canvas());
        assert!(!art.on_answer(answer(&jobs[0], picture(1, 1))).0);
        assert!(art.logo().is_none());
    }

    /// A decode the screen size outran lands on nothing. Two decodes of one
    /// picture run at once after a resize, and the answer for the box the
    /// screen no longer draws must not take the slot.
    #[test]
    fn an_answer_for_a_box_the_screen_no_longer_asks_for_is_dropped() {
        let mut art = Art::default();
        let item = block(r#"{"logo":"/art/logo.png"}"#);
        art.on_item(&item, &canvas());
        let small = art.sync(&item, &film(), &canvas(), None, None);
        let large = art.sync(&item, &film(), &wide(), None, None);
        assert_eq!(boxes(&large), [(Kind::Logo, 1520, 220)]);

        // The answer for the old box lands after the new one was asked for.
        assert!(!art.on_answer(answer(&small[0], picture(760, 110))).0);
        assert!(art.logo().is_none());
        assert!(art.on_answer(answer(&large[0], picture(1520, 220))).0);
        assert_eq!(art.logo().map(|logo| logo.width), Some(1520));
    }

    /// The cover's own late answer marks no box answered, so the box the
    /// screen asks for now is still asked for.
    #[test]
    fn a_late_cover_answer_leaves_the_new_box_asked_for() {
        let mut art = Art::default();
        let item = block(r#"{"type":"music"}"#);
        let small = art.sync(&item, &film(), &canvas(), None, None);
        let large = art.sync(&item, &film(), &wide(), None, None);
        assert_eq!(large.len(), 1);

        assert!(!art.on_answer(answer(&small[0], picture(1, 1))).0);
        assert!(art.cover().is_none());
        assert!(art.on_answer(answer(&large[0], picture(1, 1))).0);
        assert!(art.cover().is_some());
    }

    /// Every offer of a Play draws its picture in the same box, so the offer's
    /// slot is named by the reference as well: a second offer decodes its own
    /// picture, and the first offer's answer does not land on it.
    #[test]
    fn a_second_offer_decodes_its_own_picture() {
        let mut art = Art::default();
        let first = art.sync(&block("{}"), &film(), &canvas(), None, Some("/art/one.jpg"));
        assert_eq!(first.len(), 1);

        let second = art.sync(&block("{}"), &film(), &canvas(), None, Some("/art/two.jpg"));
        assert_eq!(second.len(), 1);
        assert!(
            art.sync(&block("{}"), &film(), &canvas(), None, Some("/art/two.jpg"))
                .is_empty()
        );

        assert!(!art.on_answer(answer(&first[0], picture(2, 2))).0);
        assert!(art.next().is_none());
        assert!(art.on_answer(answer(&second[0], picture(4, 4))).0);
        assert_eq!(art.next().map(|next| next.width), Some(4));
    }

    /// A decode that never ran answers with no picture, so the slot it names
    /// stops waiting on it and the display asks again only for a new box.
    #[test]
    fn a_decode_that_never_ran_answers_with_no_picture() {
        let mut art = Art::default();
        let item = block(r#"{"type":"music"}"#);
        let jobs = art.sync(&item, &film(), &canvas(), None, None);
        assert!(art.on_answer(jobs[0].clone().empty()).0);
        assert!(art.cover().is_none());
        assert!(art.sync(&item, &film(), &canvas(), None, None).is_empty());
    }

    /// An answer the item swap outran lands on nothing, so the last item's art
    /// never draws over the new one.
    #[test]
    fn an_answer_the_item_swap_outran_lands_on_nothing() {
        let mut art = Art::default();
        let item = block(r#"{"logo":"/art/logo.png"}"#);
        art.on_item(&item, &canvas());
        let jobs = art.sync(&item, &film(), &canvas(), None, None);
        art.on_item(&item, &canvas());
        assert!(!art.on_answer(answer(&jobs[0], picture(1, 1))).0);
        assert!(art.logo().is_none());
    }

    /// Three turns during one decode in flight start one decode, and the
    /// answer starts the newest want.
    #[test]
    fn one_tile_decode_stands_in_flight_at_a_time() {
        let mut art = Art::default();
        let item = block(r#"{"trickplay":"/art/tiles"}"#);
        art.on_item(&item, &canvas());
        let mut started = Vec::new();
        for time in [10.0, 10.2, 12.0] {
            started.extend(art.sync(&item, &film(), &canvas(), Some(time), None));
        }
        assert_eq!(started.len(), 1);
        assert_eq!(at(&started[0]), 10_000);

        let (changed, more) = art.on_answer(answer(&started[0], picture(1, 1)));
        assert!(changed);
        assert_eq!(more.len(), 1);
        assert_eq!(at(&more[0]), 12_000);
        assert!(art.tile().is_some());
    }

    /// An answer for the want the display holds starts nothing more.
    #[test]
    fn an_answer_for_the_want_the_display_holds_starts_nothing() {
        let mut art = Art::default();
        let item = block(r#"{"trickplay":"/art/tiles"}"#);
        art.on_item(&item, &canvas());
        let mut started = Vec::new();
        for time in [10.0, 10.2, 10.4, 10.49] {
            started.extend(art.sync(&item, &film(), &canvas(), Some(time), None));
        }
        assert_eq!(started.len(), 1);
        let (_, more) = art.on_answer(answer(&started[0], picture(1, 1)));
        assert!(more.is_empty());
    }

    /// A new item reopens the gate, so the new item's first tile goes out even
    /// when the old item's last decode answered nothing.
    #[test]
    fn a_new_item_reopens_the_tile_gate() {
        let mut art = Art::default();
        let item = block(r#"{"trickplay":"/art/tiles"}"#);
        art.on_item(&item, &canvas());
        assert_eq!(
            art.sync(&item, &film(), &canvas(), Some(10.0), None).len(),
            1
        );
        art.on_item(&item, &canvas());

        let started = art.sync(&item, &film(), &canvas(), Some(20.0), None);
        assert_eq!(started.len(), 1);
        assert_eq!(at(&started[0]), 20_000);
    }

    /// A resize reopens the gate and asks at the new box, and the logo and the
    /// offer ask again as well.
    #[test]
    fn a_resize_asks_every_consumer_again() {
        let mut art = Art::default();
        let item = block(r#"{"logo":"/art/logo.png","trickplay":"/art/tiles"}"#);
        let offer = Some("/art/next.jpg");
        art.on_item(&item, &canvas());
        let _ = art.sync(&item, &film(), &canvas(), Some(10.0), offer);

        let jobs = art.sync(&item, &film(), &wide(), Some(10.0), offer);
        assert_eq!(
            boxes(&jobs),
            [
                (Kind::Logo, 1520, 220),
                (Kind::Trickplay, 720, 440),
                (Kind::Next, 880, 496),
            ]
        );
    }

    /// The logo sits at the title's own corner, in canvas units, so a larger
    /// surface puts it at the same place in the layout.
    #[test]
    fn the_logo_sits_at_the_title_corner() {
        assert_eq!(logo_at(&canvas()), Point::new(96.0, 90.0));
        assert_eq!(logo_at(&wide()), Point::new(96.0, 90.0));
    }

    /// The tile centres on the playhead and stands on its own bottom line, and
    /// it clamps to the screen at both ends.
    #[test]
    fn the_tile_centres_on_the_playhead_and_clamps() {
        let tile = picture(360, 220).expect("a tile");
        for (cursor, at) in [
            (960.0, Point::new(780.0, 616.0)),
            (96.0, Point::new(0.0, 616.0)),
            (1824.0, Point::new(1560.0, 616.0)),
        ] {
            assert_eq!(tile_at(&canvas(), &tile, cursor), at, "at {cursor}");
        }
    }

    /// A tile taller than the line it stands on clamps to the top of the
    /// screen.
    #[test]
    fn a_tile_taller_than_its_line_clamps_to_the_top() {
        let tile = picture(4, 900).expect("a tall tile");
        assert_eq!(tile_at(&canvas(), &tile, 960.0).y, 0.0);
    }

    /// The cover centres in its region, and a cover larger than the screen
    /// clamps to the corner.
    #[test]
    fn the_cover_centres_in_its_region() {
        let cover = picture(532, 532).expect("a cover");
        assert_eq!(cover_at(&canvas(), &cover), Point::new(694.0, 300.0));

        let huge = picture(1000, 1).expect("a wide cover");
        assert_eq!(cover_at(&canvas(), &huge), Point::new(460.0, 566.0));
    }
}
