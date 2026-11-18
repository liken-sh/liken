// The frame loop of `Ready`: key presses, drawing one frame, and the
// choice of when the loop wakes next.

use iced_wgpu::graphics::Viewport;
use iced_wgpu::wgpu;
use iced_winit::core::time::Instant;
use iced_winit::core::{Color, Event, Size, Theme, mouse, renderer, window};
use iced_winit::runtime::user_interface::UserInterface;
use iced_winit::winit::event_loop::{ActiveEventLoop, ControlFlow};

#[cfg(feature = "measure")]
use super::capture::{self, Captures};
use super::graphics::configure;
#[cfg(feature = "measure")]
use super::stats::millis;
use super::timeline::{self, Wake};
use super::{QUIT, Ready, Screen};

/// The shortest time between two frames, one sixtieth of a second. The
/// surface presents without vsync, so this is the only frame-rate limit. A
/// screen that asks for a frame on every pass is drawn 60 times a second
/// instead of as fast as the loop can run.
pub const STEP: f64 = 1.0 / 60.0;

impl<S: Screen> Ready<S> {
    /// Passes a key from the keyboard to the screen, and returns true when
    /// the key ends the run. A key can arrive between frames, so the
    /// screen's clock moves to the current time first, so an animation
    /// that the key starts begins at that time.
    pub(crate) fn keyboard(&mut self, name: &str) -> bool {
        if let Some(start) = self.start {
            self.screen.tick(start.elapsed().as_secs_f64());
        }
        self.press(name)
    }

    /// Passes a key from the keyboard or the script to the screen, and
    /// returns true when the key is [`QUIT`].
    pub(crate) fn press(&mut self, name: &str) -> bool {
        if name == QUIT {
            return true;
        }
        // The key can change the view and the screen's schedule.
        self.next_frame_at = None;
        self.needs_redraw = true;
        self.screen.key(name);
        false
    }

    /// Writes the statistics and exits the event loop as a requested exit.
    pub(crate) fn stop(&mut self, event_loop: &ActiveEventLoop) {
        self.stopped = true;
        self.finish();
        event_loop.exit();
    }

    /// Builds, draws, captures if due, and presents one frame.
    pub(crate) fn frame(&mut self, event_loop: &ActiveEventLoop) {
        // This frame draws every change so far, and it is the frame that the
        // saved second was waiting for.
        self.next_frame_at = None;
        self.needs_redraw = false;
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

        self.last_frame_at = at;

        // The clock moves before the scripted keys, so each key is handled at
        // this frame's second. A screen at rest can draw only once a minute,
        // so at the previous frame's second a key's animation could start a
        // minute in the past and already be over.
        self.screen.tick(at);

        for key in self.timeline.due(at) {
            if self.press(&key) {
                self.stop(event_loop);
                return;
            }
        }

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
                eprintln!("the window surface is out of memory, stopping the event loop");
                self.stop(event_loop);
                return;
            }
            Err(_) => {
                self.resized = true;
                return;
            }
        };

        // The timer starts after the surface texture is acquired, so the
        // frame time excludes the wait for the display.
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
        // The frame time is paused here. A capture writes a file and blocks
        // on a GPU readback, so its time stays out of the frame time.
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
        // The frame time covers building the interface, drawing it, and
        // submitting the commands. It stops before `frame.present()`,
        // because that call waits for the compositor and would measure the
        // display's rate instead of this program's work.
        #[cfg(feature = "measure")]
        let build_ms = drawn_ms + millis(submit_start.elapsed());

        frame.present();

        // A captured frame waits on a GPU readback and writes a file, so its
        // timings are left out of the statistics.
        #[cfg(feature = "measure")]
        if let Some(stats) = &mut self.stats {
            stats.frame(build_ms, millis(loop_start.elapsed()), !captured);
        }

        if self.timeline.past_deadline(at) || self.captured_everything() {
            self.stop(event_loop);
        }
    }

    /// Chooses when the loop wakes next, and asks winit for a frame when one
    /// is due now. The loop sleeps until the earliest second at which
    /// anything is due, so a screen at rest is drawn once per change and not
    /// once per display refresh.
    pub(crate) fn pace(&mut self, event_loop: &ActiveEventLoop) {
        // The clock starts at the first frame, so request that frame now.
        let Some(start) = self.start else {
            event_loop.set_control_flow(ControlFlow::Poll);
            self.graphics.window.request_redraw();
            return;
        };

        let at = start.elapsed().as_secs_f64();
        let screen_next = screen_due(&self.screen, self.next_frame_at, self.last_frame_at);
        self.next_frame_at = None;

        // The next wake is the earliest of: now when the last frame is out
        // of date, the screen's next change, the next scripted key, the
        // deadline, and the next capture. Only the screen's second is saved
        // between passes. The result is moved to at least `STEP` after the last frame, so a burst
        // of changes draws at most 60 frames a second.
        let redraw_now = self.needs_redraw.then_some(at);
        let next = [
            redraw_now,
            screen_next,
            self.timeline.next_due(),
            self.next_capture(),
        ]
        .into_iter()
        .flatten()
        .min_by(f64::total_cmp)
        .map(|next| next.max(self.last_frame_at + STEP));

        match timeline::wake(self.resized, at, next) {
            Wake::Now => {
                event_loop.set_control_flow(ControlFlow::Poll);
                self.graphics.window.request_redraw();
            }
            // The loop wakes at most `timeline::BACKSTOP` seconds from now to
            // call `Screen::pump`, and that wake draws nothing unless a change
            // made the frame out of date. The screen's second is saved for
            // the wakes until the clock reaches it.
            Wake::At(next) => {
                self.next_frame_at = screen_next;
                event_loop.set_control_flow(ControlFlow::WaitUntil(
                    start + std::time::Duration::from_secs_f64(timeline::sleep_until(at, next)),
                ));
            }
            Wake::Never => event_loop.set_control_flow(ControlFlow::Wait),
        }
    }

    /// Writes this frame to a PNG file if a capture is due at second `at`.
    /// Returns whether a file was written.
    ///
    /// `Renderer::screenshot` draws the frame into an offscreen texture and
    /// reads it back as RGBA. On a capture frame the caller does not also
    /// call `Renderer::present`, so the frame is not submitted twice.
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

    #[cfg(not(feature = "measure"))]
    fn capture(&mut self, _at: f64, _background: Color) -> bool {
        false
    }

    #[cfg(feature = "measure")]
    fn next_capture(&self) -> Option<f64> {
        self.captures.as_ref().and_then(Captures::next_due)
    }

    #[cfg(not(feature = "measure"))]
    fn next_capture(&self) -> Option<f64> {
        None
    }

    #[cfg(feature = "measure")]
    fn captured_everything(&self) -> bool {
        self.captures.as_ref().is_some_and(Captures::taken)
    }

    #[cfg(not(feature = "measure"))]
    fn captured_everything(&self) -> bool {
        false
    }

    /// Writes the statistics file once, however the run ends.
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

/// Returns `saved`, the second the screen already returned, or else asks
/// the screen with `last_frame_at`.
///
/// The screen is asked about the last frame drawn and not the current
/// clock. Building a frame takes time, so an animation can end between the
/// frame's second and the moment the loop asks. Asked at the current time,
/// the screen would report nothing left to draw, and the display would keep
/// the unfinished animation until the screen's next scheduled change, which
/// can be a minute away. Asked at `last_frame_at`, the screen reports the
/// animation still running, so the loop draws one more frame that shows its
/// end.
pub(crate) fn screen_due<S: Screen>(
    screen: &S,
    saved: Option<f64>,
    last_frame_at: f64,
) -> Option<f64> {
    match saved {
        Some(saved) => Some(saved),
        None => screen.next_frame(last_frame_at),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use iced_wgpu::Renderer;
    use iced_winit::core::{Element, Theme};

    // A screen with one animation that runs until `ends`, like a fade on the
    // idle screen.
    struct Moving {
        ends: f64,
    }

    impl Screen for Moving {
        type Message = ();

        fn key(&mut self, _name: &str) {}

        fn tick(&mut self, _at: f64) {}

        fn view(&self) -> Element<'_, (), Theme, Renderer> {
            iced_widget::Space::new().into()
        }

        fn next_frame(&self, at: f64) -> Option<f64> {
            (at < self.ends).then_some(at)
        }
    }

    #[test]
    fn a_frame_drawn_before_an_animation_ends_is_followed_by_another() {
        let moving = Moving { ends: 1.01 };
        let cases = [(1.0, Some(1.0)), (1.01, None), (1.03, None)];
        for (drawn, due) in cases {
            assert_eq!(screen_due(&moving, None, drawn), due, "{drawn}");
        }
    }

    #[test]
    fn a_saved_second_is_kept() {
        let moving = Moving { ends: 1.01 };
        assert_eq!(screen_due(&moving, Some(4.0), 1.0), Some(4.0));
    }
}
