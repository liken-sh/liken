// Every clip the browser draws through. iced_wgpu 0.14 skips a mesh whose
// clip covers no whole pixel of its layer, and while it skips one it
// moves past the mesh's vertices but not past its indices
// (`triangle.rs`, `Pipeline::render`). Every later mesh of the layer then
// reads its triangles from the wrong place in the index buffer, and joins
// vertices of different shapes into large filled wedges across the
// frame. So a clip that covers no whole pixel of the frame draws nothing
// at all, and no mesh reaches the renderer with a clip it would skip.

use std::cell::Cell;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Rectangle, Size};

thread_local! {
    // The size of the canvas the outermost clip was made on, while a clip
    // draws. A clip made inside another is a frame the size of the outer
    // clip, but the renderer tests every mesh's clip against the whole
    // canvas, so a nested clip is tested against the outermost size.
    static CANVAS: Cell<Option<Size>> = const { Cell::new(None) };
}

/// Draw `draw` inside `clip`, as `Frame::with_clip` does, or nothing
/// where `clip` covers no whole pixel of the canvas. The answer is what
/// `draw` answers, and its default where nothing draws.
pub fn within<R: Default>(
    frame: &mut canvas::Frame<Renderer>,
    clip: Rectangle,
    draw: impl FnOnce(&mut canvas::Frame<Renderer>) -> R,
) -> R {
    let outer = CANVAS.get();
    let Some(size) = admitted(clip, outer, frame.size()) else {
        return R::default();
    };
    CANVAS.set(Some(size));
    let drawn = frame.with_clip(clip, draw);
    CANVAS.set(outer);
    drawn
}

// The size of the canvas a clip is tested against, where the clip covers
// a whole pixel of it: the outermost canvas while another clip draws,
// and this frame's own size where none does.
fn admitted(clip: Rectangle, outer: Option<Size>, frame: Size) -> Option<Size> {
    let size = outer.unwrap_or(frame);
    covers(clip, size).then_some(size)
}

// Whether the clip covers at least one whole pixel of a frame this size,
// the test the renderer applies before it draws a mesh. It is made in
// logical pixels, so on a scaled screen it holds back a clip a little
// smaller than the renderer would, which draws nothing either way.
fn covers(clip: Rectangle, size: Size) -> bool {
    let frame = Rectangle::new(iced_winit::core::Point::ORIGIN, size);
    clip.intersection(&frame)
        .and_then(Rectangle::snap)
        .is_some()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::views::area;

    const FRAME: Size = Size {
        width: 1920.0,
        height: 1080.0,
    };

    #[test]
    fn a_clip_draws_only_where_it_covers_a_whole_pixel_of_the_frame() {
        let cases = [
            (area(96.0, 154.0, 1728.0, 926.0), true),
            (area(0.0, 154.0, 1920.0, 0.0), false),
            (area(96.0, 154.0, 0.0, 96.0), false),
            (area(96.0, 154.0, 1728.0, 0.4), false),
            (area(96.0, -400.0, 1728.0, 300.0), false),
            (area(96.0, 1200.0, 1728.0, 300.0), false),
            (area(2000.0, 154.0, 300.0, 300.0), false),
            (area(96.0, -40.0, 1728.0, 300.0), true),
        ];
        for (clip, drawn) in cases {
            assert_eq!(covers(clip, FRAME), drawn, "{clip:?}");
        }
    }

    // A clip inside another is a frame only as large as the outer clip,
    // so a caption's band low on the screen lies past that frame's own
    // size and is still on the canvas.
    #[test]
    fn a_nested_clip_is_tested_against_the_whole_canvas() {
        let region = Size::new(1728.0, 926.0);
        let caption = area(96.0, 1000.0, 180.0, 30.0);
        assert_eq!(admitted(caption, Some(FRAME), region), Some(FRAME));
        assert_eq!(admitted(caption, None, region), None);
        assert_eq!(admitted(caption, None, FRAME), Some(FRAME));
        assert_eq!(
            admitted(area(0.0, 154.0, 1920.0, 0.0), Some(FRAME), region),
            None
        );
    }

    // One clip made straight on the frame brings the wedges back, so no
    // source file but this one names the frame's own clip.
    #[test]
    fn every_clip_goes_through_within() {
        let root = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("src");
        let mut stack = vec![root];
        let mut offenders = Vec::new();
        while let Some(dir) = stack.pop() {
            for entry in std::fs::read_dir(&dir).expect("the source directory reads") {
                let path = entry.expect("an entry").path();
                if path.is_dir() {
                    stack.push(path);
                } else if path.extension().is_some_and(|ext| ext == "rs")
                    && !path.ends_with("views/clip.rs")
                    && std::fs::read_to_string(&path)
                        .expect("a source file reads")
                        .contains(concat!(".with", "_clip("))
                {
                    offenders.push(path);
                }
            }
        }
        assert!(offenders.is_empty(), "{offenders:?}");
    }
}
