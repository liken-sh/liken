// One pass of the frame loop: the script's keys, the draw, the capture, and
// the numbers. The pass ends by setting the pace of the next one.

use iced_wgpu::graphics::Viewport;
use iced_wgpu::wgpu;
use iced_winit::core::time::Instant;
use iced_winit::core::{Color, Event, Size, Theme, mouse, renderer, window};
use iced_winit::runtime::user_interface::UserInterface;
use iced_winit::winit::event_loop::{ActiveEventLoop, ControlFlow};

use std::sync::Arc;

#[cfg(feature = "measure")]
use super::capture::{self, Captures};
use super::graphics::{self, configure};
#[cfg(feature = "measure")]
use super::stats::millis;
use super::timeline::{self, Wake};
use super::{QUIT, Ready, Screen};

/// The least time between two frames, one sixtieth of a second. The surface
/// presents without vsync, so this floor is the whole of the frame-rate cap:
/// an animation that answers "now" on every ask draws sixty frames a second
/// and not as many as the loop can spin.
pub const STEP: f64 = 1.0 / 60.0;

impl<S: Screen> Ready<S> {
    /// Hand one key to the screen. The answer is true when the key ends the
    /// run. Both the keyboard and the script arrive here, so the key that
    /// ends a run is decided once for the two of them.
    pub(crate) fn press(&mut self, name: &str) -> bool {
        if name == QUIT {
            return true;
        }
        // A key can change what the screen draws next, so the second it named
        // before the press no longer holds.
        self.scheduled = None;
        self.screen.key(name);
        false
    }

    /// Write the numbers and leave the loop. The mark says the run ended on its
    /// own terms, so a loop that returns without it is a window that went away.
    pub(crate) fn stop(&mut self, event_loop: &ActiveEventLoop) {
        self.stopped = true;
        self.finish();
        event_loop.exit();
    }

    /// Build, draw, capture, and present one frame.
    pub(crate) fn frame(&mut self, event_loop: &ActiveEventLoop) {
        // This frame is the one the schedule asked for, so the schedule is
        // spent and the next pass asks the screen again.
        self.scheduled = None;
        let loop_start = std::time::Instant::now();
        let at = match self.start {
            Some(start) => start.elapsed().as_secs_f64(),
            None => {
                self.start = Some(loop_start);
                #[cfg(feature = "measure")]
                if let Some(stats) = &mut self.stats {
                    stats.first_frame();
                }
                0.0
            }
        };

        self.drawn = at;

        for key in self.timeline.due(at) {
            if self.press(&key) {
                self.stop(event_loop);
                return;
            }
        }

        self.screen.tick(at);
        #[cfg(feature = "measure")]
        if let Some(stats) = &mut self.stats {
            stats.sample_rss(at);
        }

        if self.resized {
            let size = self.graphics.window.inner_size();
            let (width, height) = (size.width.max(1), size.height.max(1));
            self.viewport = Viewport::with_physical_size(
                Size::new(width, height),
                self.graphics.window.scale_factor() as f32,
            );
            configure(
                &self.graphics.surface,
                &self.graphics.device,
                self.graphics.format,
                width,
                height,
            );
            #[cfg(feature = "measure")]
            if let Some(stats) = &mut self.stats {
                stats.resized((width, height));
            }
            self.resized = false;
        }

        let frame = match self.graphics.surface.get_current_texture() {
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
        #[cfg(feature = "measure")]
        let build_start = std::time::Instant::now();
        let view = frame
            .texture
            .create_view(&wgpu::TextureViewDescriptor::default());

        let mut interface = UserInterface::build(
            self.screen.view(),
            self.viewport.logical_size(),
            std::mem::take(&mut self.cache),
            &mut self.graphics.renderer,
        );

        let mut messages = Vec::new();
        self.events.push(Event::Window(
            window::Event::RedrawRequested(Instant::now()),
        ));
        let _ = interface.update(
            &self.events,
            mouse::Cursor::Unavailable,
            &mut self.graphics.renderer,
            &mut self.clipboard,
            &mut messages,
        );
        self.events.clear();

        interface.draw(
            &mut self.graphics.renderer,
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
        #[cfg(feature = "measure")]
        let drawn_ms = millis(build_start.elapsed());

        let captured = self.capture(at, background);

        #[cfg(feature = "measure")]
        let submit_start = std::time::Instant::now();
        if !captured {
            let _ = self.graphics.renderer.present(
                Some(background),
                self.graphics.format,
                &view,
                &self.viewport,
            );
        }
        // The frame time is the work of a frame: build the interface, draw it,
        // and submit the commands. It stops before the surface is presented,
        // because that call waits for the compositor and measures the screen's
        // rate rather than this program's cost.
        #[cfg(feature = "measure")]
        let build_ms = drawn_ms + millis(submit_start.elapsed());

        frame.present();

        // A captured frame draws twice and blocks on a readback, so it says
        // nothing about the cost of a frame and stays out of the numbers.
        #[cfg(feature = "measure")]
        if let Some(stats) = &mut self.stats {
            stats.frame(build_ms, millis(loop_start.elapsed()), !captured);
        }

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
            self.graphics.window.request_redraw();
            return;
        };

        let at = start.elapsed().as_secs_f64();
        let screen_next = match self.scheduled {
            Some(scheduled) => Some(scheduled),
            None => self.screen.next_frame(at),
        };
        self.scheduled = None;

        // The wake is the earliest second anything is due: the screen's own
        // change, the next script key, the deadline, or the next capture. The
        // harness's own seconds come from forward-only cursors, so they are
        // asked again on every pass, and only the screen's answer is held.
        // The floor holds every answer at least [`STEP`] after the last
        // frame, which is the frame-rate cap.
        let next = [screen_next, self.timeline.next_due(), self.next_capture()]
            .into_iter()
            .flatten()
            .min_by(f64::total_cmp)
            .map(|next| next.max(self.drawn + STEP));

        match timeline::wake(self.resized, at, next) {
            Wake::Now => {
                event_loop.set_control_flow(ControlFlow::Poll);
                self.graphics.window.request_redraw();
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
    #[cfg(feature = "measure")]
    fn capture(&mut self, at: f64, background: Color) -> bool {
        let Some(path) = self.captures.as_mut().and_then(|captures| captures.due(at)) else {
            return false;
        };

        let pixels = self
            .graphics
            .renderer
            .screenshot(&self.viewport, background);
        let size = self.viewport.physical_size();
        capture::write_png(&path, size.width, size.height, &pixels);
        eprintln!("captured {} at {at:.3}s", path.display());
        true
    }

    /// A build without the `measure` feature captures no frame.
    #[cfg(not(feature = "measure"))]
    fn capture(&mut self, _at: f64, _background: Color) -> bool {
        false
    }

    /// The second of the next capture, folded into the wake time.
    #[cfg(feature = "measure")]
    fn next_capture(&self) -> Option<f64> {
        self.captures.as_ref().and_then(Captures::next_due)
    }

    #[cfg(not(feature = "measure"))]
    fn next_capture(&self) -> Option<f64> {
        None
    }

    /// Whether the run has taken every capture it asked for, which ends it.
    #[cfg(feature = "measure")]
    fn captured_everything(&self) -> bool {
        self.captures.as_ref().is_some_and(Captures::taken)
    }

    #[cfg(not(feature = "measure"))]
    fn captured_everything(&self) -> bool {
        false
    }

    /// Map a fresh Wayland surface, and report whether one went up.
    ///
    /// Weston's kiosk-shell reveals a lower surface only along a code path
    /// gated on a seat, and `liken`'s compositor has none, so a client that a
    /// film covered stays hidden until it maps a new surface. A newly mapped
    /// toplevel is revealed along a seat-independent path.
    ///
    /// The new window is created before the old one is dropped, so the client
    /// is never without a surface and the screen never shows the compositor's
    /// background. The old surface is destroyed here too: the assignments drop
    /// it and then the last reference to its window, in that order, because a
    /// surface holds the window it was created from.
    ///
    /// A compositor that gives no second window leaves the first one drawing.
    pub(crate) fn represent(&mut self, event_loop: &ActiveEventLoop) -> bool {
        let size = self.viewport.physical_size();
        let Some(window) = graphics::window(event_loop, (size.width, size.height), &self.app_id)
        else {
            return false;
        };

        let surface = match self.graphics.instance.create_surface(Arc::clone(&window)) {
            Ok(surface) => surface,
            Err(error) => {
                eprintln!("idle-screen: no surface on the new window: {error}");
                return false;
            }
        };

        configure(
            &surface,
            &self.graphics.device,
            self.graphics.format,
            size.width.max(1),
            size.height.max(1),
        );
        self.graphics.surface = surface;
        self.graphics.window = window;
        eprintln!("idle-screen: a new surface is up");
        true
    }

    /// Write the statistics file once, whichever way the run ends.
    pub(crate) fn finish(&mut self) {
        if self.finished {
            return;
        }
        self.finished = true;
        #[cfg(feature = "measure")]
        if let Some(stats) = &self.stats {
            stats.write();
        }
    }
}
