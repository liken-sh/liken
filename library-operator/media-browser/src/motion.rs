// The motion of focus: the outline that travels from one slot to the next,
// the rows and walls that scroll under it, and the breath of the outline
// while focus rests. Every value here is a function of the clock and of a
// start, a length, and two ends that a press sets, so a frame draws the
// same picture at the same second however many frames came before it. A
// press that arrives during a motion starts the next one from the value
// drawn at that second, so the motion never jumps.

mod breath;
mod glide;
mod outline;
mod pace;

pub use breath::Breath;
pub use glide::Glide;
pub use outline::Outline;
pub use pace::Pace;
