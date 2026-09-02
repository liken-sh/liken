// The winit application handler: the window the compositor gives, the events
// it sends, and the pass the loop takes between them. The frame itself is in
// `frame.rs`, and this file is what calls it.

use iced_wgpu::graphics::Viewport;
use iced_winit::core::Size;
use iced_winit::runtime::user_interface;
use iced_winit::winit;
use iced_winit::{Clipboard, conversion};

use winit::event::WindowEvent;
use winit::event_loop::ControlFlow;
use winit::keyboard::ModifiersState;
use winit::window::WindowId;

use super::capture::Captures;
use super::stats::Stats;
use super::timeline::Timeline;
use super::watchdog::Watchdog;
use super::{Options, Ready, Screen, graphics, key_name};

/// The run and the one thing that outlives its window: the watchdog,
/// which runs before the first window and after the last one.
pub(super) struct App<S: Screen> {
    pub(super) watchdog: Watchdog,
    pub(super) state: State<S>,
}

pub(super) enum State<S: Screen> {
    Loading {
        screen: Option<S>,
        options: Box<Options>,
        launched: std::time::Instant,
    },
    Ready(Box<Ready<S>>),
    /// The run is over and the graphics are already gone.
    Done,
}

impl<S: Screen> winit::application::ApplicationHandler for App<S> {
    fn resumed(&mut self, event_loop: &winit::event_loop::ActiveEventLoop) {
        let App { watchdog, state } = self;
        let State::Loading {
            screen,
            options,
            launched,
        } = state
        else {
            return;
        };
        let launched = *launched;

        // The window comes first, so a compositor that gives none
        // leaves the screen and the flags where they are and the watchdog
        // running.
        let Some(graphics) = graphics::open(event_loop, options.size, &options.app_id) else {
            return;
        };
        watchdog.present();

        let Options {
            script,
            capture_dir,
            capture_at,
            stats: stats_path,
            quit_after,
            app_id,
            // The binary reads the catalog flags before the run, so
            // the harness carries them and uses none of them.
            ..
        } = *std::mem::take(options);

        let viewport = Viewport::with_physical_size(
            Size::new(graphics.size.0, graphics.size.1),
            graphics.window.scale_factor() as f32,
        );
        let stats = Stats::new(
            graphics.backend.clone(),
            graphics.adapter.clone(),
            graphics.size,
        );

        let screen = screen.take().expect("one window per run");
        *state = State::Ready(Box::new(Ready {
            screen,
            timeline: Timeline::new(script, quit_after),
            stats_path,
            window: graphics.window,
            instance: graphics.instance,
            device: graphics.device,
            surface: graphics.surface,
            format: graphics.format,
            renderer: graphics.renderer,
            viewport,
            cache: user_interface::Cache::new(),
            clipboard: Clipboard::unconnected(),
            modifiers: ModifiersState::default(),
            events: Vec::new(),
            resized: false,
            app_id,
            surface_pending: false,
            launched,
            scheduled: None,
            start: None,
            drawn: 0.0,
            stale: false,
            captures: Captures::requested(capture_dir, capture_at),
            stats,
            finished: false,
        }));
    }

    fn window_event(
        &mut self,
        event_loop: &winit::event_loop::ActiveEventLoop,
        id: WindowId,
        event: WindowEvent,
    ) {
        let App { watchdog, state } = self;
        let State::Ready(ready) = state else {
            return;
        };

        match &event {
            WindowEvent::RedrawRequested => {
                ready.frame(event_loop);
                return;
            }
            // The window went away, which is what a compositor restart
            // under a running pod leaves behind. The grace starts again, and
            // nothing in this process opens the connection a second time.
            other if lost_its_window(other, id, ready.window.id()) => {
                watchdog.missing(std::time::Instant::now());
                return;
            }
            WindowEvent::Resized(_) => ready.resized = true,
            WindowEvent::CloseRequested => {
                ready.stop(event_loop);
                return;
            }
            WindowEvent::ModifiersChanged(new) => ready.modifiers = new.state(),
            WindowEvent::KeyboardInput { event, .. } => {
                if event.state.is_pressed()
                    && let Some(name) = key_name(&event.logical_key)
                    && ready.press(&name)
                {
                    ready.stop(event_loop);
                    return;
                }
            }
            _ => {}
        }

        if let Some(event) =
            conversion::window_event(event, ready.window.scale_factor() as f32, ready.modifiers)
        {
            ready.events.push(event);
        }
    }

    fn about_to_wait(&mut self, event_loop: &winit::event_loop::ActiveEventLoop) {
        // The grace is checked here rather than in the frame, because a
        // client with no window draws no frame.
        self.watchdog.expire_if_late(std::time::Instant::now());

        // A client waiting for a window has nothing to draw and a grace
        // to check, so the loop takes every pass it can until one is up. Winit
        // waits for an event otherwise, and a compositor that gives no window
        // sends none.
        if self.watchdog.counting() {
            event_loop.set_control_flow(ControlFlow::Poll);
            return;
        }

        let State::Ready(ready) = &mut self.state else {
            return;
        };

        // The deadline is checked here as well as in the frame, so a run ends
        // even if the compositor stops asking for frames. Before the first
        // frame there is no timeline yet, so there is no deadline.
        if let Some(start) = ready.start
            && ready.timeline.past_deadline(start.elapsed().as_secs_f64())
        {
            ready.stop(event_loop);
            return;
        }

        // The sources are pumped here, on every wake of the loop, because a
        // covered client draws no frame: the compositor sends a hidden
        // surface no frame callbacks.
        // `present` is the one message that lets a covered browser map the
        // surface that reveals it, so the bus is read on a path the
        // compositor cannot starve.
        if let Some(start) = ready.start {
            let at = start.elapsed().as_secs_f64();
            if ready.screen.pump(at) {
                // What arrived changed the screen, so the frame on the glass
                // is stale whatever the animations schedule, and the second
                // scheduled before it no longer holds.
                ready.scheduled = None;
                ready.stale = true;
            }
            if ready.screen.surface_due() {
                ready.surface_pending = true;
            }
            if ready.surface_pending && ready.represent(event_loop) {
                ready.surface_pending = false;
                ready.screen.surfaced(at);
                // A Wayland surface is not on screen until its first buffer
                // arrives, so the new window gets a draw whatever the
                // schedule says.
                ready.window.request_redraw();
            }
        }

        ready.pace(event_loop);
    }

    fn exiting(&mut self, _event_loop: &winit::event_loop::ActiveEventLoop) {
        if let State::Ready(ready) = &mut self.state {
            ready.finish();
        }
        // Wgpu builds an instance for every backend it can reach, and the one
        // for GL holds an EGL display on the compositor's connection. Its
        // destructor speaks Wayland, so it has to run while the connection is
        // open. winit closes the connection after this call and never before
        // it, so the graphics are dropped here rather than where the loop
        // returns.
        self.state = State::Done;
    }
}

/// Whether one window event says the window the run draws on now went
/// away. A `present` maps a new window before it drops the old one, so
/// a `Destroyed` arrives for a window the run has already replaced.
/// Without this guard the grace starts on that stale window, and the
/// browser exits 7 fifteen seconds after every `Play`, which puts a
/// person back at the libraries. The idle client's harness in
/// `media-operator` reads the same rule.
fn lost_its_window(event: &WindowEvent, destroyed: WindowId, drawing: WindowId) -> bool {
    matches!(event, WindowEvent::Destroyed) && destroyed == drawing
}

#[cfg(test)]
mod tests {
    use super::*;

    // Two identifiers of a run's own, because a test opens no window.
    fn ids() -> (WindowId, WindowId) {
        (WindowId::from(1), WindowId::from(2))
    }

    #[test]
    fn the_window_the_run_draws_on_going_away_is_a_loss() {
        let (drawing, _replaced) = ids();
        assert!(lost_its_window(&WindowEvent::Destroyed, drawing, drawing));
    }

    #[test]
    fn a_window_the_present_already_replaced_going_away_is_no_loss() {
        let (drawing, replaced) = ids();
        assert!(!lost_its_window(&WindowEvent::Destroyed, replaced, drawing));
    }

    #[test]
    fn an_event_that_is_not_a_destroyed_is_no_loss() {
        let (drawing, _replaced) = ids();
        assert!(!lost_its_window(
            &WindowEvent::CloseRequested,
            drawing,
            drawing
        ));
    }
}
