# 73, Menu control, deck control, and the menu language over CEC

Not built. This plan states three optional HDMI-CEC features that the
`liken` playback device does not implement, and the questions to answer
before any of them is built. It makes no design decision. Each section
gives the problem, what the spec requires, what `liken` does now, and
the options with their trade-offs.

The plan was written on 2026-09-29, and the text below is current to
2026-10-04. Two later changes moved its ground. Commit 3c425c3f, made
under equipment-operator
[plan 09](../equipment-operator/plans/completed/09-cec.md), fixed
gap 1 and gap 5 of the conformance review. A wake's guard now yields
to a person's routing change, and the deterministic remote functions
set the state they name. The same commit gave the playback sidecar the
`play`, `hold`, and `stop` actions that Deck Control needs.
[Plan 77](completed/77-the-media-bus-stops-at-media-operator.md) took
equipment-operator off the media bus. The node workload holds no
broker connection, so any route from it to a `Play` or a `Player` goes
through `Television` or `Receiver` status, which media-operator
watches.

The maintainer's standard for CEC is a fully conforming device that is
a good citizen of the bus and claims the input only when a person asks.
Each feature below is optional or narrow in the spec, so none is a
defect that a person has been measured to hit. Where this plan states a
symptom, it marks the symptom as a risk unless a drill measured it.

The spec sections are the sections of the HDMI 1.3a CEC supplement,
cited as "CEC 13.x" and "CEC Table x".

## The three features

1. Menu Control (CEC 13.12): `<Menu Request>` and `<Menu Status>`.
2. Deck Control (CEC 13.7): `<Play>`, `<Deck Control>`, and
   `<Give Deck Status>` with `<Deck Status>`.
3. The menu language (CEC 13.6): `<Set Menu Language>` from the TV, and
   `<Get Menu Language>` to the TV.

The three share one owner on the bus, the follower in
`equipment-operator/cec/follower.go`, and they share one problem: the
follower has no path to the components that own the answer. The
equipment operator knows the bus. The media operator knows whether a
`Play` stands and whether a menu is on the screen. The screens know the
language they draw in. Every design question below is a question about
that path.

## What `liken` does now

The follower answers a directed request it does not support with
`<Feature Abort>` "Unrecognized opcode" (`equipment-operator/cec/follower.go:102`).
It skips the answer for any opcode in its `answers` set, which lists
the opcodes that reply to a request (`follower.go:51-78`). That set
includes `OpSetMenuLanguage` (`follower.go:73`), so the follower drops
`<Set Menu Language>` and sends no reply. `OpDeckStatus` and
`OpMenuStatus` are in the set too, for the same reason.

The follower's tests fix the current answer for `<Menu Request>`: a
`<Feature Abort>` for a directed request, and no answer for a request
from address 15 or a broadcast (`equipment-operator/cec/follower_test.go:17-24`).

`equipment-operator/cec/message.go` declares `OpMenuRequest` (line
101), `OpMenuStatus` (86), `OpGiveDeckStatus` (100), `OpDeckStatus`
(84), `OpSetMenuLanguage` (93), and `OpGetMenuLanguage` (102). It
declares no opcode for `<Play>` (0x41) or `<Deck Control>` (0x42), and
it has no builder for any of the messages this plan sends.

The node workload records one active-source address. `noteSource`
(`equipment-operator/cecnode_source.go:25`) sets it when the adapter
hears an `<Active Source>` (`cecnode_adapter.go:548-551`) and when the
adapter sends one itself (`announceSource`, `cecnode_source.go:73`).
`noteSource` also records the route, and an `<Active Source>` for
`0.0.0.0` is a route that a person chose. `claimInput`
(`cecnode_source.go:58`) is the one place that sends `<Active Source>`
for a person's request.

On the media side, the `Play` status holds a phase, and
`playActivity` folds the phase and the paused flag into `Starting`,
`Playing`, `Paused`, `Finished`, or `Failed` (`media-operator/api.go:626-643`).
The `Player` status reads `Idle` between films
(`media-operator/playerstatus.go`). The playback sidecar maps key names
to commands in `playbackKeys` (`media-operator/keybindings.go`). Its
`actionPause` toggles, and `KEY_PLAYPAUSE`, `KEY_PLAY`, and
`KEY_PAUSE` bind to it (`keybindings.go:58-60`). `KEY_PLAYCD`,
`KEY_PAUSECD`, and `KEY_STOPCD`, the names `rc-cec` gives a TV
remote's Play Function, Pause, and Stop Function, bind to `actionPlay`,
`actionHold`, and `actionStop` (`keybindings.go:62-64`). Play and hold
set the state they name, so a second press changes nothing
(`media-operator/input.go`). Stop ends the run and asks the screen
under it nothing (`media-operator/stop.go`). Home and power also end a
run, and each sends an ask to the screen under it
(`media-operator/home.go`, `media-operator/power.go`).

`MediaPreferences` holds `audioLanguages`, `subtitleLanguages`,
`subtitles`, and `timeZone`, with the idle screen policy
(`media-operator/api.go:681-692`). The first two select
tracks inside a film. They are not the language of a menu. I found no
string table or translation code in `library-operator/media-browser`,
`media-operator/idle`, or `media-screen`, so the screens draw one
language today.

The first drill's TV sent `<User Control Pressed>` for the arrows, OK,
and back with the adapter as the active source, and the adapter sent
no `<Menu Status>` (equipment-operator plan 09, "The TV remote reaches
the input device"). That drill is the only measurement of remote
behavior so far.

## Menu Control

### The problem

A person points the TV's remote at a TV whose input shows the Player's
browser, and presses the arrow keys and OK. Risk, not measured: a TV
that forwards navigation keys to a source only after that source
reports an active menu, or that stops forwarding after a `<Feature Abort>`
to its `<Menu Request>`, sends the keys nowhere. The person sees a
browser that does not move. The first drill's TV forwarded the keys
without any `<Menu Status>`, so this is a risk on other TVs, not a
failure that anyone has seen.

### What the spec requires

- CEC 13.12.2: "A device shall indicate that it is displaying a menu by
  sending a `<Menu Status>` ['Activated'] message to the TV." When the
  device leaves the menu, it sends "Deactivated".
- CEC 13.12.2: the TV may send `<Menu Request>` with "Activate",
  "Deactivate", or "Query", and "the menu device shall always respond
  with a `<Menu Status>` command when it receives a `<Menu Request>`".
- CEC 13.12.2: "A new active source device shall send a `<Menu Status>`
  ['Activated'] message to the TV if it is displaying a menu." The TV
  assumes a new active source is not in a menu unless the message
  follows the `<Active Source>`.
- CEC 13.12.2: "A source device shall only send `<Menu Status>`
  commands when it is the current active source." The TV ignores a
  `<Menu Status>` from any other device.
- CEC Table 18 defines both messages. CEC Table 24 lists
  `<Menu Status>` as the response to `<Menu Request>`.

The feature is optional. A device that implements it takes on every
"shall" above.

### What `liken` does now

`<Menu Request>` gets a `<Feature Abort>` (`follower.go:102`).
Nothing sends `<Menu Status>`.

### Questions to answer

1. When is the menu "Activated"? The Player draws a menu whenever the
   browser or the idle screen is on the screen and no `Play` stands.
   Three definitions are possible:
   - Activated while the `Player` status is `Idle` and the adapter is
     the active source. This matches what a person sees, but the
     equipment operator does not read the `Player` status today.
   - Activated whenever the adapter is the active source, whether or
     not a film plays. This needs no new input, but it reports a menu
     while a film fills the screen, and it asks the TV to keep
     forwarding keys that the film does not use as a menu. The
     playback sidecar does bind the arrows during a `Play`, so this
     may be acceptable.
   - Activated only after the person has pressed a key. This is the
     smallest claim, but CEC 13.12.2 expects the message after
     `<Active Source>`, so a TV that waits for it never forwards the
     first key.
2. Which component reads the state and which one sends the message? The
   sender has to be the node workload, because only it owns the bus.
   The options:
   - The node workload reads the `Player` status through the
     `Receiver` session that already names the `Player`
     (`TelevisionSession.Player`, `equipment-operator/television.go:56`).
     It stays event-driven, with no timer, if it watches the
     `Player`. It adds a second watch to a component that watches
     `Television` and `Receiver` today.
   - The media operator writes the menu state into the `Receiver`'s
     `status.session`, and the equipment operator's `Deployment` copies
     it into the `Television`'s session, the path the `Player`'s name
     already takes. This puts the fact next to its owner. It adds a
     field that both components must agree on. The node workload cannot
     subscribe to a topic, because plan 77 took it off the media bus.
   - The node workload derives the state from what it already has:
     `session.Awake` and the active-source address. It adds no
     input, and it cannot tell a menu from a film.
3. What sends "Deactivated"? A `Play` that starts is the obvious
   event. Losing the input (an `<Active Source>` from another device)
   also ends the state, and CEC 13.12.2 then forbids a
   `<Menu Status>` from the adapter. The design has to send
   "Deactivated" before the adapter gives up the input, or send
   nothing, and the spec text does not say which one a TV expects.
4. What does `<Menu Request>` "Activate" do? The spec says the device
   "may enter or exit the 'Device Menu Active' state" (CEC Table 18).
   Options: answer with the current state and change nothing, or
   claim the input first. Claiming the input on a TV's request
   conflicts with the standard to claim only when a person asks. A
   `<Menu Request>` "Activate" is the TV's own UI, and it may not
   count as the person asking the Player.
5. What does the answer say to a `<Menu Request>` that arrives while the
   adapter is not the active source? CEC 13.12.2 limits `<Menu
   Status>` to the active source. The options are a `<Feature Abort>`
   "Not in correct mode to respond", or a `<Menu Status>`
   "Deactivated". The spec says the device "shall always respond with
   a `<Menu Status>`", and it also says a non-source shall not send
   one, so the two rules conflict for this case. The `cec-follower`
   test tool in v4l-utils answers `<Menu Request>` with "Activated"
   whatever the active source is, so it does not settle the question.
   The maintainer decides which reading `liken` follows.

## Deck Control

### The problem

A person presses play, pause, or stop on a TV whose remote drives a
playback device through the deck messages, not through
`<User Control Pressed>`. Risk, not measured: the TV shows transport
controls for the Player, and they do nothing, because every deck
message gets a `<Feature Abort>`. The first drill's TV has no play or
pause key, so no drill has exercised the transport keys either way.

### What the spec requires

- CEC 13.7.2: `<Play>` and `<Deck Control>` "may be initiated after a
  user command. The Deck shall act upon the command that it receives
  within the messages `<Play>` and `<Deck Control>`. It is the
  equivalent of the user selecting the command local to the Deck."
- CEC 13.7.2: "If the deck cannot carry out the command (e.g. it has no
  media when trying to play) it should respond with a `<Feature Abort>`
  ['Not in correct mode to respond'] message."
- CEC 13.7.2: a device queries a deck with `<Give Deck Status>`, and
  "the deck should respond with a `<Deck Status>` message". The
  request operand "On" asks for the status on every later change, and
  "Off" cancels it (CEC Table 13).
- CEC 13.7.2: a deck in standby that receives `<Play>` "Play Forward"
  or `<Deck Control>` "Eject" "should power on and act on the message".
  Power on for any other operand is "up to the manufacturer".
- CEC Table 13 lists `<Play>` modes (Play Forward 0x24, Play Reverse
  0x20, Play Still 0x25, and the fast forward and fast reverse speeds),
  the `<Deck Control>` modes (Skip Forward 1, Skip Reverse 2, Stop 3,
  Eject 4), and the `<Deck Status>` values (Play 0x11, Still 0x14,
  Fast Forward 0x17, Fast Reverse 0x18, No Media 0x19, and others).
- CEC Table 24: a device that accepts `<Play>` and `<Deck Control>` can
  be asked for `<Deck Status>`, and `<Give Deck Status>` requires
  `<Deck Status>` in response.

The feature is optional. Once a device implements it, "shall act upon"
binds it.

### What `liken` does now

`<Play>`, `<Deck Control>`, and `<Give Deck Status>` get a
`<Feature Abort>` "Unrecognized opcode" (`follower.go:102`). The
opcodes `<Play>` and `<Deck Control>` are not declared in
`equipment-operator/cec/message.go`.

### Questions to answer

1. Where does each message map in `media-operator`'s commands? The
   commands the sidecar accepts are the actions in
   `media-operator/input.go:31-50` (`pause`, `play`, `hold`, `seek`,
   `chapter`, `subtitles`, `audio`, `info`, `cycle-focus`, and the
   navigation actions), with `stop`, `home`, and `power`. The
   candidates:

   | Deck message | Candidate command | Gap |
   |---|---|---|
   | `<Play>` "Play Forward" | `play` | None. `actionPlay` resumes and never pauses. |
   | `<Play>` "Play Still" | `hold` | None. `actionHold` pauses and never resumes. |
   | `<Play>` fast modes | `seek` with a step | The sidecar seeks by a fixed step. A speed has no equivalent, and a device "should select the closest match" (CEC Table 13 note). |
   | `<Deck Control>` "Skip Forward" and "Skip Reverse" | `chapter` +1 and -1 | Fits `actionChapter` (`keybindings.go:69-70`). |
   | `<Deck Control>` "Stop" | `stop` | None. `actionStop` ends the run and sends no ask to the screen under it (`stop.go`). |
   | `<Deck Control>` "Eject" | none | `liken` has no media tray. |

   Commit 3c425c3f built the split between a toggle and an explicit
   play or pause for the deterministic remote functions of
   `<User Control Pressed>`. Deck Control maps onto the same actions,
   so it needs no new sidecar action.
2. How does the message reach the `Play`? The equipment operator does
   not talk to a `Play`, and since plan 77 it holds no broker
   connection. So the route passes through `Television` or `Receiver`
   status, and media-operator relays it. The `Television`'s
   `status.screenAsk` already carries a screen ask this way: the node
   workload writes it, and media-operator publishes it on the
   `Player`'s power topic (`media-operator/screenask.go`). Options:
   - Reuse the path of `<User Control Pressed>`: the kernel turns the
     key into an input event, and `media-operator` reads the device.
     `<Play>` and `<Deck Control>` are not keys, so the kernel does
     not turn them into events, and the follower would have to
     synthesize one. That gives the sidecar no new interface, but it
     invents a key that no remote sends.
   - Write a deck ask in `Television` status beside `screenAsk`, and
     let media-operator publish it on the `Play`'s commands topic as
     `play`, `hold`, `stop`, or `chapter`. This reuses the shape of
     the screen ask relay, and adds one field and one relay. A status
     write and a watch add latency over a direct publish.
3. What does the deck report as `<Deck Status>`? The `Play` activity
   maps to Play (0x11, `Playing`), Still (0x14, `Paused`), and No
   Media (0x19, no `Play`). Nothing in the CEC set maps to `Starting`
   or `Failed`. The sender needs the `Play` status, so it faces the
   same reader question as Menu Control.
4. What answers `<Play>` when no `Play` stands? CEC 13.7.2 allows
   "Not in correct mode to respond", and that fits "no media" for a
   Player that has none to resume. The other option is to start the
   last `Play` again, which needs a stored last `Play` and conflicts
   with plan 72's decision that a power press does not start the last
   `Play` again. That decision applies to power, not to `<Play>`, so
   this plan leaves the question open.
5. Does a `<Play>` wake a sleeping room? CEC 13.7.2 says a deck in
   standby "should power on" for "Play Forward". The Player's room
   wakes only on a person's press (`Television` `session.wokeAt`). A
   TV that sends `<Play>` "Play Forward" to a sleeping Player has
   already turned itself on. Waking the room means claiming the input.
   The standard to claim only when a person asks conflicts with the
   optional "should", and the spec makes the choice a manufacturer's.
6. Does `<Give Deck Status>` "On" need a standing subscription? The
   deck sends `<Deck Status>` on every later state change until the TV
   sends "Off". That is a push from the `Play` status to the bus, and
   it fits the event-driven rule. It also adds per-TV state in the node
   workload. Answering "Once" only, and sending the status on a state
   change only when a TV asked for "On", is the smaller claim.
   Ignoring the "On" operand is not conforming.

## The menu language

### The problem

A person sets the TV's menu language to one other than the Player's.
The TV sends `<Set Menu Language>` to the bus. The Player's menus (the
browser and the idle screen) stay in their own language. Risk of low
harm, and not measured: the person sees a mismatch between the TV's
menus and the Player's. The playback pod's audio and subtitle language
preferences do not change, and they should not, because the menu
language and the film's track languages are two settings.

### What the spec requires

- CEC Table 12: `<Set Menu Language>` is mandatory for a follower,
  "All, except for TV, CEC Switches and devices without OSD/Menu
  generation capabilities". Its response to a receiver is "Set the menu
  language as specified, if possible."
- CEC 13.6.2: "On receipt of the `<Set Menu Language>` message, the
  device shall attempt to use the newly selected [Language] for Menus
  and OSDs." The device may receive the message when the language has
  not changed.
- CEC 13.6.2: "A device shall ignore any of the above messages that
  come from an initiator address other than 0 (the TV)."
- CEC 13.6.2: "When a source device is powered on, it should send a
  `<Get Menu Language>` message to the TV. The TV shall then respond
  ... with a `<Set Menu Language>` message." The TV may send `<Get Menu
  Language>` to a device during its own installation, and the device
  then responds with `<Set Menu Language>` (CEC Table 12).
- CEC 13.6.2: the language is three ASCII letters from the
  bibliographic codes of ISO 639-2. Chinese uses "zho" and "chi".

The Player draws menus, so CEC Table 12 makes the attempt mandatory.
The word "attempt" and the words "if possible" allow a Player that has
no translation for the language to keep its own, so the mandatory part
is to receive the message, record it, and use it where a translation
exists.

### What `liken` does now

The follower drops `<Set Menu Language>` (`follower.go:73`). Nothing
sends `<Get Menu Language>`, and nothing answers one from the TV: the
follower aborts `<Get Menu Language>` as unrecognized (`follower.go:102`).
No screen has a translation, as the section above states.

### Questions to answer

1. Which component records the TV's language, and who reads it? The
   node workload hears the message, so it records the value. The
   options for where it lands:
   - `Television` status, as a field beside `activeSource`
     (`equipment-operator/television.go:105-107`). The fact belongs to
     the TV, and `kubectl` shows it. The screens do not read
     `Television` today.
   - `MediaPreferences` `spec`. This is where the household's language
     choices sit (`media-operator/api.go:686-689`), but it is spec, so
     a person writes it, and the operator would overwrite a person's
     edit. A status field on another kind avoids the conflict.
   - `Player` status. The screens already read the `Player`. The
     `Player` is not the owner of the fact, since two Players can share
     one TV.
2. Does the menu language follow the TV or the household? The TV's
   `<Set Menu Language>` is one household member's choice on one
   screen. Options: the Player follows the TV always (the spec's
   default), follows it unless a person set a language on the
   `Player`, or ignores it and only records it. "Attempt" allows the
   third. It leaves the mandatory row unmet in spirit even though the
   message is read.
3. Which screens can use a language, and what happens for a code with
   no translation? No screen has strings for more than one language.
   Two pieces of work are separate and can ship apart: recording the
   language (small, in the node workload) and drawing in it (a
   translation effort in `library-operator/media-browser`,
   `media-operator/idle`, and `media-screen`). The plan has to decide
   whether the first alone counts as "attempt". The spec permits a
   device that cannot use a language to say so by keeping its own, but
   a status field does not tell the person that the Player tried.
4. Does the Player send `<Get Menu Language>` at start, and when does
   "powered on" apply? A Player pod restart is not a power-on of the
   device. Options: send it when the adapter first claims a logical
   address on a bus, send it when a session wakes the room, or send
   it on both. Each send costs one directed message, and each answer
   arrives as a `<Set Menu Language>`. Sending at every wake asks a
   question the last answer already settled. Sending once per claim
   fits CEC 13.6.2 and the once-per-join scan.
5. Does the node workload answer a TV's `<Get Menu Language>`? CEC
   Table 12 makes the addressed device respond with `<Set Menu
   Language>`. The device answers with its own language, not the TV's.
   With one language drawn, the answer is that language. The answer
   changes when translations exist.
6. What does the node workload do with a message from another initiator
   address, and with a language code it does not recognize? The first
   is a required ignore (CEC 13.6.2). The second is not stated, and a
   log line with no state change is the smallest choice.

## What the three features share

- A reader for the `Player` or `Play` status inside the node workload,
  for Menu Control and for Deck Control. One design serves both if the
  choice in Menu Control question 2 and Deck Control question 2 is made
  together.
- The rule for claiming the input. All three features send only replies
  to the TV and never `<Active Source>`. Two answers depend on being the
  active source (`<Menu Status>`, and by the standard, any wake from
  `<Play>`). Gap 1 of the conformance review said that the
  active-source state was wrong after a routing change. Menu Control
  is only as right as that state. Commit 3c425c3f fixed gap 1, so this
  dependency is met.
- A reason on every refusal. `<Feature Abort>` reasons other than
  "Unrecognized opcode" (`equipment-operator/cec/message.go`, the
  `AbortUnrecognizedOpcode` reason at the call in `follower.go:102`)
  are needed once a feature exists and cannot act: "Not in correct mode
  to respond" for no `Play`, and "Refused" for a request that the
  standard to claim only when asked declines.
- The `answers` set. Adding a reply for an opcode removes the opcode
  from the set for a request the follower now answers, and keeps it for
  an opcode that only carries an answer. `<Menu Status>` and
  `<Deck Status>` stay in the set, since the Player only sends them.

## Options at the plan level

The three features can be built together or apart, and the trade-offs
differ.

- **Language first.** The recording half needs no reader for the
  `Player` status and no claim on the input. It meets the mandatory
  part of CEC Table 12 as far as receiving and recording. It leaves
  the drawing half owed until a screen has a translation.
- **Menu Control second.** It carries the most risk of a person's
  remote not working, and the smallest set of messages. Its correct
  form depends on the reader question. The active-source fix is
  built.
- **Deck Control last.** It needs no new sidecar action, because
  `play`, `hold`, and `stop` exist. It needs a route from the node
  workload to a `Play` through `Television` or `Receiver` status, and a
  decision on waking. It has the most parts, and the least evidence
  of need, since the first drill's TV sent keys for navigation and no
  transport keys were tested.
- **All at once.** One reader and one route serve all three, and the
  work touches `equipment-operator`, `media-operator`, and the screens
  in one change. The change is large, and one review has to
  cover three components.
- **Drill before design.** A capture of the messages a room's TV sends
  (`Listen` on the room for an hour, with the TV's menus and remote
  used) shows which of `<Menu Request>`, `<Play>`, `<Deck Control>`,
  and `<Give Deck Status>` a TV sends at all. A feature no TV sends is
  a feature to build for conformance only, and the maintainer's
  standard says to build it.

## Not in this plan

- System Audio Control, which equipment-operator plan 09 covers.
- The deterministic remote functions of `<User Control Pressed>`
  (gap 5 of the conformance review). Commit 3c425c3f built them, and
  Deck Control uses the sidecar actions they added.
- The active-source state after a routing change (gap 1 of the
  conformance review). Commit 3c425c3f fixed it.
- A menu in any language other than the one the screens draw now.
  Writing translations is a different plan.

## How it will be proved

Each feature gets a test that fails first, then the change that passes
it. The follower's tests already table the answers to a request
(`equipment-operator/cec/follower_test.go`), so each new reply adds
rows there. The drill is owed for all three.

- Follower tests: `<Menu Request>` "Query" while the adapter is the
  active source returns `<Menu Status>`; the same request while it is
  not gets the answer that question 5 of Menu Control chooses.
  `<Give Deck Status>` returns `<Deck Status>` for a `Play` in each
  activity. `<Set Menu Language>` from address 0 records the language,
  and from any other address records nothing.
- Node workload tests: the recorded language reaches its status field;
  a `<Play>` and a `<Deck Control>` reach the route that question 2 of
  Deck Control chooses.
- Sidecar tests: the tests of `play`, `hold`, and `stop`
  (`media-operator/input_test.go`, `media-operator/stop_test.go`)
  already cover the actions a deck message reaches. A new test covers
  the relay from `Television` status to the `Play`'s commands topic.
- A drill on a `Player` with a `Receiver` and a TV:
  - The TV's menu language changes, and `kubectl` shows the new value.
  - With the browser on the screen, `<Menu Request>` "Query" from the
    TV returns "Activated", and a TV that gates navigation keys on the
    message forwards them.
  - The TV's transport controls, where it has them, play, pause, and
    stop a `Play`.
- A `cec-compliance` run from v4l-utils against the adapter, before and
  after, to check that the refusals name the right reasons.
