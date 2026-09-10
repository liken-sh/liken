// One pass of the frame loop: the script's keys, the draw, the capture, and
// the numbers. The pass ends by setting the pace of the next one.

use iced_wgpu::graphics::Viewport;
use iced_wgpu::wgpu;
use iced_winit::core::time::Instant;
use iced_winit::core::{Color, Event, Size, Theme, mouse, renderer, window};
use iced_winit::runtime::user_interface::UserInterface;
use iced_winit::winit::event_loop::{ActiveEventLoop, ControlFlow};

use super::capture::{self, Captures};
use super::graphics::configure;
use super::stats::millis;
use super::timeline::{self, Wake};
use super::{QUIT, Ready, Screen};

/// The least time between two frames, one sixtieth of a second. The surface
/// presents without vsync, so this floor is the whole of the frame-rate cap:
/// an animation that answers "now" on every ask draws sixty frames a second
/// and not as many as the loop can spin.
pub const STEP: f64 = 1.0 / 60.0;

/// The viewport's logical size in whole pixels, which is what a screen lays
/// out in.
pub(crate) fn logical(viewport: &Viewport) -> (u32, u32) {
    let size = viewport.logical_size();
    (size.width.round() as u32, size.height.round() as u32)
}

impl<S: Screen> Ready<S> {
    /// The scale the run lays out at: the one --scale stated, or the one
    /// the compositor states for the window's output.
    pub(crate) fn scale(&self) -> f32 {
        self.scale
            .unwrap_or_else(|| self.window.scale_factor() as f32)
    }

    /// Hand one key to the screen. The answer is true when the key ends the
    /// run. Both the keyboard and the script arrive here, so the key that
    /// ends a run is decided once for the two of them.
    pub(crate) fn press(&mut self, name: &str) -> bool {
        if name == QUIT {
            return true;
        }
        // The screen's clock moves before the key lands. A screen at rest
        // draws no frames, so its clock stands at the last frame, which
        // can be seconds old, and a motion the key starts would begin in
        // the past and land fully run on its first frame. Before the first
        // frame there is no clock, and the key waits on nothing.
        if let Some(start) = self.start {
            self.screen.tick(start.elapsed().as_secs_f64());
        }
        // A press that changes the screen makes the frame on the glass
        // stale and drops the second the screen named before it. A press
        // that changes nothing, such as an arrow at the edge of the
        // keyboard grid, leaves both alone and draws no frame.
        if self.screen.key(name) {
            self.scheduled = None;
            self.stale = true;
        }
        false
    }

    /// Write the numbers and leave the loop.
    pub(crate) fn stop(&mut self, event_loop: &ActiveEventLoop) {
        self.finish();
        event_loop.exit();
    }

    /// Build, draw, capture, and present one frame.
    pub(crate) fn frame(&mut self, event_loop: &ActiveEventLoop) {
        let loop_start = std::time::Instant::now();
        let at = match self.start {
            Some(start) => start.elapsed().as_secs_f64(),
            None => {
                self.start = Some(loop_start);
                self.stats
                    .first_frame(millis(self.launched.elapsed()) / 1000.0);
                0.0
            }
        };

        self.screen.tick(at);

        for key in self.timeline.due(at) {
            if self.press(&key) {
                self.stop(event_loop);
                return;
            }
        }
        self.stats.sample_rss(at);

        if self.resized {
            let size = self.window.inner_size();
            let (width, height) = (size.width.max(1), size.height.max(1));
            self.viewport = Viewport::with_physical_size(Size::new(width, height), self.scale());
            self.screen
                .scaled(logical(&self.viewport), self.viewport.scale_factor());
            configure(&self.surface, &self.device, self.format, width, height);
            self.stats.resized((width, height));
            self.resized = false;
        }

        // Nothing under a cover is drawn. The keys, the resize, and the
        // deadline above and below still ran, so a scripted run under a
        // cover ends on time, and the glass stays stale, so the uncover
        // draws one frame with everything that changed.
        if self.screen.covered() {
            if self.timeline.past_deadline(at) {
                self.stop(event_loop);
            }
            return;
        }

        // This frame is the one the schedule asked for, so the schedule is
        // spent and the next pass asks the screen again. It draws every fold
        // so far, so the glass is current again.
        self.scheduled = None;
        self.stale = false;
        self.drawn = at;

        let frame = match self.surface.get_current_texture() {
            Ok(frame) => frame,
            Err(wgpu::SurfaceError::OutOfMemory) => {
                eprintln!("surface out of memory");
                self.stop(event_loop);
                return;
            }
            Err(_) => {
                self.resized = true;
                return;
            }
        };

        // The clock starts after the swapchain image is in hand, so the frame
        // time measures the work of a frame and not the wait for the display.
        let build_start = std::time::Instant::now();
        let view = frame
            .texture
            .create_view(&wgpu::TextureViewDescriptor::default());

        let mut interface = UserInterface::build(
            self.screen.view(),
            self.viewport.logical_size(),
            std::mem::take(&mut self.cache),
            &mut self.renderer,
        );

        let mut messages = Vec::new();
        self.events.push(Event::Window(
            window::Event::RedrawRequested(Instant::now()),
        ));
        let _ = interface.update(
            &self.events,
            mouse::Cursor::Unavailable,
            &mut self.renderer,
            &mut self.clipboard,
            &mut messages,
        );
        self.events.clear();

        interface.draw(
            &mut self.renderer,
            &Theme::Dark,
            &renderer::Style::default(),
            mouse::Cursor::Unavailable,
        );
        self.cache = interface.into_cache();

        for message in messages {
            self.screen.update(message);
        }

        let background = self.screen.background();
        // The frame is built. A capture writes a file and blocks on a readback,
        // so the clock stops here and starts again for the submit.
        let drawn_ms = millis(build_start.elapsed());

        let captured = self.capture(at, background);

        let submit_start = std::time::Instant::now();
        if !captured {
            let _ = self
                .renderer
                .present(Some(background), self.format, &view, &self.viewport);
        }
        // The frame time is the work of a frame: build the interface, draw it,
        // and submit the commands. It stops before the surface is presented,
        // because that call waits for the compositor and measures the screen's
        // rate rather than this program's cost.
        let build_ms = drawn_ms + millis(submit_start.elapsed());

        frame.present();

        // A captured frame draws twice and blocks on a readback, so it says
        // nothing about the cost of a frame and stays out of the numbers.
        self.stats
            .frame(build_ms, millis(loop_start.elapsed()), !captured);

        if self.timeline.past_deadline(at) || self.captured_everything() {
            self.stop(event_loop);
        }
    }

    /// Set the pace of the loop, and ask for the frame that pace calls for.
    ///
    /// The loop sleeps until the earliest second anything is due, so a screen
    /// at rest builds one frame a change rather than one a display refresh. A
    /// second the screen has already named holds until the clock reaches it,
    /// because a fresh answer after the clock arrived would name the change
    /// after it, and the frame would never be drawn.
    pub(crate) fn pace(&mut self, event_loop: &ActiveEventLoop) {
        // Before the first frame there is no clock to schedule against, and
        // the first frame is what starts it.
        let Some(start) = self.start else {
            event_loop.set_control_flow(ControlFlow::Poll);
            self.window.request_redraw();
            return;
        };

        let at = start.elapsed().as_secs_f64();
        // A covered screen's own schedule is not asked for, and the stale
        // glass does not wake the loop: a frame under the cover reaches
        // nobody. The script, the deadline, and the captures still do.
        let covered = self.screen.covered();
        let screen_next = match self.scheduled {
            _ if covered => None,
            Some(scheduled) => Some(scheduled),
            None => self.screen.next_frame(at),
        };
        self.scheduled = None;

        // The wake is the earliest second anything is due: a stale frame is
        // due now, and after it the screen's own change, the next script key,
        // the deadline, or the next capture. The harness's own seconds come
        // from forward-only cursors, so they are asked again on every pass,
        // and only the screen's answer is held. The floor holds every answer
        // at least [`STEP`] after the last frame, which is the frame-rate
        // cap: a burst of folds coalesces to sixty frames a second and no
        // press waits past the next one.
        let stale_now = (self.stale && !covered).then_some(at);
        let next = [
            stale_now,
            screen_next,
            self.timeline.next_due(),
            self.next_capture(),
        ]
        .into_iter()
        .flatten()
        .min_by(f64::total_cmp)
        .map(|next| next.max(self.drawn + STEP));

        match timeline::wake(self.resized, at, next) {
            Wake::Now => {
                event_loop.set_control_flow(ControlFlow::Poll);
                self.window.request_redraw();
            }
            Wake::At(next) => {
                self.scheduled = screen_next;
                event_loop.set_control_flow(ControlFlow::WaitUntil(
                    start + std::time::Duration::from_secs_f64(next),
                ));
            }
            Wake::Never => event_loop.set_control_flow(ControlFlow::Wait),
        }
    }

    /// Write this frame to a file, if a capture is due at this second. The
    /// answer is whether one was written.
    ///
    /// `Renderer::screenshot` renders the frame that was just drawn into an
    /// offscreen texture and reads it back as RGBA. It is iced's own path off
    /// the GPU, and it draws the same layers the surface would get. The
    /// surface itself is left alone on a capture frame, because one drawn
    /// frame must not be submitted twice.
    fn capture(&mut self, at: f64, background: Color) -> bool {
        let Some(path) = self.captures.as_mut().and_then(|captures| captures.due(at)) else {
            return false;
        };

        let pixels = self.renderer.screenshot(&self.viewport, background);
        let size = self.viewport.physical_size();
        capture::write_png(&path, size.width, size.height, &pixels);
        eprintln!("captured {} at {at:.3}s", path.display());
        true
    }

    /// The second of the next capture, folded into the wake time.
    fn next_capture(&self) -> Option<f64> {
        self.captures.as_ref().and_then(Captures::next_due)
    }

    /// Whether the run has taken every capture it asked for, which ends it.
    fn captured_everything(&self) -> bool {
        self.captures.as_ref().is_some_and(Captures::taken)
    }

    /// Write the statistics file once, whichever way the run ends.
    pub(crate) fn finish(&mut self) {
        if self.finished {
            return;
        }
        self.finished = true;
        self.stats.art_counts(self.screen.art_counts());
        self.stats.index_size(self.screen.index_size());
        if let Some(path) = &self.stats_path {
            self.stats.write(path);
        }
    }
}
