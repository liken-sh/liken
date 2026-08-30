// One pass of the frame loop: the script's keys, the draw, the capture, and
// the numbers.

use iced_wgpu::graphics::Viewport;
use iced_wgpu::wgpu;
use iced_winit::core::time::Instant;
use iced_winit::core::{Event, Size, Theme, mouse, renderer, window};
use iced_winit::runtime::user_interface::UserInterface;
use iced_winit::winit::event_loop::ActiveEventLoop;

use super::graphics::{configure, write_png};
use super::stats::millis;
use super::{QUIT, Ready, Screen};

impl<S: Screen> Ready<S> {
    /// Hand one key to the screen. The answer is true when the key ends the run.
    pub(crate) fn press(&mut self, name: &str) -> bool {
        if name == QUIT {
            return true;
        }
        self.screen.key(name);
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
        let at = self.start.elapsed().as_secs_f64();

        let due = self.timeline.due(at);
        for key in &due.keys {
            self.screen.key(key);
        }
        if due.quit {
            self.stop(event_loop);
            return;
        }

        self.screen.tick(at);
        self.stats.sample_rss(at);

        if self.resized {
            let size = self.window.inner_size();
            let (width, height) = (size.width.max(1), size.height.max(1));
            self.viewport = Viewport::with_physical_size(
                Size::new(width, height),
                self.window.scale_factor() as f32,
            );
            configure(&self.surface, &self.device, self.format, width, height);
            self.stats.resized((width, height));
            self.resized = false;
        }

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

        let capture = self.timeline.due_capture(at);
        let submit_start;
        if let Some(path) = &capture {
            if let Some(dir) = path.parent() {
                let _ = std::fs::create_dir_all(dir);
            }
            // Renderer::screenshot renders the frame that was just drawn into
            // an offscreen texture and reads it back as RGBA. It is iced's own
            // path off the GPU, and it draws the same layers the surface would
            // get. The surface itself is left alone on a capture frame, because
            // one drawn frame must not be submitted twice.
            let pixels = self.renderer.screenshot(&self.viewport, background);
            let size = self.viewport.physical_size();
            write_png(path, size.width, size.height, &pixels);
            eprintln!("captured {} at {at:.3}s", path.display());
            submit_start = std::time::Instant::now();
        } else {
            submit_start = std::time::Instant::now();
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
        let loop_ms = millis(loop_start.elapsed());
        // A captured frame draws twice and blocks on a readback, so it says
        // nothing about the cost of a frame and stays out of the numbers.
        self.stats.frame(at, build_ms, loop_ms, capture.is_none());

        if self.timeline.ended(at) {
            self.stop(event_loop);
        }
    }

    /// Write the statistics file once, whichever way the run ends.
    pub(crate) fn finish(&mut self) {
        if self.finished {
            return;
        }
        self.finished = true;
        if let Some(path) = &self.stats_path {
            self.stats.write(path);
        }
    }
}
