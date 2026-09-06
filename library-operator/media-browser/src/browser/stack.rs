// The navigation stack and the moves across it. The home page is the
// bottom and never leaves, so the screen is never empty. Open, back,
// home, and search push and pop the screens over it, and each of them
// takes focus off the strip, because the screen under it changed. Only
// the browser holds the stack, so a screen names the screen it opens
// and never pushes one itself.

use super::Browser;
use crate::catalog::Source;
use crate::posters::Posters;
use crate::screens::{self, Step, loading};

impl<S: Source, P: Posters> Browser<S, P> {
    pub(super) fn top(&self) -> &screens::Screen {
        self.stack.last().unwrap_or(&self.home)
    }

    // Read the screen on top again. The home page goes through the
    // reader, so the read that uncovers it never holds the frame thread.
    pub(super) fn reread_top(&mut self) {
        let Some(top) = self.stack.last_mut() else {
            self.refresh_home();
            return;
        };
        top.reread(&mut self.source);
        top.volume(&*self.posters.borrow());
    }

    // Do what the screen that took the press asked for. Only the browser
    // holds the stack, so a screen names the screen it opens and never
    // pushes one itself.
    pub(super) fn take(&mut self, step: Step) {
        match step {
            Step::Stay | Step::Still => {}
            Step::Open(screen) => self.opened(screen),
            Step::Replace(screen) => {
                self.stack.pop();
                self.opened(screen);
            }
            Step::Play { library, selection } => {
                // The press enters the state in the frame it lands in.
                // Nothing downstream is awaited: the request crosses the
                // bus, the operator creates the `Play`, and the pod
                // starts, and none of the three reaches this browser. A
                // choice with no film behind it enters nothing, because
                // no film will ever cover the page.
                if self.request_play(&library, &selection) {
                    self.loading = Some(loading::Loading::entered(self.clock));
                }
            }
        }
    }

    // Push a screen and read the files it draws off the volume,
    // which the screen itself cannot reach: only the browser holds the
    // store that resolves a library's root.
    pub(super) fn opened(&mut self, mut screen: screens::Screen) {
        screen.volume(&*self.posters.borrow());
        self.stack.push(screen);
        self.on_strip = false;
    }

    // Ask the store for the backdrop of the page under the focused item,
    // at the size the page draws it. The answer is dropped. The ask is
    // the point: the decode lands in the cache before the page opens.
    pub(super) fn prefetch(&mut self) {
        let top = self.stack.last().unwrap_or(&self.home);
        let Some((library, art)) = top.resting(&mut self.source) else {
            return;
        };
        let (width, height) = self.page;
        let _ = self.posters.get_mut().poster(&library, &art, width, height);
    }

    // Back pops one descent and re-reads the screen it uncovers,
    // because a change that landed while that screen was covered was
    // folded into the screen that was shown at the time and not into
    // this one. The home page is the one screen that is read only where
    // it is behind, so back to it draws the page a person left at once.
    //
    // At the home page there is nowhere to climb, so a browser on a bus
    // asks for the shade. Only the browser knows whether back has
    // anywhere to go, which is why the crate never sleeps on back
    // itself.
    pub(super) fn back(&mut self) {
        self.on_strip = false;
        if self.stack.pop().is_some() {
            self.reread_top();
            return;
        }
        if let Some(bus) = &self.bus {
            bus.sleep();
        }
    }

    // Push the search wall over the screen the person is on, so back
    // returns to it. The text seeds the field. The grid shows when the
    // way in was a button and not a keyboard.
    pub(super) fn search(&mut self, text: &str, grid: bool) {
        let screen = screens::wall::searched(text, grid, &mut self.source);
        self.opened(screen);
    }

    // Home drops every screen over the home page in one press and then
    // reads the home page the way back landing on it does: only where
    // the page is behind. On the home page there is nothing to drop, and
    // the press puts focus back on the first row.
    pub(super) fn home(&mut self) {
        self.on_strip = false;
        if self.stack.is_empty() {
            if let screens::Screen::Home(home) = &mut self.home {
                home.top();
            }
            return;
        }
        self.stack.clear();
        self.refresh_home();
    }
}
