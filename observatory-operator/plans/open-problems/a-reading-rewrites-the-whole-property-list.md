# A reading rewrites the whole property list

Open problem. A device's status holds the full list of the INDI
properties its driver defines, with their definitions and their current
values. When one value changes, the operator writes the whole status,
and every watcher of the kind decodes the whole object again. The list
is the bulk of each write.

## What happens

Plan 06 put every property in `status.properties`, so a person sees a
vendor property's name, limits, and current value with `kubectl`. The
simulated CCD defines 56 properties once connected, the mount 25, and
the focuser 16, so a device's status is about 15 to 23 KB.

A mount that does not track changes its right ascension every second,
and the simulated CCD copies that value into its own properties. On the
two-node test cluster on 2026-10-06, with two telescopes Ready, five
objects wrote about 4.2 times a second in all, and the operator used 41
to 42m of CPU. With no reservation it used 0 to 1m. Almost all of the
41m is the encoding of each write and the decoding of the watch event
that follows it.

## Options considered

- **Keep the list as it is.** This is the current choice: the cost is
  paid only while a reservation is active, and 41m is acceptable on the
  test cluster's hardware.
- **Keep the definitions in status and drop the values.** The typed
  `readings` keep the values that change often. A vendor property's
  current value is then visible only through INDI.
- **Split the definitions and the values.** A split inside one object
  saves little, because each write still sends the definitions. The
  definitions would have to move to a separate object for each device,
  such as a 21st kind. That adds a kind that people never write.

Return to this when a rig with more devices, or a slower node, makes
the cost visible.
