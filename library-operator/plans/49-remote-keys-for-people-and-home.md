# Remote keys for people and home

Plan 49. The X6 has two keys the browser does not use well: the
compose key, which today goes to the home page, and the WWW key,
which passes through and no screen takes. This plan gives the compose
key to the person picker and the WWW key to the home page, and it
names what the page-up and page-down keys could do.

## The problem

The person picker is the first screen the browser shows, and there is
no key that brings it back. A house of several people changes who is
watching more often than it changes rooms, and the picker is where
that happens. The home page has a key, but it is the compose key,
which was bound there only because the remote drives one unit and had
no use for the focus cycle.

## The contract

* The browser takes a new key word, `people`, which raises the person
  picker over any page, the way `home` pops to the home page. The
  kernel name for it is `KEY_ADDRESSBOOK`, the key the kernel gives a
  contacts button.
* The living room's `Keymap` binds the compose key to
  `KEY_ADDRESSBOOK` and the WWW key to `KEY_HOMEPAGE`. That is one
  edit in the house repository and no change to the media operator.
* Page up and page down stay unbound in this plan. Two uses fit them
  and the design should pick one: on a wall, jump one rail stop, the
  way the jump rail already moves by era, season, year, or letter;
  during a film, skip one chapter. The wall use needs the browser and
  the film use needs the media operator's key bindings.

## The proof

The browser change is small and local: the key table and the picker.
A local run proves the word, and the living room proves the keys.
