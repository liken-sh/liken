-- Gives a sink its own channels back after a bitstream played through
-- it.
--
-- WirePlumber links an encoded stream, such as a film's Dolby TrueHD
-- track, by putting both the stream and the sink in passthrough mode.
-- A sink in that mode has no port format. When the next PCM stream
-- arrives, WirePlumber 0.5 finds no format on either side of the new
-- link, and builds its default for the sink: two channels, FL and FR
-- (configure_and_link_adapters in module-si-standard-link.c and
-- build_adapter_default_format in module-si-audio-adapter.c). So after
-- one film with a bitstream, a 7.1 sink plays every later stream
-- folded into the front pair, until WirePlumber starts again.
--
-- This hook runs in the chain that links a stream, after the target is
-- chosen and before the link is made. For a link that will pass a
-- bitstream through, it remembers the port format the sink has now,
-- which is the format WirePlumber configured from the sink's own
-- channels when it first read the node. For any other link to a sink
-- that is still in passthrough mode, it sets that format on the sink
-- again and waits for the ports, so the link-target hook finds the
-- sink configured and gives the stream the same channels.
--
-- A sink that WirePlumber finds in passthrough mode at its own start
-- needs nothing from this hook, because WirePlumber configures every
-- node it finds with the node's own channels.

lutils = require ("linking-utils")
log = Log.open_topic ("s-linking")

-- The port format of each sink before its first bitstream, by the id
-- of the sink's session item.
local remembered = {}

AsyncEventHook {
  name = "linking/liken-restore-device-ports",
  after = "linking/prepare-link",
  before = "linking/link-target",
  interests = {
    EventInterest {
      Constraint { "event.type", "=", "select-target" },
    },
  },
  steps = {
    start = {
      next = "none",
      execute = function (event, transition)
        local _, _, _, _, si_flags, target =
            lutils:unwrap_select_target_event (event)
        if not target then
          transition:advance ()
          return
        end

        local format, mode = target:get_ports_format ()
        if si_flags.can_passthrough then
          if format and mode ~= "passthrough" then
            remembered [target.id] = format
          end
          transition:advance ()
          return
        end

        local ports = remembered [target.id]
        if mode ~= "passthrough" or not ports then
          transition:advance ()
          return
        end

        log:notice (target, "a bitstream left this sink in passthrough mode; " ..
            "setting the port format it had before")
        target:set_ports_format (ports, "dsp", function (_, e)
          if e then
            transition:return_error ("setting the sink's port format after a bitstream: " ..
                tostring (e))
            return
          end
          transition:advance ()
        end)
      end,
    },
  },
}:register ()
