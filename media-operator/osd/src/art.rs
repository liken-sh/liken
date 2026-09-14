//! The bridge decodes the art the display cannot. The display asks the bridge
//! for a picture at a pixel size. The bridge reads the file, scales it, writes
//! the bgra to the volume it shares with the display, and answers with the path
//! and the size.
// PROSE: one module holds the four pictures the bridge serves, because the display draws them in its own z order instead of handing four overlay ids to mpv.

use iced::advanced::image::Handle;
use iced::{Point, Rectangle, Size};
use serde_json::json;

use crate::canvas::{Brush, Canvas};
use crate::ipc::Command;
use crate::presentation::Presentation;
use crate::theme;

/// The two script-message names the display and the bridge agree on.
pub const REQUEST: &str = "liken-art-request";
pub const REPLY: &str = "liken-art";

/// The four pictures the bridge serves, one name per consumer.
const LOGO: &str = "logo";
const TRICKPLAY: &str = "trickplay";
const ALBUM: &str = "album";
const NEXT: &str = "next";

/// The logo's largest box, in canvas coordinates. It sits where the title line
/// does, and is about that line's height, so the second line below it stays
/// clear. The bridge scales the logo to fit inside this box.
const LOGO_MAX_W: f32 = 760.0;
const LOGO_MAX_H: f32 = 110.0;

/// The tile's box in canvas coordinates. The bridge fits the tile inside the box
/// and keeps the aspect, so the returned bitmap may be smaller.
const TILE_W: f32 = 360.0;
const TILE_H: f32 = 220.0;

/// The tile's bottom edge in canvas coordinates. The tile sits above the time
/// label and the bar, and grows upward from this line.
const TILE_BOTTOM_Y: f32 = 836.0;

/// How long, in seconds, a request holds the gate closed when the bridge
/// answers nothing. An item with no trickplay set, or a sheet the bridge cannot
/// read, draws no reply. Without this limit one such request would stop every
/// later request for the rest of the item.
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

/// One request, as the words the bridge reads. Every argument travels as a
/// string.
fn request(kind: &str, arguments: &[String]) -> Command {
    let mut words = vec![json!("script-message"), json!(REQUEST), json!(kind)];
    words.extend(arguments.iter().map(|word| json!(word)));
    words
}

/// What the bridge may hand a bitmap in one go. A toolkit image at or over
/// this size uploads on a thread of its own and draws no earlier than the next
/// frame, so a picture that large is read as bands under the limit.
// PROSE: the band split is the port's own: the Lua handed mpv one file and mpv did the upload.
const MAX_SYNC: usize = 2 * 1024 * 1024;

/// One band of a bitmap: the row it starts at, the rows it covers, and the
/// toolkit image it was read into.
#[derive(Debug)]
struct Band {
    top: u32,
    height: u32,
    handle: Handle,
}

/// One decoded picture the bridge answered with: its size in real pixels, and
/// the toolkit images the display read it into.
#[derive(Debug)]
pub struct Bitmap {
    pub width: u32,
    pub height: u32,
    bands: Vec<Band>,
}

impl Bitmap {
    /// Read one bgra file the bridge wrote. The reply names the size, so a file
    /// shorter than the size it states is no picture at all.
    pub fn read(path: &str, width: u32, height: u32, stride: u32) -> Option<Self> {
        Self::decode(&std::fs::read(path).ok()?, width, height, stride)
    }

    // PROSE: the bridge writes premultiplied bgra, which is what mpv's overlay took, and the toolkit blends on straight alpha, so each channel divides its alpha out here.
    fn decode(bytes: &[u8], width: u32, height: u32, stride: u32) -> Option<Self> {
        let (rows, row) = (height as usize, width as usize * 4);
        if width == 0 || height == 0 || (stride as usize) < row {
            return None;
        }
        if bytes.len() < stride as usize * rows {
            return None;
        }

        let per_band = ((MAX_SYNC - 1) / row).max(1);
        let bands = (0..rows)
            .step_by(per_band)
            .map(|top| {
                let height = per_band.min(rows - top);
                let mut pixels = Vec::with_capacity(height * row);
                for y in top..top + height {
                    let line = &bytes[y * stride as usize..][..row];
                    for pixel in line.chunks_exact(4) {
                        let alpha = pixel[3];
                        pixels.extend_from_slice(&[
                            straight(pixel[2], alpha),
                            straight(pixel[1], alpha),
                            straight(pixel[0], alpha),
                            alpha,
                        ]);
                    }
                }
                Band {
                    top: top as u32,
                    height: height as u32,
                    handle: Handle::from_rgba(width, height as u32, pixels),
                }
            })
            .collect();
        Some(Self {
            width,
            height,
            bands,
        })
    }

    /// Draw the picture at the place the formulas give it, in real pixels. The
    /// bridge decodes every picture to the pixel size the screen takes, so one
    /// pixel of it covers one output pixel.
    pub fn draw(&self, brush: &mut Brush<'_>, at: Point) {
        let scale = brush.canvas().scale;
        for band in &self.bands {
            brush.image(
                Rectangle::new(
                    Point::new(at.x / scale, (at.y + band.top as f32) / scale),
                    Size::new(self.width as f32 / scale, band.height as f32 / scale),
                ),
                &band.handle,
            );
        }
    }

    /// The rows the picture covers in canvas units, which the header measures
    /// its second line down from.
    pub fn canvas_height(&self, canvas: &Canvas) -> f32 {
        (self.height as f32 / canvas.scale + 0.5).floor()
    }
}

fn straight(channel: u8, alpha: u8) -> u8 {
    match alpha {
        0 => 0,
        _ => ((u32::from(channel) * 255 + u32::from(alpha) / 2) / u32::from(alpha)).min(255) as u8,
    }
}

/// The logo, the cover, and the offer's picture each ask for one box and hold
/// what came back.
#[derive(Debug, Default)]
struct Slot {
    /// The pixel size last asked for, so an unchanged size is not asked for
    /// again.
    want: Option<String>,
    /// The decoded picture the bridge returned. It is nothing until the bridge
    /// answers, and a swap clears it.
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

/// The display's half of the art bridge: what each consumer asked for, and
/// what came back.
#[derive(Debug, Default)]
pub struct Art {
    /// The logo the current item declared, nothing when it has none.
    logo_uri: Option<String>,
    logo: Slot,
    tile: Slot,
    /// The gate. `inflight` is the key of the request the bridge is answering,
    /// and `tile_want` is the newest request the display asks for. One request
    /// is in flight at a time, so a held scrub sends one request per round trip,
    /// and the last reply is for the newest position. Without the gate a hold
    /// sends about thirty requests a second, the bridge serves them in order,
    /// and the tile keeps moving after the hand lifts. An unchanged second and
    /// box are not asked for again.
    inflight: Option<String>,
    tile_want: Option<Wanted>,
    // PROSE: the gate reopens at a deadline the next turn reads, because this client holds no timer of its own.
    deadline: Option<f64>,
    cover: Slot,
    /// The box the bridge has already answered for. A redraw then asks once,
    /// and an answer of no cover ends the asking instead of starting it again.
    answered: Option<String>,
    next: Slot,
    /// Whether an offer with a picture stands, so a reply for an offer that no
    /// longer stands is dropped.
    offered: bool,
    // PROSE: the canvas the last requests were sized for; the Lua read osd-dimensions, and this client learns its own surface from the frame it draws.
    canvas: Option<Canvas>,
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
    /// A stale reply for the old item then does not land on the new one. The
    /// in-flight mark goes as well, so the new item's first request goes out
    /// even when the old item's last request drew no reply.
    ///
    /// The offer's picture is named by its box alone and serves the whole run,
    /// so an item swap leaves it where it is.
    pub fn on_item(&mut self, presentation: &Presentation, canvas: &Canvas) -> Vec<Command> {
        self.logo_uri = match presentation.is_music() {
            true => None,
            false => presentation.logo().map(str::to_string),
        };
        self.logo = Slot::default();
        self.tile = Slot::default();
        self.tile_want = None;
        self.clear_inflight();
        self.cover = Slot::default();
        self.answered = None;
        self.canvas = Some(*canvas);
        self.logo_request(canvas).into_iter().collect()
    }

    /// The frame's own turn: ask for what this state needs, the way the Lua
    /// runs its four syncs at the top of a redraw.
    ///
    /// `preview` carries the target time while a fine scan is in flight for an
    /// item that declares trickplay, and nothing at every other moment.
    /// `offered` says an offer with a picture stands.
    pub fn sync(
        &mut self,
        presentation: &Presentation,
        canvas: &Canvas,
        preview: Option<f64>,
        offered: bool,
        now: f64,
    ) -> Vec<Command> {
        self.offered = offered;
        let mut commands = Vec::new();
        commands.extend(self.on_resize(canvas));
        if self.deadline.is_some_and(|deadline| now >= deadline) {
            self.clear_inflight();
            self.tile_want = None;
        }
        if presentation.is_music() {
            commands.extend(self.cover_request(canvas));
        }
        if let Some(time) = preview {
            commands.extend(self.tile_request(time, canvas, now));
        }
        if offered {
            commands.extend(self.next_request(canvas));
        }
        commands
    }

    /// The screen size changed. Every consumer asks at the new box, and each
    /// keeps the picture it holds on screen until the new one arrives, so
    /// nothing blinks on a resize.
    ///
    /// The cover forgets no key. A new box differs from the one in flight and
    /// from the one answered, so its own request asks on its own, and a change
    /// that leaves the box where it was asks nothing.
    fn on_resize(&mut self, canvas: &Canvas) -> Option<Command> {
        if self.canvas == Some(*canvas) {
            return None;
        }
        self.canvas = Some(*canvas);
        self.logo.want = None;
        self.tile_want = None;
        self.clear_inflight();
        self.next.want = None;
        self.logo_request(canvas)
    }

    /// Ask the bridge for the current logo at the pixel size the screen needs.
    /// It sends nothing when the item has no logo, when the screen size gives
    /// no box, or when the header already holds the logo at that size.
    fn logo_request(&mut self, canvas: &Canvas) -> Option<Command> {
        self.logo_uri.as_ref()?;
        let (width, height) = (pixels(LOGO_MAX_W, canvas), pixels(LOGO_MAX_H, canvas));
        let key = box_key(width, height)?;
        if self.logo.want.as_deref() == Some(key.as_str()) && self.logo.bitmap.is_some() {
            return None;
        }
        self.logo.want = Some(key);
        Some(request(LOGO, &[width.to_string(), height.to_string()]))
    }

    /// Ask the bridge for the playing track's cover at the pixel box the region
    /// gives it. The bridge fits the cover inside the box and keeps the aspect,
    /// so the returned bitmap may be smaller. It sends nothing before the
    /// screen size gives a box, or when the same box is already in flight or
    /// already answered.
    fn cover_request(&mut self, canvas: &Canvas) -> Option<Command> {
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
        self.cover.want = Some(key);
        Some(request(ALBUM, &[width.to_string(), height.to_string()]))
    }

    /// Ask the bridge for the tile at the target time and pixel box. Quantize
    /// the request to a whole second, so a scrub within one second sends
    /// nothing and the IPC does not flood. A new second, or a new box, asks
    /// again. While a request is in flight, a new want is recorded and not
    /// sent; the reply sends it when it arrives.
    fn tile_request(&mut self, time: f64, canvas: &Canvas, now: f64) -> Option<Command> {
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

    /// Ask the bridge for the offer's art at the pixel size the art box takes
    /// on this screen, once per size. The bridge fits the picture inside the box
    /// and keeps its ratio, so a poster comes back letterboxed.
    fn next_request(&mut self, canvas: &Canvas) -> Option<Command> {
        let (width, height) = (pixels(CARD_W, canvas), pixels(ART_H, canvas));
        let key = box_key(width, height)?;
        if self.next.want.as_deref() == Some(key.as_str()) {
            return None;
        }
        self.next.want = Some(key);
        Some(request(NEXT, &[width.to_string(), height.to_string()]))
    }

    /// Send one tile request and mark it in flight. The deadline clears the
    /// mark, and the want with it, when no reply arrives within the timeout. It
    /// clears the want as well, so the next turn asks again even at the same
    /// second; the tile the bridge never answered is otherwise never asked for
    /// until the cursor moves.
    fn send_tile(&mut self, wanted: &Wanted, now: f64) -> Command {
        self.inflight = Some(wanted.key.clone());
        self.deadline = Some(now + REPLY_TIMEOUT);
        request(
            TRICKPLAY,
            &[
                wanted.ms.to_string(),
                wanted.width.to_string(),
                wanted.height.to_string(),
            ],
        )
    }

    /// Forget the request in flight and its deadline, so the next want goes out
    /// at once.
    fn clear_inflight(&mut self) {
        self.inflight = None;
        self.deadline = None;
    }

    /// One `liken-art` reply. Each consumer takes its own kind, so a misrouted
    /// reply draws nothing. The answer says whether anything the display draws
    /// changed, and the reply the tile's gate holds open may send the next
    /// request.
    pub fn on_reply(&mut self, words: &[String], now: f64) -> (bool, Vec<Command>) {
        let [name, kind, path, width, height, stride] = words else {
            return (false, Vec::new());
        };
        if name != REPLY {
            return (false, Vec::new());
        }
        let size = || {
            Some((
                width.parse().ok()?,
                height.parse().ok()?,
                stride.parse().ok()?,
            ))
        };
        let read = || {
            let (width, height, stride) = size()?;
            Bitmap::read(path, width, height, stride)
        };

        match kind.as_str() {
            // A reply for an item that no longer has a logo is dropped.
            LOGO if self.logo_uri.is_some() => self.logo.bitmap = read(),
            // The bridge answers every request, and an empty path is its
            // answer for an item it found no cover for. Either answer marks
            // the box answered, so the display asks once for a box, and a
            // missing cover ends the asking.
            ALBUM => {
                self.answered = self.cover.want.clone();
                self.cover.bitmap = read();
            }
            // An empty path is the bridge's answer for an offer whose art it
            // could not read.
            NEXT if self.offered => self.next.bitmap = read(),
            // The reply carries no key, so the request it answers is the one
            // marked in flight. When the want names a different tile, it goes
            // out here: this is the one request per round trip.
            TRICKPLAY => {
                let answered = self.inflight.take();
                self.deadline = None;
                self.tile.bitmap = read();
                let wanted = self
                    .tile_want
                    .clone()
                    .filter(|want| Some(&want.key) != answered.as_ref());
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

    /// One bgra file the bridge could have written, and the path it wrote it
    /// to.
    fn written(name: &str, pixels: &[u8]) -> String {
        let path = std::env::temp_dir().join(format!("media-osd-{name}.bgra"));
        std::fs::write(&path, pixels).expect("a file to read back");
        path.to_string_lossy().to_string()
    }

    fn block(text: &str) -> Presentation {
        let mut presentation = Presentation::default();
        presentation.receive(text);
        presentation
    }

    fn canvas() -> Canvas {
        Canvas::for_output(Size::new(1920.0, 1080.0))
    }

    /// The pixels one toolkit image carries. A handle takes an id of its own
    /// on every call, so two handles never compare equal and the pixels are
    /// what a test reads.
    fn read_back(handle: &Handle) -> Vec<u8> {
        match handle {
            Handle::Rgba { pixels, .. } => pixels.to_vec(),
            _ => Vec::new(),
        }
    }

    /// One reply, as the words the bridge sends.
    fn reply(kind: &str, path: &str, width: u32, height: u32, stride: u32) -> Vec<String> {
        [
            REPLY,
            kind,
            path,
            &width.to_string(),
            &height.to_string(),
            &stride.to_string(),
        ]
        .iter()
        .map(|word| word.to_string())
        .collect()
    }

    /// The words one command carries, so a request reads as the strings the
    /// bridge parses.
    fn words(command: &Command) -> Vec<String> {
        command
            .iter()
            .map(|word| word.as_str().unwrap_or_default().to_string())
            .collect()
    }

    #[test]
    fn every_kind_asks_in_the_form_the_bridge_parses() {
        let mut art = Art::default();
        let commands = art.on_item(&block(r#"{"logo":"/art/logo.png"}"#), &canvas());
        assert_eq!(
            commands.iter().map(words).collect::<Vec<_>>(),
            vec![vec![
                "script-message".to_string(),
                "liken-art-request".to_string(),
                "logo".to_string(),
                "760".to_string(),
                "110".to_string(),
            ]]
        );

        let cover = art.sync(&block(r#"{"type":"music"}"#), &canvas(), None, false, 0.0);
        assert_eq!(
            words(&cover[0]),
            [
                "script-message",
                "liken-art-request",
                "album",
                "1728",
                "532"
            ]
        );

        let tile = art.sync(&block("{}"), &canvas(), Some(12.0), false, 0.0);
        assert_eq!(
            words(&tile[0]),
            [
                "script-message",
                "liken-art-request",
                "trickplay",
                "12000",
                "360",
                "220"
            ]
        );

        let next = art.sync(&block("{}"), &canvas(), None, true, 0.0);
        assert_eq!(
            words(&next[0]),
            ["script-message", "liken-art-request", "next", "440", "248"]
        );
    }

    /// A 4K surface asks for every box at twice the canvas measure.
    #[test]
    fn a_larger_surface_asks_for_a_larger_box() {
        let canvas = Canvas::for_output(Size::new(3840.0, 2160.0));
        let mut art = Art::default();
        let logo = art.on_item(&block(r#"{"logo":"/art/logo.png"}"#), &canvas);
        assert_eq!(
            words(&logo[0])[3..],
            ["1520".to_string(), "220".to_string()]
        );
        let cover = art.sync(&block(r#"{"type":"music"}"#), &canvas, None, false, 0.0);
        assert_eq!(
            words(&cover[0])[3..],
            ["3456".to_string(), "1064".to_string()]
        );
    }

    /// A music item asks for no logo at all, and an item with none asks for
    /// nothing.
    #[test]
    fn only_an_item_that_declares_a_logo_asks_for_one() {
        for text in [r#"{"type":"music","logo":"/art/logo.png"}"#, "{}"] {
            assert!(
                Art::default().on_item(&block(text), &canvas()).is_empty(),
                "{text}"
            );
        }
    }

    /// The header asks once for a size it already holds, and asks again for
    /// every other item.
    #[test]
    fn the_logo_asks_once_for_a_size_it_holds() {
        let mut art = Art::default();
        let item = block(r#"{"logo":"/art/logo.png"}"#);
        assert_eq!(art.on_item(&item, &canvas()).len(), 1);
        assert!(art.sync(&item, &canvas(), None, false, 0.0).is_empty());

        let path = written("logo", &[0, 0, 0, 0]);
        assert!(art.on_reply(&reply(LOGO, &path, 1, 1, 4), 0.0).0);
        assert!(art.logo().is_some());
        assert_eq!(art.on_item(&item, &canvas()).len(), 1);
        assert!(art.logo().is_none());
    }

    /// The cover asks once for a box, and the bridge's empty path ends the
    /// asking.
    #[test]
    fn an_empty_cover_reply_ends_the_asking() {
        let mut art = Art::default();
        let item = block(r#"{"type":"music"}"#);
        assert_eq!(art.sync(&item, &canvas(), None, false, 0.0).len(), 1);
        assert!(art.sync(&item, &canvas(), None, false, 0.0).is_empty());

        assert!(art.on_reply(&reply(ALBUM, "", 0, 0, 0), 0.0).0);
        assert!(art.cover().is_none());
        assert!(art.sync(&item, &canvas(), None, false, 0.0).is_empty());
    }

    /// A new item drops the cover it holds and asks again, so a film after a
    /// music item carries no cover parked over it.
    #[test]
    fn a_new_item_drops_the_cover_and_asks_again() {
        let mut art = Art::default();
        let item = block(r#"{"type":"music"}"#);
        let _ = art.sync(&item, &canvas(), None, false, 0.0);
        let path = written("cover", &[0, 0, 0, 255]);
        let _ = art.on_reply(&reply(ALBUM, &path, 1, 1, 4), 0.0);
        assert!(art.cover().is_some());

        assert!(art.on_item(&block("{}"), &canvas()).is_empty());
        assert!(art.cover().is_none());
        assert_eq!(art.sync(&item, &canvas(), None, false, 0.0).len(), 1);
    }

    /// The offer's picture is named by its box alone and serves the whole run,
    /// so it asks once and an item swap leaves it where it is.
    #[test]
    fn the_offers_picture_asks_once_and_outlives_an_item() {
        let mut art = Art::default();
        assert_eq!(art.sync(&block("{}"), &canvas(), None, true, 0.0).len(), 1);
        assert!(
            art.sync(&block("{}"), &canvas(), None, true, 0.0)
                .is_empty()
        );

        let path = written("next", &[0, 0, 0, 255]);
        assert!(art.on_reply(&reply(NEXT, &path, 1, 1, 4), 0.0).0);
        assert!(art.next().is_some());
        assert!(art.on_item(&block("{}"), &canvas()).is_empty());
        assert!(art.next().is_some());
    }

    /// A reply for an offer that no longer stands, and one for an item with no
    /// logo, are dropped.
    #[test]
    fn a_reply_for_something_that_no_longer_stands_is_dropped() {
        let path = written("dropped", &[0, 0, 0, 255]);
        let mut art = Art::default();
        assert!(!art.on_reply(&reply(NEXT, &path, 1, 1, 4), 0.0).0);
        assert!(art.next().is_none());
        assert!(!art.on_reply(&reply(LOGO, &path, 1, 1, 4), 0.0).0);
        assert!(art.logo().is_none());
    }

    /// A line that is not a reply, and a kind no consumer takes, move nothing.
    #[test]
    fn a_line_that_is_not_a_reply_moves_nothing() {
        let mut art = Art::default();
        for words in [
            vec!["summon".to_string()],
            reply("nothing", "/art/x.bgra", 1, 1, 4),
            ["presentation", "{}", "", "", "", ""]
                .iter()
                .map(|word| word.to_string())
                .collect(),
        ] {
            assert_eq!(art.on_reply(&words, 0.0), (false, Vec::new()));
        }
    }

    /// Three turns during one request in flight send one request, and the reply
    /// sends the newest want.
    #[test]
    fn one_tile_request_stands_in_flight_at_a_time() {
        let mut art = Art::default();
        let item = block("{}");
        let mut sent = Vec::new();
        for time in [10.0, 10.2, 12.0] {
            sent.extend(art.sync(&item, &canvas(), Some(time), false, 0.0));
        }
        assert_eq!(sent.len(), 1);
        assert_eq!(words(&sent[0])[3], "10000");

        let path = written("tile", &[0, 0, 0, 255]);
        let (changed, more) = art.on_reply(&reply(TRICKPLAY, &path, 1, 1, 4), 0.0);
        assert!(changed);
        assert_eq!(more.len(), 1);
        assert_eq!(words(&more[0])[3], "12000");
        assert!(art.tile().is_some());
    }

    /// A reply for the want the display holds sends nothing more.
    #[test]
    fn a_reply_for_the_want_the_display_holds_sends_nothing() {
        let mut art = Art::default();
        let item = block("{}");
        let mut sent = Vec::new();
        for time in [10.0, 10.2, 10.4, 10.49] {
            sent.extend(art.sync(&item, &canvas(), Some(time), false, 0.0));
        }
        let path = written("tile", &[0, 0, 0, 255]);
        let (_, more) = art.on_reply(&reply(TRICKPLAY, &path, 1, 1, 4), 0.0);
        assert_eq!(sent.len(), 1);
        assert!(more.is_empty());
    }

    /// The timeout reopens the gate when no reply arrives, and the next turn
    /// asks again at the second it stands on.
    #[test]
    fn the_timeout_reopens_the_gate() {
        let mut art = Art::default();
        let item = block("{}");
        assert_eq!(art.sync(&item, &canvas(), Some(10.0), false, 0.0).len(), 1);
        assert!(
            art.sync(&item, &canvas(), Some(12.0), false, 0.5)
                .is_empty()
        );

        let sent = art.sync(&item, &canvas(), Some(12.0), false, 1.0);
        assert_eq!(sent.len(), 1);
        assert_eq!(words(&sent[0])[3], "12000");
    }

    /// A new item reopens the gate, so the new item's first request goes out
    /// even when the old item's last request drew no reply.
    #[test]
    fn a_new_item_reopens_the_tile_gate() {
        let mut art = Art::default();
        let item = block("{}");
        assert_eq!(art.sync(&item, &canvas(), Some(10.0), false, 0.0).len(), 1);
        assert!(art.on_item(&item, &canvas()).is_empty());

        let sent = art.sync(&item, &canvas(), Some(20.0), false, 0.1);
        assert_eq!(sent.len(), 1);
        assert_eq!(words(&sent[0])[3], "20000");
    }

    /// A resize reopens the gate and asks at the new box, and the logo and the
    /// offer ask again as well.
    #[test]
    fn a_resize_asks_every_consumer_again() {
        let wide = Canvas::for_output(Size::new(3840.0, 2160.0));
        let mut art = Art::default();
        let item = block(r#"{"logo":"/art/logo.png"}"#);
        let _ = art.on_item(&item, &canvas());
        let _ = art.sync(&item, &canvas(), Some(10.0), true, 0.0);

        let sent = art.sync(&item, &wide, Some(10.0), true, 0.1);
        let asked: Vec<Vec<String>> = sent.iter().map(words).collect();
        assert_eq!(asked[0][2..], ["logo", "1520", "220"]);
        assert_eq!(asked[1][2..], ["trickplay", "10000", "720", "440"]);
        assert_eq!(asked[2][2..], ["next", "880", "496"]);
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
        assert!(
            art.on_item(&block(r#"{"logo":"/art/logo.png"}"#), &flat)
                .is_empty()
        );
        assert!(
            art.sync(&block(r#"{"type":"music"}"#), &flat, Some(1.0), true, 0.0)
                .is_empty()
        );
    }

    /// The bridge writes premultiplied bgra, and the toolkit blends on straight
    /// alpha, so each channel divides its alpha out.
    #[test]
    fn a_bgra_file_reads_as_straight_rgba() {
        // One opaque red pixel, one half-covered white one, and one clear.
        let pixels = [0, 0, 255, 255, 128, 128, 128, 128, 0, 0, 0, 0];
        let path = written("straight", &pixels);
        let bitmap = Bitmap::read(&path, 3, 1, 12).expect("a picture the display can draw");
        assert_eq!(bitmap.width, 3);
        assert_eq!(bitmap.height, 1);
        assert_eq!(
            read_back(&bitmap.bands[0].handle),
            vec![255, 0, 0, 255, 255, 255, 255, 128, 0, 0, 0, 0]
        );
    }

    /// A file shorter than the size the reply states is no picture at all, and
    /// neither is a size of nothing or a path that names no file.
    #[test]
    fn a_file_that_does_not_match_its_reply_is_no_picture() {
        let path = written("short", &[0, 0, 0, 255]);
        assert!(Bitmap::read(&path, 2, 1, 8).is_none());
        assert!(Bitmap::read(&path, 0, 1, 4).is_none());
        assert!(Bitmap::read(&path, 1, 0, 4).is_none());
        assert!(Bitmap::read(&path, 2, 1, 4).is_none());
        assert!(Bitmap::read("/art/nothing.bgra", 1, 1, 4).is_none());
    }

    /// A picture too large to upload inside one frame reads as bands under the
    /// limit, and the bands cover every row once.
    #[test]
    fn a_large_picture_reads_as_bands() {
        let (width, height) = (1024u32, 700u32);
        let path = written("bands", &vec![255; (width * height * 4) as usize]);
        let bitmap = Bitmap::read(&path, width, height, width * 4).expect("a picture in bands");
        assert!(bitmap.bands.len() > 1);
        assert_eq!(bitmap.bands[0].top, 0);
        assert_eq!(
            bitmap.bands.iter().map(|band| band.height).sum::<u32>(),
            height
        );
        for pair in bitmap.bands.windows(2) {
            assert_eq!(pair[0].top + pair[0].height, pair[1].top);
        }
    }

    /// A small picture is one band, and its rows measure in canvas units at the
    /// surface's own scale.
    #[test]
    fn a_small_picture_is_one_band() {
        let path = written("small", &[0; 4 * 8 * 4]);
        let bitmap = Bitmap::read(&path, 4, 8, 16).expect("one band");
        assert_eq!(bitmap.bands.len(), 1);
        assert_eq!(bitmap.canvas_height(&canvas()), 8.0);
        assert_eq!(
            bitmap.canvas_height(&Canvas::for_output(Size::new(3840.0, 2160.0))),
            4.0
        );
    }

    /// The logo sits at the title's own corner, in real pixels.
    #[test]
    fn the_logo_sits_at_the_title_corner() {
        assert_eq!(logo_at(&canvas()), Point::new(96.0, 90.0));
        assert_eq!(
            logo_at(&Canvas::for_output(Size::new(3840.0, 2160.0))),
            Point::new(192.0, 180.0)
        );
    }

    /// The tile centres on the playhead and stands on its own bottom line, and
    /// it clamps to the screen at both ends.
    #[test]
    fn the_tile_centres_on_the_playhead_and_clamps() {
        let path = written("place", &vec![0; 360 * 220 * 4]);
        let tile = Bitmap::read(&path, 360, 220, 360 * 4).expect("a tile");
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
        let path = written("tall", &vec![0; 4 * 900 * 4]);
        let tile = Bitmap::read(&path, 4, 900, 16).expect("a tall tile");
        assert_eq!(tile_at(&canvas(), &tile, 960.0).y, 0.0);
    }

    /// The cover centres in its region, and a cover larger than the screen
    /// clamps to the corner.
    #[test]
    fn the_cover_centres_in_its_region() {
        let path = written("square", &vec![0; 532 * 532 * 4]);
        let cover = Bitmap::read(&path, 532, 532, 532 * 4).expect("a cover");
        assert_eq!(cover_at(&canvas(), &cover), Point::new(694.0, 300.0));

        let path = written("huge", &vec![0; 4000 * 4]);
        let huge = Bitmap::read(&path, 1000, 1, 4000).expect("a wide cover");
        assert_eq!(cover_at(&canvas(), &huge), Point::new(460.0, 566.0));
    }
}
