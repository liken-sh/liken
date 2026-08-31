// The binary reads the flags and the wiring. A bad flag stops the run before a
// window opens.

use idle_screen::client::Client;
use idle_screen::harness::options::help;
use idle_screen::harness::{self, Invocation, Options};
use idle_screen::wiring::Wiring;

fn main() {
    // The environment is read once here. The client takes the broker, the
    // topics, and the seeds, and the harness takes the two window facts: the
    // app-id routes the surface to a screen, and the grace arms the watchdog.
    let wiring = Wiring::from_environment();

    match Options::parse(std::env::args().skip(1)) {
        Ok(Invocation::Help) => print!("{}", help()),
        Ok(Invocation::Run(mut options)) => {
            options.app_id = wiring.app_id.clone();
            options.window_grace = wiring.window_grace;
            if let Err(error) = harness::run(Client::open(wiring), options) {
                eprintln!("idle-screen: {error}");
                std::process::exit(1);
            }
        }
        Err(error) => {
            eprintln!("idle-screen: {error}");
            std::process::exit(2);
        }
    }
}
