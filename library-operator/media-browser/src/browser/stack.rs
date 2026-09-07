// The navigation stack and the moves across it. The home page is the
// bottom and never leaves, so the screen is never empty. Open, back,
// home, and search push and pop the screens over it, and each of them
// takes focus off the strip, because the screen under it changed. Only
// the browser holds the stack, so a screen names the screen it opens
// and never pushes one itself.

use super::Browser;
use crate::art::Art;
use crate::catalog::Source;
use crate::screens::{self, Step, loading};

impl<S: Source, A: Art> Browser<S, A> {
    pub(super) fn top(&self) -> &screens::Screen {
        self.stack.last().unwrap_or(&self.home)
    }

    // Read the screen on top again. The home page goes through the
    // reader, so the read that uncovers it never holds the frame thread.
    pub(super) fn reread_top(&mut self) {
        let people = self.audience.current(self.clock).to_vec();
        let Some(top) = self.stack.last_mut() else {
            self.refresh_home();
            return;
        };
        top.reread(&mut self.source);
        top.read_progress(&mut self.source, &people);
        top.volume(&*self.store.borrow());
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
            Step::Play {
                library,
                selection,
                start,
            } => {
                // The press enters the state in the frame it lands in.
                // Nothing downstream is awaited: the request crosses the
                // bus, the operator creates the `Play`, and the pod
                // starts, and none of the three reaches this browser. A
                // choice with no film behind it enters nothing, because
                // no film will ever cover the page.
                if self.request_play(&library, &selection, start) {
                    self.loading = Some(loading::Loading::entered(self.clock));
                }
            }
        }
    }

    // Push a screen, read how far the audience reached in what it draws,
    // and read the files it draws off the volume. A screen reaches neither
    // for itself: only the browser holds the audience and the store that
    // resolves a library's root.
    pub(super) fn opened(&mut self, mut screen: screens::Screen) {
        let people = self.audience.current(self.clock).to_vec();
        screen.read_progress(&mut self.source, &people);
        screen.volume(&*self.store.borrow());
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
        let _ = self.store.get_mut().covered(&library, &art, width, height);
    }

    // Back pops one descent and re-reads the screen it uncovers,
    // because a change that landed while that screen was covered was
    // folded into the screen that was shown at the time and not into
    // this one. The home page is the one screen that is read only where
    // it is behind, so back to it draws the page a person left at once.
    //
    // At the home page there is nowhere to climb, so back is home: focus
    // returns to the banner. The shade is the power key's, because a
    // person backing out of a deep page would otherwise darken the
    // screen one press past where they meant to stop.
    pub(super) fn back(&mut self) {
        self.on_strip = false;
        if self.stack.pop().is_some() {
            self.reread_top();
            return;
        }
        self.home();
    }

    // Ask the crate for the shade. Only the crate decides, because it
    // alone knows whether the unit plays something, and the moment comes
    // back to the browser the way a quiet window's does. A browser with no
    // bus has no shade to ask for.
    pub(super) fn rest(&mut self) {
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
