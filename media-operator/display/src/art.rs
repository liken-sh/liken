//! Decodes the pictures the display draws: the logo, a music track's
//! cover, the scrub thumbnail, the up-next offer's picture, and cast
//! portraits. The thumbnail is one tile of the item's trickplay sprite
//! sheet.
//!
//! `Art` returns a `Job` for each picture it needs. The caller runs the
//! job on the blocking pool, where it reads the file or fetches the URL
//! and scales the picture, and passes the result to `Art::on_answer`.
//! The display draws the pictures in its own window above mpv's, not as
//! mpv overlays, so it controls what draws over what.

use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use iced::{Point, Size};

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

/// The largest box the logo can fill, in canvas coordinates. It starts
/// at the title line and is about as tall as that line, so the second
/// line below it stays clear.
const LOGO_MAX_W: f32 = 760.0;
const LOGO_MAX_H: f32 = 110.0;

/// The box a scrub thumbnail fits in, in canvas coordinates. The
/// thumbnail keeps its aspect ratio, so the bitmap can be smaller.
const TILE_W: f32 = 360.0;
const TILE_H: f32 = 220.0;

/// The y coordinate of the thumbnail's bottom edge, above the time
/// label and the scrub bar.
const TILE_BOTTOM_Y: f32 = 836.0;
/// The y coordinate of the top of the tallest thumbnail. Subtitles move
/// above it while a thumbnail shows.
pub const TILE_TOP: f32 = TILE_BOTTOM_Y - TILE_H;

/// The region the cover fits in, in canvas coordinates: the full width
/// between the side margins, and from below the header to above the
/// scrub bar.
const REGION_TOP: f32 = 300.0;
const REGION_BOTTOM: f32 = 832.0;
const BOX_H: f32 = REGION_BOTTOM - REGION_TOP;
const CENTER_Y: f32 = REGION_TOP + BOX_H / 2.0;

/// Converts a canvas length to whole pixels. Requests and placements
/// both use it, so a picture is decoded at the size it is drawn at.
fn pixels(length: f32, canvas: &Canvas) -> i32 {
    canvas.to_pixels(length) as i32
}

/// The pixel size to decode at, or `None` when either side is zero or
/// less, as it is for a screen whose size is not known yet.
fn pixel_box(width: i32, height: i32) -> Option<(i32, i32)> {
    (width > 0 && height > 0).then_some((width, height))
}

/// Identifies one request. `Art` keeps the key of what it requested
/// last for each picture, and drops an answer whose key differs, such
/// as an answer for an earlier screen size.
#[derive(Debug, Clone, PartialEq, Eq)]
enum Key {
    /// The logo and the cover, which are identified by their pixel box.
    Box(i32, i32),
    /// An offer's picture or a portrait. The key includes the
    /// reference, because every offer of a `Play` draws its picture in
    /// the same box, and every portrait in a row has the same box.
    Picture(String, i32, i32),
    /// One thumbnail, at a whole second of the film.
    Tile(i64, i32, i32),
}

/// The picture a job or an answer is for.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Kind {
    Logo,
    Trickplay,
    Album,
    Next,
    Portrait,
}

/// What one decode reads.
#[derive(Debug, Clone)]
enum Source {
    /// A file path or URL: the item's logo or the offer's art.
    Picture(String),
    /// One cell of a trickplay sprite sheet in directory `dir`, for the
    /// time `ms` in milliseconds.
    Tile { dir: String, ms: i64 },
    /// The playing track's cover. `cover::resolve` takes the item's
    /// `art`, then a picture embedded in the file, then a cover file in
    /// its folder. Each check opens a file, so the search runs in the
    /// job and not on the frame loop.
    Cover {
        art: Option<String>,
        file: Option<String>,
    },
    /// One person's portrait. It is scaled to fill its card's box and
    /// not to fit inside it, so every card in the row shows a picture
    /// of the same size.
    Portrait(String),
}

/// One decode to run. It records the item it was requested for, so
/// `Art::on_answer` can drop the answer when the item has changed.
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
    /// Reads the source and scales it into the box. Call it on the
    /// blocking pool, because decoding and fetching a URL are slow.
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
            Source::Portrait(reference) => {
                decode::open(reference).and_then(|bytes| decode::fill(&bytes, width, height))
            }
        };
        // A picture that fails to decode looks the same on screen as a
        // picture the item does not have, so the failure is logged.
        if bitmap.is_none() {
            eprintln!(
                "media-display: decoding {} produced no picture",
                self.reference()
            );
        }
        self.answer(bitmap)
    }

    /// An answer with no picture, for a job whose decode panicked. It
    /// still ends the wait for its request.
    pub fn empty(self) -> Answer {
        self.answer(None)
    }

    /// The path or URL this job reads, for log lines.
    pub fn reference(&self) -> &str {
        match &self.source {
            Source::Picture(reference) | Source::Portrait(reference) => reference,
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

/// The result of one `Job`. A `None` bitmap means the source held no
/// picture. It is still an answer, because without it `Art` would treat
/// the request as pending and repeat it on every redraw.
#[derive(Debug, Clone)]
pub struct Answer {
    kind: Kind,
    item: u64,
    key: Key,
    bitmap: Option<Bitmap>,
}

/// The request and the result for one picture: the logo, the cover, the
/// thumbnail, the offer's picture, or one portrait.
#[derive(Debug, Default)]
struct Slot {
    /// The key of the latest request. `Art` makes no second request for
    /// the same key, and drops an answer for any other key.
    requested: Option<Key>,
    /// The picture of the latest accepted answer, or `None`.
    bitmap: Option<Bitmap>,
}

/// The latest thumbnail request.
#[derive(Debug, Clone, PartialEq, Eq)]
struct NewestTile {
    key: Key,
    ms: i64,
    width: i32,
    height: i32,
}

/// The decoded pictures of the current item, and the state that decides
/// which pictures to request next.
#[derive(Debug, Default)]
pub struct Art {
    /// The current item's logo, or `None` when it has none or is music.
    logo_uri: Option<String>,
    logo: Slot,
    /// The trickplay sprite-sheet directory the current item declares,
    /// or `None`.
    trickplay: Option<String>,
    tile: Slot,
    /// `inflight` is the key of the running thumbnail decode, and
    /// `newest_tile` is the latest thumbnail request. Only one decode
    /// runs at a time, and when it finishes, `on_answer` starts one for
    /// `newest_tile` if that differs. So decodes do not queue up one per
    /// frame during a scrub, and the thumbnail settles soon after the
    /// scrub stops.
    inflight: Option<Key>,
    newest_tile: Option<NewestTile>,
    cover: Slot,
    /// The key of the last cover request that received an answer. After
    /// an answer with no cover, this stops `cover_request` from asking
    /// again on every redraw.
    cover_answered: Option<Key>,
    next: Slot,
    /// The canvas size of the box the offer's picture draws in. The
    /// display sets it to the up-next card's art box, or to the larger
    /// column box while the film is stepped back. `None` means the
    /// card's box.
    next_box: Option<Size>,
    /// The art reference of the current offer, or `None` when no offer
    /// with a picture is showing. Answers for any other offer are
    /// dropped.
    offer: Option<String>,
    /// The portraits the cast row has requested for this item, by path.
    /// They stay until the item changes, so a portrait that scrolls out
    /// of the row and back is not decoded again.
    portraits: HashMap<String, Slot>,
    /// The canvas the latest requests were sized for. `on_resize`
    /// compares each frame's canvas with it.
    canvas: Option<Canvas>,
    /// A counter that increases for each item. Each job records it, so
    /// an answer for an earlier item can be recognized and dropped.
    item: u64,
    /// The most recently decoded sprite sheet, shared with the jobs, so
    /// a tile from the same sheet is cut without reading the file again.
    sheets: Arc<Mutex<Sheets>>,
}

impl Art {
    /// The item's decoded logo.
    pub fn logo(&self) -> Option<&Bitmap> {
        self.logo.bitmap.as_ref()
    }

    /// The decoded scrub thumbnail.
    pub fn tile(&self) -> Option<&Bitmap> {
        self.tile.bitmap.as_ref()
    }

    /// The decoded cover of the playing track.
    pub fn cover(&self) -> Option<&Bitmap> {
        self.cover.bitmap.as_ref()
    }

    /// Sets the canvas size of the box the offer's picture draws in.
    /// The next `sync` requests the picture again at the new size, and
    /// the old picture stays on screen until the new one arrives.
    pub fn frame_next(&mut self, size: Size) {
        self.next_box = Some(size);
    }

    /// The decoded picture of the up-next offer.
    pub fn next(&self) -> Option<&Bitmap> {
        self.next.bitmap.as_ref()
    }

    /// The decoded portrait for `path`, once its job has been answered.
    pub fn portrait(&self, path: &str) -> Option<&Bitmap> {
        self.portraits.get(path)?.bitmap.as_ref()
    }

    /// Starts a new item and drops the previous item's pictures. It also
    /// clears `inflight`, because `on_answer` drops the answer of the
    /// previous item's thumbnail decode, and without the reset the new
    /// item would never start a thumbnail decode.
    ///
    /// A music item requests no logo, because its header shows the
    /// track name where the logo would go.
    ///
    /// The offer's picture belongs to the offer, not the item, so an
    /// item change keeps it.
    pub fn on_item(&mut self, presentation: &Presentation, canvas: &Canvas) {
        self.item += 1;
        self.logo_uri = match presentation.is_music() {
            true => None,
            false => presentation.logo().map(str::to_string),
        };
        self.trickplay = presentation.trickplay().map(str::to_string);
        self.logo = Slot::default();
        self.tile = Slot::default();
        self.newest_tile = None;
        self.inflight = None;
        self.cover = Slot::default();
        self.cover_answered = None;
        self.portraits.clear();
        if let Ok(mut sheets) = self.sheets.lock() {
            *sheets = Sheets::default();
        }
        self.canvas = Some(*canvas);
    }

    /// Returns the jobs for the pictures this frame needs and has not
    /// already requested.
    ///
    /// `preview` is the time the scrub thumbnail shows, or `None` when
    /// no thumbnail shows. `offer` is the art reference of the offer on
    /// screen. `portraits` lists the portraits the cast row draws.
    pub fn sync(
        &mut self,
        presentation: &Presentation,
        film: &Film,
        canvas: &Canvas,
        preview: Option<f64>,
        offer: Option<&str>,
        portraits: &[String],
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
        for path in portraits {
            jobs.extend(self.portrait_request(path, canvas));
        }
        jobs
    }

    /// Handles a change of screen size. The logo, thumbnail, offer
    /// picture, and portraits are requested again at the new size, and
    /// each keeps its current picture on screen until the new one
    /// arrives.
    ///
    /// The cover needs no reset. Its new box differs from both the box
    /// being decoded and the last answered box, so `cover_request`
    /// makes a request for it.
    fn on_resize(&mut self, canvas: &Canvas) {
        if self.canvas == Some(*canvas) {
            return;
        }
        self.canvas = Some(*canvas);
        self.logo.requested = None;
        self.newest_tile = None;
        self.inflight = None;
        self.next.requested = None;
        for slot in self.portraits.values_mut() {
            slot.requested = None;
        }
    }

    /// Requests the logo at the pixel size of its box. It returns
    /// `None` when the item has no logo, when the screen size gives no
    /// box, or when the logo was already requested at this size.
    fn logo_request(&mut self, canvas: &Canvas) -> Option<Job> {
        self.logo_uri.as_ref()?;
        let (width, height) = pixel_box(pixels(LOGO_MAX_W, canvas), pixels(LOGO_MAX_H, canvas))?;
        let key = Key::Box(width, height);
        if self.logo.requested.as_ref() == Some(&key) {
            return None;
        }
        self.logo.requested = Some(key.clone());
        let reference = self.logo_uri.clone()?;
        Some(self.job(Kind::Logo, key, Source::Picture(reference), width, height))
    }

    /// Requests the playing track's cover, fitted inside the cover
    /// region. It returns `None` before the screen size gives a box,
    /// and when this box is already requested or answered.
    ///
    /// It also returns `None` while the item has no `art` and mpv has
    /// not reported a file path. The cover is requested once per box,
    /// so an answer with no cover, sent before the file was known,
    /// would leave the cover empty for the rest of the item.
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
        if self.cover.requested.as_ref() == Some(&key) || self.cover_answered.as_ref() == Some(&key)
        {
            return None;
        }
        self.cover.requested = Some(key.clone());
        let source = Source::Cover {
            art: presentation.art().map(str::to_string),
            file: film.path.clone(),
        };
        Some(self.job(Kind::Album, key, source, width, height))
    }

    /// Requests the thumbnail for the target time. The time is rounded
    /// to a whole second, so a scrub within one second makes no new
    /// request. While a decode is running, the new request is only
    /// recorded in `newest_tile`, and `on_answer` starts it when the
    /// running decode returns.
    fn tile_request(&mut self, time: f64, canvas: &Canvas) -> Option<Job> {
        self.trickplay.as_ref()?;
        let (width, height) = pixel_box(pixels(TILE_W, canvas), pixels(TILE_H, canvas))?;
        let key = Key::Tile((time + 0.5).floor() as i64, width, height);
        if self
            .newest_tile
            .as_ref()
            .is_some_and(|tile| tile.key == key)
        {
            return None;
        }
        let newest = NewestTile {
            key,
            ms: (time * 1000.0 + 0.5).floor() as i64,
            width,
            height,
        };
        self.newest_tile = Some(newest.clone());
        match self.inflight {
            Some(_) => None,
            None => Some(self.send_tile(&newest)),
        }
    }

    /// Requests the offer's picture at the pixel size of its box, once
    /// per reference and size. The picture keeps its aspect ratio, so a
    /// poster is letterboxed.
    fn next_request(&mut self, canvas: &Canvas) -> Option<Job> {
        let frame = self
            .next_box
            .unwrap_or(Size::new(crate::upnext::CARD_W, crate::upnext::ART_H));
        let (width, height) = pixel_box(pixels(frame.width, canvas), pixels(frame.height, canvas))?;
        let asked = matches!(
            (self.offer.as_deref(), &self.next.requested),
            (Some(reference), Some(Key::Picture(had, wide, high)))
                if had == reference && *wide == width && *high == height
        );
        if asked {
            return None;
        }
        let reference = self.offer.clone()?;
        let key = Key::Picture(reference.clone(), width, height);
        self.next.requested = Some(key.clone());
        Some(self.job(Kind::Next, key, Source::Picture(reference), width, height))
    }

    /// Requests one portrait at the pixel size of a card's picture,
    /// once per path and size.
    fn portrait_request(&mut self, path: &str, canvas: &Canvas) -> Option<Job> {
        let (width, height) = pixel_box(
            pixels(crate::cast::PORTRAIT_W, canvas),
            pixels(crate::cast::PORTRAIT_H, canvas),
        )?;
        let key = Key::Picture(path.to_string(), width, height);
        let slot = self.portraits.entry(path.to_string()).or_default();
        if slot.requested.as_ref() == Some(&key) {
            return None;
        }
        slot.requested = Some(key.clone());
        Some(self.job(
            Kind::Portrait,
            key,
            Source::Portrait(path.to_string()),
            width,
            height,
        ))
    }

    /// Returns the job for a thumbnail request and records it as
    /// running.
    fn send_tile(&mut self, newest: &NewestTile) -> Job {
        self.inflight = Some(newest.key.clone());
        let source = Source::Tile {
            dir: self.trickplay.clone().unwrap_or_default(),
            ms: newest.ms,
        };
        self.job(
            Kind::Trickplay,
            newest.key.clone(),
            source,
            newest.width,
            newest.height,
        )
    }

    /// Builds a job for the current item.
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

    /// Stores an answer. An answer is kept only when its picture kind
    /// and key match the latest request for that picture, so late
    /// answers draw nothing. It returns whether anything drawn changed,
    /// and a job when a newer thumbnail request was waiting for this
    /// decode to finish.
    pub fn on_answer(&mut self, answer: Answer) -> (bool, Vec<Job>) {
        // The offer's picture belongs to the offer, not the item, so its
        // answer is kept across an item change.
        if answer.item != self.item && answer.kind != Kind::Next {
            return (false, Vec::new());
        }
        match answer.kind {
            // Drop the answer when the item has no logo now or the box
            // changed.
            Kind::Logo
                if self.logo_uri.is_some() && self.logo.requested.as_ref() == Some(&answer.key) =>
            {
                self.logo.bitmap = answer.bitmap;
            }
            // An answer without a bitmap also marks the box answered,
            // so a missing cover is not requested again.
            Kind::Album if self.cover.requested.as_ref() == Some(&answer.key) => {
                self.cover_answered = self.cover.requested.clone();
                self.cover.bitmap = answer.bitmap;
            }
            Kind::Next
                if self.offer.is_some() && self.next.requested.as_ref() == Some(&answer.key) =>
            {
                self.next.bitmap = answer.bitmap;
            }
            Kind::Portrait => {
                let Key::Picture(path, ..) = &answer.key else {
                    return (false, Vec::new());
                };
                match self.portraits.get_mut(path) {
                    Some(slot) if slot.requested.as_ref() == Some(&answer.key) => {
                        slot.bitmap = answer.bitmap;
                    }
                    _ => return (false, Vec::new()),
                }
            }
            // The decode finished. When the newest request is for a
            // different tile, start its decode now.
            Kind::Trickplay => {
                self.inflight = None;
                self.tile.bitmap = answer.bitmap;
                let newest = self
                    .newest_tile
                    .clone()
                    .filter(|tile| tile.key != answer.key);
                return match newest {
                    Some(newest) => (true, vec![self.send_tile(&newest)]),
                    None => (true, Vec::new()),
                };
            }
            _ => return (false, Vec::new()),
        }
        (true, Vec::new())
    }
}

/// Returns where to draw the logo, in canvas units. Pictures are placed
/// on whole output pixels, because each is decoded at its drawn size
/// and a fractional position would make the renderer resample it.
pub fn logo_at(canvas: &Canvas) -> Point {
    Point::new(canvas.snap(theme::MARGIN_X), canvas.snap(theme::MARGIN_Y))
}

/// Returns where to draw the thumbnail: centered on `cursor_x`, above
/// the scrub bar, and clamped to stay on screen.
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

/// Returns where to draw the cover: centered in the cover region.
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

    /// A film with a file path, which the cover needs to look for an
    /// embedded or neighboring picture.
    fn film() -> Film {
        Film {
            path: Some("/media/track.flac".to_string()),
            ..Film::default()
        }
    }

    /// The picture kind and pixel size of each job.
    fn boxes(jobs: &[Job]) -> Vec<(Kind, u32, u32)> {
        jobs.iter()
            .map(|job| (job.kind, job.width, job.height))
            .collect()
    }

    /// The time in milliseconds that a thumbnail job is for.
    fn at(job: &Job) -> i64 {
        match job.source {
            Source::Tile { ms, .. } => ms,
            _ => panic!("the job decodes {:?}, want a Source::Tile", job.kind),
        }
    }

    /// A blank picture of the given size.
    fn picture(width: u32, height: u32) -> Option<Bitmap> {
        Bitmap::from_rgba(width, height, vec![0; (width * height * 4) as usize])
    }

    /// An answer for `job` with the given picture.
    fn answer(job: &Job, bitmap: Option<Bitmap>) -> Answer {
        Answer {
            kind: job.kind,
            item: job.item,
            key: job.key.clone(),
            bitmap,
        }
    }

    /// Each picture is requested at the pixel size of its box on a
    /// 1080p canvas.
    #[test]
    fn each_picture_is_requested_at_its_own_box_size() {
        let mut art = Art::default();
        art.on_item(&block(r#"{"logo":"/art/logo.png"}"#), &canvas());
        let jobs = art.sync(
            &block(r#"{"logo":"/art/logo.png"}"#),
            &film(),
            &canvas(),
            None,
            None,
            &[],
        );
        assert_eq!(boxes(&jobs), [(Kind::Logo, 760, 110)]);

        let mut art = Art::default();
        let cover = art.sync(
            &block(r#"{"type":"music"}"#),
            &film(),
            &canvas(),
            None,
            None,
            &[],
        );
        assert_eq!(boxes(&cover), [(Kind::Album, 1728, 532)]);

        let tile = art.sync(
            &block(r#"{"trickplay":"/art/tiles"}"#),
            &film(),
            &canvas(),
            Some(12.0),
            None,
            &[],
        );
        assert_eq!(boxes(&tile), []);
        art.on_item(&block(r#"{"trickplay":"/art/tiles"}"#), &canvas());
        let tile = art.sync(
            &block(r#"{"trickplay":"/art/tiles"}"#),
            &film(),
            &canvas(),
            Some(12.0),
            None,
            &[],
        );
        assert_eq!(boxes(&tile), [(Kind::Trickplay, 360, 220)]);
        assert_eq!(at(&tile[0]), 12_000);

        let next = art.sync(
            &block("{}"),
            &film(),
            &canvas(),
            None,
            Some("/art/next.jpg"),
            &[],
        );
        assert_eq!(boxes(&next), [(Kind::Next, 440, 248)]);
    }

    /// A 4K canvas requests every box at twice the pixel size.
    #[test]
    fn a_larger_screen_requests_larger_boxes() {
        let mut art = Art::default();
        art.on_item(&block(r#"{"logo":"/art/logo.png"}"#), &wide());
        let logo = art.sync(
            &block(r#"{"logo":"/art/logo.png"}"#),
            &film(),
            &wide(),
            None,
            None,
            &[],
        );
        assert_eq!(boxes(&logo), [(Kind::Logo, 1520, 220)]);

        let mut art = Art::default();
        let cover = art.sync(
            &block(r#"{"type":"music"}"#),
            &film(),
            &wide(),
            None,
            None,
            &[],
        );
        assert_eq!(boxes(&cover), [(Kind::Album, 3456, 1064)]);
    }

    /// A music item requests no logo even when it declares one, and an
    /// item without a logo requests none.
    #[test]
    fn only_an_item_with_a_logo_requests_one() {
        for text in [r#"{"type":"music","logo":"/art/logo.png"}"#, "{}"] {
            let mut art = Art::default();
            art.on_item(&block(text), &canvas());
            let jobs = art.sync(&block(text), &film(), &canvas(), None, None, &[]);
            let logos: Vec<_> = jobs.iter().filter(|job| job.kind == Kind::Logo).collect();
            assert!(logos.is_empty(), "{text}");
        }
    }

    /// The logo is requested once per size, and again for the next
    /// item.
    #[test]
    fn the_logo_is_requested_once_per_size() {
        let mut art = Art::default();
        let item = block(r#"{"logo":"/art/logo.png"}"#);
        art.on_item(&item, &canvas());
        let jobs = art.sync(&item, &film(), &canvas(), None, None, &[]);
        assert_eq!(jobs.len(), 1);
        assert!(
            art.sync(&item, &film(), &canvas(), None, None, &[])
                .is_empty(),
            "sync requested a picture again, want no request",
        );

        art.on_answer(answer(&jobs[0], picture(1, 1)));
        assert!(art.logo().is_some(), "no logo picture is stored, want one");
        art.on_item(&item, &canvas());
        assert!(art.logo().is_none(), "a logo picture is stored, want none");
        assert_eq!(
            art.sync(&item, &film(), &canvas(), None, None, &[]).len(),
            1
        );
    }

    /// The cover is requested once per box, and an answer with no cover
    /// stops further requests.
    #[test]
    fn an_empty_cover_answer_stops_further_cover_requests() {
        let mut art = Art::default();
        let item = block(r#"{"type":"music"}"#);
        let jobs = art.sync(&item, &film(), &canvas(), None, None, &[]);
        assert_eq!(jobs.len(), 1);
        assert!(
            art.sync(&item, &film(), &canvas(), None, None, &[])
                .is_empty(),
            "sync requested a picture again, want no request",
        );

        assert!(
            art.on_answer(answer(&jobs[0], None)).0,
            "the answer reports no change to what is drawn, want one"
        );
        assert!(
            art.cover().is_none(),
            "a cover picture is stored, want none"
        );
        assert!(
            art.sync(&item, &film(), &canvas(), None, None, &[])
                .is_empty(),
            "sync requested a picture again, want no request",
        );
    }

    /// No cover is requested while mpv has no file and the item has no
    /// `art`, because an empty answer would stop all later requests.
    #[test]
    fn a_cover_is_not_requested_before_mpv_reports_the_file() {
        let mut art = Art::default();
        let item = block(r#"{"type":"music"}"#);
        assert!(
            art.sync(&item, &Film::default(), &canvas(), None, None, &[])
                .is_empty(),
            "sync requested a picture again, want no request",
        );
        assert_eq!(
            art.sync(&item, &film(), &canvas(), None, None, &[]).len(),
            1
        );

        let mut art = Art::default();
        let named = block(r#"{"type":"music","art":"/art/cover.jpg"}"#);
        assert_eq!(
            art.sync(&named, &Film::default(), &canvas(), None, None, &[])
                .len(),
            1
        );
    }

    /// A new item drops the previous cover, so a film that follows a
    /// music item does not draw the old cover.
    #[test]
    fn a_new_item_drops_the_cover_and_requests_it_again() {
        let mut art = Art::default();
        let item = block(r#"{"type":"music"}"#);
        let jobs = art.sync(&item, &film(), &canvas(), None, None, &[]);
        art.on_answer(answer(&jobs[0], picture(1, 1)));
        assert!(
            art.cover().is_some(),
            "no cover picture is stored, want one"
        );

        art.on_item(&block("{}"), &canvas());
        assert!(
            art.cover().is_none(),
            "a cover picture is stored, want none"
        );
        assert_eq!(
            art.sync(&item, &film(), &canvas(), None, None, &[]).len(),
            1
        );
    }

    /// The offer's picture is requested once. An item change keeps it,
    /// because it belongs to the offer.
    #[test]
    fn the_offer_picture_is_requested_once_and_survives_an_item_change() {
        let mut art = Art::default();
        let offer = Some("/art/next.jpg");
        let jobs = art.sync(&block("{}"), &film(), &canvas(), None, offer, &[]);
        assert_eq!(jobs.len(), 1);
        assert!(
            art.sync(&block("{}"), &film(), &canvas(), None, offer, &[])
                .is_empty(),
            "sync requested a picture again, want no request",
        );

        assert!(
            art.on_answer(answer(&jobs[0], picture(1, 1))).0,
            "the answer reports no change to what is drawn, want one"
        );
        assert!(art.next().is_some(), "no next picture is stored, want one");
        art.on_item(&block("{}"), &canvas());
        assert!(art.next().is_some(), "no next picture is stored, want one");
    }

    /// An answer is dropped when its offer is no longer shown or when
    /// the item no longer has a logo.
    #[test]
    fn an_answer_for_a_withdrawn_offer_or_a_removed_logo_is_dropped() {
        let mut art = Art::default();
        let jobs = art.sync(
            &block("{}"),
            &film(),
            &canvas(),
            None,
            Some("/art/next.jpg"),
            &[],
        );
        let _ = art.sync(&block("{}"), &film(), &canvas(), None, None, &[]);
        assert!(
            !art.on_answer(answer(&jobs[0], picture(1, 1))).0,
            "the answer reports a change to what is drawn, want none"
        );
        assert!(art.next().is_none(), "a next picture is stored, want none");

        let mut art = Art::default();
        let item = block(r#"{"logo":"/art/logo.png"}"#);
        art.on_item(&item, &canvas());
        let jobs = art.sync(&item, &film(), &canvas(), None, None, &[]);
        art.on_item(&block("{}"), &canvas());
        assert!(
            !art.on_answer(answer(&jobs[0], picture(1, 1))).0,
            "the answer reports a change to what is drawn, want none"
        );
        assert!(art.logo().is_none(), "a logo picture is stored, want none");
    }

    /// After a resize, two decodes of one picture can run at once. The
    /// answer for the old size must not replace the picture.
    #[test]
    fn an_answer_for_a_previous_screen_size_is_dropped() {
        let mut art = Art::default();
        let item = block(r#"{"logo":"/art/logo.png"}"#);
        art.on_item(&item, &canvas());
        let small = art.sync(&item, &film(), &canvas(), None, None, &[]);
        let large = art.sync(&item, &film(), &wide(), None, None, &[]);
        assert_eq!(boxes(&large), [(Kind::Logo, 1520, 220)]);

        // The answer for the old size arrives after the new request.
        assert!(
            !art.on_answer(answer(&small[0], picture(760, 110))).0,
            "the answer reports a change to what is drawn, want none"
        );
        assert!(art.logo().is_none(), "a logo picture is stored, want none");
        assert!(
            art.on_answer(answer(&large[0], picture(1520, 220))).0,
            "the answer reports no change to what is drawn, want one"
        );
        assert_eq!(art.logo().map(|logo| logo.width), Some(1520));
    }

    /// A late answer for the old cover box does not mark the new box
    /// answered, so the new box's answer is still accepted.
    #[test]
    fn a_late_cover_answer_does_not_mark_the_new_box_answered() {
        let mut art = Art::default();
        let item = block(r#"{"type":"music"}"#);
        let small = art.sync(&item, &film(), &canvas(), None, None, &[]);
        let large = art.sync(&item, &film(), &wide(), None, None, &[]);
        assert_eq!(large.len(), 1);

        assert!(
            !art.on_answer(answer(&small[0], picture(1, 1))).0,
            "the answer reports a change to what is drawn, want none"
        );
        assert!(
            art.cover().is_none(),
            "a cover picture is stored, want none"
        );
        assert!(
            art.on_answer(answer(&large[0], picture(1, 1))).0,
            "the answer reports no change to what is drawn, want one"
        );
        assert!(
            art.cover().is_some(),
            "no cover picture is stored, want one"
        );
    }

    /// A second offer requests its own picture, and the first offer's
    /// answer is dropped.
    #[test]
    fn a_second_offer_decodes_its_own_picture() {
        let mut art = Art::default();
        let first = art.sync(
            &block("{}"),
            &film(),
            &canvas(),
            None,
            Some("/art/one.jpg"),
            &[],
        );
        assert_eq!(first.len(), 1);

        let second = art.sync(
            &block("{}"),
            &film(),
            &canvas(),
            None,
            Some("/art/two.jpg"),
            &[],
        );
        assert_eq!(second.len(), 1);
        assert!(
            art.sync(
                &block("{}"),
                &film(),
                &canvas(),
                None,
                Some("/art/two.jpg"),
                &[]
            )
            .is_empty(),
            "sync requested a picture again, want no request",
        );

        assert!(
            !art.on_answer(answer(&first[0], picture(2, 2))).0,
            "the answer reports a change to what is drawn, want none"
        );
        assert!(art.next().is_none(), "a next picture is stored, want none");
        assert!(
            art.on_answer(answer(&second[0], picture(4, 4))).0,
            "the answer reports no change to what is drawn, want one"
        );
        assert_eq!(art.next().map(|next| next.width), Some(4));
    }

    /// The empty answer for a panicked decode ends the wait, and the
    /// cover is requested again only for a new box.
    #[test]
    fn a_panicked_decode_ends_the_wait_for_its_picture() {
        let mut art = Art::default();
        let item = block(r#"{"type":"music"}"#);
        let jobs = art.sync(&item, &film(), &canvas(), None, None, &[]);
        assert!(
            art.on_answer(jobs[0].clone().empty()).0,
            "the answer reports no change to what is drawn, want one"
        );
        assert!(
            art.cover().is_none(),
            "a cover picture is stored, want none"
        );
        assert!(
            art.sync(&item, &film(), &canvas(), None, None, &[])
                .is_empty(),
            "sync requested a picture again, want no request",
        );
    }

    /// An answer for the previous item is dropped, so its art is not
    /// drawn over the new item.
    #[test]
    fn an_answer_for_the_previous_item_is_dropped() {
        let mut art = Art::default();
        let item = block(r#"{"logo":"/art/logo.png"}"#);
        art.on_item(&item, &canvas());
        let jobs = art.sync(&item, &film(), &canvas(), None, None, &[]);
        art.on_item(&item, &canvas());
        assert!(
            !art.on_answer(answer(&jobs[0], picture(1, 1))).0,
            "the answer reports a change to what is drawn, want none"
        );
        assert!(art.logo().is_none(), "a logo picture is stored, want none");
    }

    /// Three frames while a thumbnail decode runs start one decode. Its
    /// answer starts the decode for the newest time.
    #[test]
    fn one_tile_decode_runs_at_a_time() {
        let mut art = Art::default();
        let item = block(r#"{"trickplay":"/art/tiles"}"#);
        art.on_item(&item, &canvas());
        let mut started = Vec::new();
        for time in [10.0, 10.2, 12.0] {
            started.extend(art.sync(&item, &film(), &canvas(), Some(time), None, &[]));
        }
        assert_eq!(started.len(), 1);
        assert_eq!(at(&started[0]), 10_000);

        let (changed, more) = art.on_answer(answer(&started[0], picture(1, 1)));
        assert!(
            changed,
            "the answer reports no change to what is drawn, want one"
        );
        assert_eq!(more.len(), 1);
        assert_eq!(at(&more[0]), 12_000);
        assert!(art.tile().is_some(), "no tile picture is stored, want one");
    }

    /// An answer for the newest requested thumbnail starts no further
    /// decode.
    #[test]
    fn an_answer_for_the_newest_tile_starts_no_decode() {
        let mut art = Art::default();
        let item = block(r#"{"trickplay":"/art/tiles"}"#);
        art.on_item(&item, &canvas());
        let mut started = Vec::new();
        for time in [10.0, 10.2, 10.4, 10.49] {
            started.extend(art.sync(&item, &film(), &canvas(), Some(time), None, &[]));
        }
        assert_eq!(started.len(), 1);
        let (_, more) = art.on_answer(answer(&started[0], picture(1, 1)));
        assert!(
            more.is_empty(),
            "the answer started another decode, want none"
        );
    }

    /// A new item starts its first thumbnail decode while the previous
    /// item's decode is still running.
    #[test]
    fn a_new_item_starts_a_tile_decode_while_one_is_running() {
        let mut art = Art::default();
        let item = block(r#"{"trickplay":"/art/tiles"}"#);
        art.on_item(&item, &canvas());
        assert_eq!(
            art.sync(&item, &film(), &canvas(), Some(10.0), None, &[])
                .len(),
            1
        );
        art.on_item(&item, &canvas());

        let started = art.sync(&item, &film(), &canvas(), Some(20.0), None, &[]);
        assert_eq!(started.len(), 1);
        assert_eq!(at(&started[0]), 20_000);
    }

    /// A resize requests the logo, thumbnail, and offer picture again
    /// at the new size.
    #[test]
    fn a_resize_requests_every_picture_again() {
        let mut art = Art::default();
        let item = block(r#"{"logo":"/art/logo.png","trickplay":"/art/tiles"}"#);
        let offer = Some("/art/next.jpg");
        art.on_item(&item, &canvas());
        let _ = art.sync(&item, &film(), &canvas(), Some(10.0), offer, &[]);

        let jobs = art.sync(&item, &film(), &wide(), Some(10.0), offer, &[]);
        assert_eq!(
            boxes(&jobs),
            [
                (Kind::Logo, 1520, 220),
                (Kind::Trickplay, 720, 440),
                (Kind::Next, 880, 496),
            ]
        );
    }

    /// The column beside a stepped-back film draws the offer's picture
    /// larger than the card does. The picture is requested again at the
    /// column's size, and again at the card's size when the film
    /// returns.
    #[test]
    fn the_offer_picture_follows_the_box_it_draws_in() {
        let mut art = Art::default();
        let item = block("{}");
        let offer = Some("/art/next.jpg");
        let _ = art.sync(&item, &film(), &canvas(), None, offer, &[]);

        art.frame_next(Size::new(704.0, 396.0));
        let column = art.sync(&item, &film(), &canvas(), None, offer, &[]);
        assert_eq!(boxes(&column), [(Kind::Next, 704, 396)]);
        assert!(
            art.sync(&item, &film(), &canvas(), None, offer, &[])
                .is_empty(),
            "sync requested a picture again, want no request",
        );

        art.frame_next(Size::new(440.0, 248.0));
        let card = art.sync(&item, &film(), &canvas(), None, offer, &[]);
        assert_eq!(boxes(&card), [(Kind::Next, 440, 248)]);
    }

    /// The logo is at the title's corner in canvas units on any screen
    /// size.
    #[test]
    fn the_logo_is_placed_at_the_title_corner() {
        assert_eq!(logo_at(&canvas()), Point::new(96.0, 90.0));
        assert_eq!(logo_at(&wide()), Point::new(96.0, 90.0));
    }

    /// The thumbnail is centered on the playhead, bottom-aligned to its
    /// line, and clamped to the screen at both ends.
    #[test]
    fn the_tile_is_centered_on_the_playhead_and_clamped_to_the_screen() {
        let tile = picture(360, 220).expect("a tile");
        for (cursor, at) in [
            (960.0, Point::new(780.0, 616.0)),
            (96.0, Point::new(0.0, 616.0)),
            (1824.0, Point::new(1560.0, 616.0)),
        ] {
            assert_eq!(tile_at(&canvas(), &tile, cursor), at, "at {cursor}");
        }
    }

    /// A thumbnail taller than the space above its line is clamped to
    /// the top of the screen.
    #[test]
    fn a_tile_taller_than_its_bottom_line_is_clamped_to_the_top() {
        let tile = picture(4, 900).expect("a tall tile");
        assert_eq!(tile_at(&canvas(), &tile, 960.0).y, 0.0);
    }

    /// The cover is centered in its region, and a cover wider than the
    /// screen is clamped to the left edge.
    #[test]
    fn the_cover_is_centered_in_its_region() {
        let cover = picture(532, 532).expect("a cover");
        assert_eq!(cover_at(&canvas(), &cover), Point::new(694.0, 300.0));

        let huge = picture(1000, 1).expect("a wide cover");
        assert_eq!(cover_at(&canvas(), &huge), Point::new(460.0, 566.0));
    }
}
