# A player takes no outside control

A player is driven only by a `Remote`. The `Remote`'s standing pod
publishes the controller's raw evdev codes to its events topic, and the
`bridge` sidecar in the playback pod turns each code into a player
command through the compiled keymap. Nothing outside that path can
command a player. An automation, a web front end, or a Home Assistant
scene cannot tell a player to pause, to seek thirty seconds, or to move
to the next chapter, because the only control surface on the bus is one
controller's private evdev vocabulary.

Raw evdev is the wrong interface for an outside caller. The events topic
carries `{"type":1,"code":304,"value":1}`, and a caller that wanted a
pause would have to know the controller model's codes and the player's
keymap to send the right number. The action a person means, `pause` or
`seek`, never rides the bus in a form a program can address. A caller
that is not a physical controller has no clean way in.

The coupling that makes the events topic raw is correct and stays. One
`Remote` can drive two players that map it differently, so the mapping
belongs per player, in the `bridge`, not on the shared topic. The same
controller can hold one profile for a film player and another for a
browser player, and the raw topic is what lets one `Remote` feed both.
The gap is not that coupling. The gap is that there is no media-terms
control surface beside the raw input plane.

This document names the problem and not a solution. The question is
whether a player exposes a control interface over the bus, and in what
terms. One direction: the `bridge` already turns an action into a player
command, so it could also subscribe to a control topic that carries the
action itself, a `pause` or a `seek 30`, and apply it the same way. A
second direction: a `Remote`'s subscriber could hairpin, translate its
keymap, and re-publish the action on a control topic, so the media-terms
stream exists beside the raw one. Neither is decided here.

This is a cousin of the focus question plan 03 set aside. Once more than
one thing can command a player, two `Remote`s or a `Remote` and an
outside caller, the player needs a rule for who holds control. The focus
topic is named for that rule and stays unused until then. Outside control
and multiple remotes reach the same arbitration, so the two problems
likely share an answer.
