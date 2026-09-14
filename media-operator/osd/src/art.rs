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

// The tile gate still holds a deadline although the decode runs in this
// process. Every task answers, so the deadline stands only for a task the
// frame loop never hears from, and without it one such task would stop
// every later tile for the rest of the item.
// this process: every task answers, so this stands only for a task the frame
// loop never hears from, and without it one such task would stop every later
// tile for the rest of the item.
const REPLY_TIMEOUT: f64 = 1.0;

/// The region the cover fits in: between the left margin and the right one,
/// and from under the header block to above the scrubber.
const REGION_X: f32 = theme::MARGIN_X;
const REGION_TOP: f32 = 300.0;
const REGION_BOTTOM: f32 = 832.0;
const BOX_H: f32 = REGION_BOTTOM - REGION_TOP;
const CENTER_Y: f32 = REGION_TOP + BOX_H / 2.0;

/// The offer's art box: the card's own width, and the height the picture takes
/// at the top of it.
const CARD_W: f32 = 440.0;
const ART_H: f32 = 248.0;

/// One canvas length in real pixels, the way every request and every placement
/// rounds one.
fn pixels(length: f32, canvas: &Canvas) -> i32 {
    (length * canvas.scale + 0.5).floor() as i32
}

/// The pixel box a request asks for, as the key that names it.
fn box_key(width: i32, height: i32) -> Option<String> {
    (width > 0 && height > 0).then(|| format!("{width}x{height}"))
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
    key: String,
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
    key: String,
    bitmap: Option<Bitmap>,
}

/// The logo, the cover, and the offer's picture each ask for one box and hold
/// what came back.
#[derive(Debug, Default)]
struct Slot {
    /// The pixel size last asked for, so an unchanged size is not asked for
    /// again.
    want: Option<String>,
    /// The decoded picture. It is nothing until an answer arrives, and a swap
    /// clears it.
    bitmap: Option<Bitmap>,
}

/// The newest tile the display asks for: its key, the target time, and the
/// pixel box.
#[derive(Debug, Clone, PartialEq, Eq)]
struct Wanted {
    key: String,
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
    inflight: Option<String>,
    tile_want: Option<Wanted>,
    // The gate reopens at a deadline the next turn reads, because this client
    // holds no timer of its own.
    deadline: Option<f64>,
    cover: Slot,
    /// The box already answered for. A redraw then asks once, and an answer of
    /// no cover ends the asking instead of starting it again.
    answered: Option<String>,
    next: Slot,
    /// The offer's art reference while an offer with a picture stands, so an
    /// answer for an offer that no longer stands is dropped.
    offer: Option<String>,
    // The canvas the last requests were sized for. The Lua read
    // osd-dimensions; this client learns its own surface from the frame it
    // draws.
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
        self.clear_inflight();
        self.cover = Slot::default();
        self.answered = None;
        if let Ok(mut sheets) = self.sheets.lock() {
            *sheets = Sheets::default();
        }
        self.canvas = Some(*canvas);
    }

    /// The frame's own turn: ask for what this state needs, the way the Lua
    /// runs its four syncs at the top of a redraw.
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
        now: f64,
    ) -> Vec<Job> {
        self.offer = offer.map(str::to_string);
        let mut jobs = Vec::new();
        self.on_resize(canvas);
        jobs.extend(self.logo_request(canvas));
        if self.deadline.is_some_and(|deadline| now >= deadline) {
            self.clear_inflight();
            self.tile_want = None;
        }
        if presentation.is_music() {
            jobs.extend(self.cover_request(presentation, film, canvas));
        }
        if let Some(time) = preview {
            jobs.extend(self.tile_request(time, canvas, now));
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
        self.clear_inflight();
        self.next.want = None;
    }

    /// Decode the current logo at the pixel size the screen needs. It asks for
    /// nothing when the item has no logo, when the screen size gives no box,
    /// or when this item's logo was already asked for at that size.
    fn logo_request(&mut self, canvas: &Canvas) -> Option<Job> {
        let reference = self.logo_uri.clone()?;
        let (width, height) = (pixels(LOGO_MAX_W, canvas), pixels(LOGO_MAX_H, canvas));
        let key = box_key(width, height)?;
        if self.logo.want.as_deref() == Some(key.as_str()) {
            return None;
        }
        self.logo.want = Some(key.clone());
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
        let art = presentation.art().map(str::to_string);
        if art.is_none() && film.path.is_none() {
            return None;
        }
        let (width, height) = (
            pixels(canvas.width - 2.0 * REGION_X, canvas),
            pixels(BOX_H, canvas),
        );
        let key = box_key(width, height)?;
        if self.cover.want.as_deref() == Some(key.as_str())
            || self.answered.as_deref() == Some(key.as_str())
        {
            return None;
        }
        self.cover.want = Some(key.clone());
        let source = Source::Cover {
            art,
            file: film.path.clone(),
        };
        Some(self.job(Kind::Album, key, source, width, height))
    }

    /// Decode the tile at the target time and pixel box. Quantize the request
    /// to a whole second, so a scrub within one second asks for nothing. A new
    /// second, or a new box, asks again. While a decode is in flight, a new
    /// want is recorded and not started; the answer starts it when it arrives.
    fn tile_request(&mut self, time: f64, canvas: &Canvas, now: f64) -> Option<Job> {
        self.trickplay.as_ref()?;
        let (width, height) = (pixels(TILE_W, canvas), pixels(TILE_H, canvas));
        let box_key = box_key(width, height)?;
        let key = format!("{}:{box_key}", (time + 0.5).floor() as i64);
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
            None => Some(self.send_tile(&wanted, now)),
        }
    }

    /// Decode the offer's art at the pixel size the art box takes on this
    /// screen, once per size. The picture fits inside the box and keeps its
    /// ratio, so a poster comes back letterboxed.
    fn next_request(&mut self, canvas: &Canvas) -> Option<Job> {
        let reference = self.offer.clone()?;
        let (width, height) = (pixels(CARD_W, canvas), pixels(ART_H, canvas));
        let key = box_key(width, height)?;
        if self.next.want.as_deref() == Some(key.as_str()) {
            return None;
        }
        self.next.want = Some(key.clone());
        Some(self.job(Kind::Next, key, Source::Picture(reference), width, height))
    }

    /// Start one tile decode and mark it in flight. The deadline clears the
    /// mark, and the want with it, when no answer arrives within the timeout.
    /// It clears the want as well, so the next turn asks again even at the same
    /// second; the tile that answered nothing is otherwise never asked for
    /// until the cursor moves.
    fn send_tile(&mut self, wanted: &Wanted, now: f64) -> Job {
        self.inflight = Some(wanted.key.clone());
        self.deadline = Some(now + REPLY_TIMEOUT);
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
    fn job(&self, kind: Kind, key: String, source: Source, width: i32, height: i32) -> Job {
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

    /// Forget the decode in flight and its deadline, so the next want starts at
    /// once.
    fn clear_inflight(&mut self) {
        self.inflight = None;
        self.deadline = None;
    }

    /// One answer. Each consumer takes its own kind, so a misrouted answer
    /// draws nothing. The return says whether anything the display draws
    /// changed, and the answer the tile's gate holds open may start the next
    /// decode.
    pub fn on_answer(&mut self, answer: Answer, now: f64) -> (bool, Vec<Job>) {
        // An answer for an item that is no longer playing lands on nothing, so
        // the last item's art never draws over the new one. The offer's
        // picture serves the whole run, so it is the one kind an item swap
        // does not outrun.
        if answer.item != self.item && answer.kind != Kind::Next {
            return (false, Vec::new());
        }
        match answer.kind {
            // An answer for an item that no longer has a logo is dropped.
            Kind::Logo if self.logo_uri.is_some() => self.logo.bitmap = answer.bitmap,
            // Either answer marks the box answered, so the display asks once
            // for a box, and a missing cover ends the asking.
            Kind::Album => {
                self.answered = self.cover.want.clone();
                self.cover.bitmap = answer.bitmap;
            }
            Kind::Next if self.offer.is_some() => self.next.bitmap = answer.bitmap,
            // The answer names the tile it carries. When the want names a
            // different tile, it starts here: this is the one decode per
            // answer.
            Kind::Trickplay => {
                self.clear_inflight();
                self.tile.bitmap = answer.bitmap;
                let wanted = self.tile_want.clone().filter(|want| want.key != answer.key);
                return match wanted {
                    Some(wanted) => (true, vec![self.send_tile(&wanted, now)]),
                    None => (true, Vec::new()),
                };
            }
            _ => return (false, Vec::new()),
        }
        (true, Vec::new())
    }
}

/// Where the logo sits, in real pixels.
pub fn logo_at(canvas: &Canvas) -> Point {
    Point::new(
        pixels(theme::MARGIN_X, canvas) as f32,
        pixels(theme::MARGIN_Y, canvas) as f32,
    )
}

/// Where the tile sits, in real pixels: centered on the playhead x, above the
/// bar. It is clamped, so it stays on screen at both ends.
pub fn tile_at(canvas: &Canvas, tile: &Bitmap, cursor_x: f32) -> Point {
    let width = tile.width as i32;
    let mut x = pixels(cursor_x, canvas) - width / 2;
    if x + width > pixels(canvas.width, canvas) {
        x = pixels(canvas.width, canvas) - width;
    }
    Point::new(
        x.max(0) as f32,
        (pixels(TILE_BOTTOM_Y, canvas) - tile.height as i32).max(0) as f32,
    )
}

/// Where the cover sits, in real pixels: centered in the region between the
/// header block and the scrubber.
pub fn cover_at(canvas: &Canvas, cover: &Bitmap) -> Point {
    let centre = REGION_X + (canvas.width - 2.0 * REGION_X) / 2.0;
    Point::new(
        (pixels(centre, canvas) - cover.width as i32 / 2).max(0) as f32,
        (pixels(CENTER_Y, canvas) - cover.height as i32 / 2).max(0) as f32,
    )
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
        Bitmap::from_rgba(width, height, &vec![0; (width * height * 4) as usize])
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
            0.0,
        );
        assert_eq!(boxes(&jobs), [(Kind::Logo, 760, 110)]);

        let mut art = Art::default();
        let cover = art.sync(
            &block(r#"{"type":"music"}"#),
            &film(),
            &canvas(),
            None,
            None,
            0.0,
        );
        assert_eq!(boxes(&cover), [(Kind::Album, 1728, 532)]);

        let tile = art.sync(
            &block(r#"{"trickplay":"/art/tiles"}"#),
            &film(),
            &canvas(),
            Some(12.0),
            None,
            0.0,
        );
        assert_eq!(boxes(&tile), []);
        art.on_item(&block(r#"{"trickplay":"/art/tiles"}"#), &canvas());
        let tile = art.sync(
            &block(r#"{"trickplay":"/art/tiles"}"#),
            &film(),
            &canvas(),
            Some(12.0),
            None,
            0.0,
        );
        assert_eq!(boxes(&tile), [(Kind::Trickplay, 360, 220)]);
        assert_eq!(at(&tile[0]), 12_000);

        let next = art.sync(
            &block("{}"),
            &film(),
            &canvas(),
            None,
            Some("/art/next.jpg"),
            0.0,
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
            0.0,
        );
        assert_eq!(boxes(&logo), [(Kind::Logo, 1520, 220)]);

        let mut art = Art::default();
        let cover = art.sync(
            &block(r#"{"type":"music"}"#),
            &film(),
            &wide(),
            None,
            None,
            0.0,
        );
        assert_eq!(boxes(&cover), [(Kind::Album, 3456, 1064)]);
    }

    /// A music item asks for no logo at all, and an item with none asks for
    /// nothing.
    #[test]
    fn only_an_item_that_declares_a_logo_asks_for_one() {
        for text in [r#"{"type":"music","logo":"/art/logo.png"}"#, "{}"] {
            let mut art = Art::default();
            art.on_item(&block(text), &canvas());
            let jobs = art.sync(&block(text), &film(), &canvas(), None, None, 0.0);
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
        let jobs = art.sync(&item, &film(), &canvas(), None, None, 0.0);
        assert_eq!(jobs.len(), 1);
        assert!(
            art.sync(&item, &film(), &canvas(), None, None, 0.0)
                .is_empty()
        );

        art.on_answer(answer(&jobs[0], picture(1, 1)), 0.0);
        assert!(art.logo().is_some());
        art.on_item(&item, &canvas());
        assert!(art.logo().is_none());
        assert_eq!(
            art.sync(&item, &film(), &canvas(), None, None, 0.0).len(),
            1
        );
    }

    /// The cover asks once for a box, and an answer of no cover ends the
    /// asking.
    #[test]
    fn an_empty_cover_answer_ends_the_asking() {
        let mut art = Art::default();
        let item = block(r#"{"type":"music"}"#);
        let jobs = art.sync(&item, &film(), &canvas(), None, None, 0.0);
        assert_eq!(jobs.len(), 1);
        assert!(
            art.sync(&item, &film(), &canvas(), None, None, 0.0)
                .is_empty()
        );

        assert!(art.on_answer(answer(&jobs[0], None), 0.0).0);
        assert!(art.cover().is_none());
        assert!(
            art.sync(&item, &film(), &canvas(), None, None, 0.0)
                .is_empty()
        );
    }

    /// A cover request is held while mpv has named no file and the block names
    /// no art, because an answer of no cover would end the asking for the
    /// whole run.
    #[test]
    fn a_cover_waits_for_the_file_mpv_plays() {
        let mut art = Art::default();
        let item = block(r#"{"type":"music"}"#);
        assert!(
            art.sync(&item, &Film::default(), &canvas(), None, None, 0.0)
                .is_empty()
        );
        assert_eq!(
            art.sync(&item, &film(), &canvas(), None, None, 0.0).len(),
            1
        );

        let mut art = Art::default();
        let named = block(r#"{"type":"music","art":"/art/cover.jpg"}"#);
        assert_eq!(
            art.sync(&named, &Film::default(), &canvas(), None, None, 0.0)
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
        let jobs = art.sync(&item, &film(), &canvas(), None, None, 0.0);
        art.on_answer(answer(&jobs[0], picture(1, 1)), 0.0);
        assert!(art.cover().is_some());

        art.on_item(&block("{}"), &canvas());
        assert!(art.cover().is_none());
        assert_eq!(
            art.sync(&item, &film(), &canvas(), None, None, 0.0).len(),
            1
        );
    }

    /// The offer's picture is named by its box alone and serves the whole run,
    /// so it asks once and an item swap leaves it where it is.
    #[test]
    fn the_offers_picture_asks_once_and_outlives_an_item() {
        let mut art = Art::default();
        let offer = Some("/art/next.jpg");
        let jobs = art.sync(&block("{}"), &film(), &canvas(), None, offer, 0.0);
        assert_eq!(jobs.len(), 1);
        assert!(
            art.sync(&block("{}"), &film(), &canvas(), None, offer, 0.0)
                .is_empty()
        );

        assert!(art.on_answer(answer(&jobs[0], picture(1, 1)), 0.0).0);
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
            0.0,
        );
        let _ = art.sync(&block("{}"), &film(), &canvas(), None, None, 0.0);
        assert!(!art.on_answer(answer(&jobs[0], picture(1, 1)), 0.0).0);
        assert!(art.next().is_none());

        let mut art = Art::default();
        let item = block(r#"{"logo":"/art/logo.png"}"#);
        art.on_item(&item, &canvas());
        let jobs = art.sync(&item, &film(), &canvas(), None, None, 0.0);
        art.on_item(&block("{}"), &canvas());
        assert!(!art.on_answer(answer(&jobs[0], picture(1, 1)), 0.0).0);
        assert!(art.logo().is_none());
    }

    /// An answer the item swap outran lands on nothing, so the last item's art
    /// never draws over the new one.
    #[test]
    fn an_answer_the_item_swap_outran_lands_on_nothing() {
        let mut art = Art::default();
        let item = block(r#"{"logo":"/art/logo.png"}"#);
        art.on_item(&item, &canvas());
        let jobs = art.sync(&item, &film(), &canvas(), None, None, 0.0);
        art.on_item(&item, &canvas());
        assert!(!art.on_answer(answer(&jobs[0], picture(1, 1)), 0.0).0);
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
            started.extend(art.sync(&item, &film(), &canvas(), Some(time), None, 0.0));
        }
        assert_eq!(started.len(), 1);
        assert_eq!(at(&started[0]), 10_000);

        let (changed, more) = art.on_answer(answer(&started[0], picture(1, 1)), 0.0);
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
            started.extend(art.sync(&item, &film(), &canvas(), Some(time), None, 0.0));
        }
        assert_eq!(started.len(), 1);
        let (_, more) = art.on_answer(answer(&started[0], picture(1, 1)), 0.0);
        assert!(more.is_empty());
    }

    /// The timeout reopens the gate when no answer arrives, and the next turn
    /// asks again at the second it stands on.
    #[test]
    fn the_timeout_reopens_the_gate() {
        let mut art = Art::default();
        let item = block(r#"{"trickplay":"/art/tiles"}"#);
        art.on_item(&item, &canvas());
        assert_eq!(
            art.sync(&item, &film(), &canvas(), Some(10.0), None, 0.0)
                .len(),
            1
        );
        assert!(
            art.sync(&item, &film(), &canvas(), Some(12.0), None, 0.5)
                .is_empty()
        );

        let started = art.sync(&item, &film(), &canvas(), Some(12.0), None, 1.0);
        assert_eq!(started.len(), 1);
        assert_eq!(at(&started[0]), 12_000);
    }

    /// A new item reopens the gate, so the new item's first tile goes out even
    /// when the old item's last decode answered nothing.
    #[test]
    fn a_new_item_reopens_the_tile_gate() {
        let mut art = Art::default();
        let item = block(r#"{"trickplay":"/art/tiles"}"#);
        art.on_item(&item, &canvas());
        assert_eq!(
            art.sync(&item, &film(), &canvas(), Some(10.0), None, 0.0)
                .len(),
            1
        );
        art.on_item(&item, &canvas());

        let started = art.sync(&item, &film(), &canvas(), Some(20.0), None, 0.1);
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
        let _ = art.sync(&item, &film(), &canvas(), Some(10.0), offer, 0.0);

        let jobs = art.sync(&item, &film(), &wide(), Some(10.0), offer, 0.1);
        assert_eq!(
            boxes(&jobs),
            [
                (Kind::Logo, 1520, 220),
                (Kind::Trickplay, 720, 440),
                (Kind::Next, 880, 496),
            ]
        );
    }

    /// A surface with no size gives no box, so nothing is asked for.
    #[test]
    fn a_surface_with_no_box_asks_for_nothing() {
        let flat = Canvas {
            width: 0.0,
            height: 1080.0,
            scale: 0.0,
        };
        let mut art = Art::default();
        let item = block(r#"{"type":"music","logo":"/art/logo.png","trickplay":"/art/tiles"}"#);
        art.on_item(&item, &flat);
        assert!(
            art.sync(&item, &film(), &flat, Some(1.0), Some("/art/next.jpg"), 0.0)
                .is_empty()
        );
    }

    /// The logo sits at the title's own corner, in real pixels.
    #[test]
    fn the_logo_sits_at_the_title_corner() {
        assert_eq!(logo_at(&canvas()), Point::new(96.0, 90.0));
        assert_eq!(logo_at(&wide()), Point::new(192.0, 180.0));
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
