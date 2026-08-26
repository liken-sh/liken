-- The preview keys, the workstation stand-in for the bus. On a cluster the idle
-- pod's sidecar forwards the Player's retained status and the reveal, and there
-- is no keyboard. On a workstation local/idle sets IDLE_PREVIEW=1 and this
-- module binds keys that build the same JSON the sidecar sends and hand it to
-- the same handler, so the ramps, the dim, and the pulse can be seen before a
-- release. Nothing here runs with the variable unset.
local utils = require("mp.utils")
local theme = require("theme")

local preview = {}

-- Whether enable has run. The legend draws only then, so the module can be
-- required unconditionally and still draw nothing on a cluster.
local enabled = false

-- The parts come from the environment seed, which carries names and no kinds.
-- identity owns that seed's format and exports the splitter for it. The
-- preview calls the last part the remote, because the parts read in the
-- order the operator lists them and a controller is the part a person adds
-- last. That part is the one whose presence the d key toggles.
local identity = require("identity")

local kind = "Idle"
local connected = true

-- Whether the fake status marks the remote focused. The f key sets it and
-- pulses, the way a live mark landing here does, and the g key clears it,
-- the way a cycle onto another unit does.
local focused = false

-- Whether the s key last put the screen to sleep, so the one key plays
-- the two edges of the fade in turn.
local asleep = false

local function status_json()
  local names = identity.split_lines(os.getenv("IDLE_PLAYER_COMPONENTS"))
  local components = {}
  for index, name in ipairs(names) do
    local component = { name = name, kind = "sink" }
    if index == #names then
      component.kind = "remote"
      component.connected = connected
      if focused then
        component.focused = true
      end
    end
    components[#components + 1] = component
  end
  local status = {
    displayName = os.getenv("IDLE_PLAYER_NAME"),
    activity = kind,
    components = components,
  }
  -- The Play block appears only while a Play starts or runs, the way the
  -- operator publishes it.
  if kind ~= "Idle" then
    status.play = { name = "sailing", title = "Sailing" }
  end
  return utils.format_json(status)
end

-- enable binds the keys. send_status takes the status as a JSON string and
-- send_revealed takes no argument, so both are the handlers the script messages
-- call and the preview exercises the same code.
function preview.enable(send_status, send_revealed, send_sleep, send_wake)
  local function publish(activity)
    kind = activity
    send_status(status_json())
  end

  mp.add_key_binding("p", "liken-preview-starting", function()
    publish("Starting")
  end)
  mp.add_key_binding("o", "liken-preview-playing", function()
    publish("Playing")
  end)
  -- i plays the end of a film: the status returns to Idle and the sidecar
  -- reports the idle surface back in view, in that order.
  mp.add_key_binding("i", "liken-preview-idle", function()
    publish("Idle")
    send_revealed()
  end)
  mp.add_key_binding("d", "liken-preview-presence", function()
    connected = not connected
    send_status(status_json())
  end)
  -- f plays a live focus message naming this unit: the sidecar sends the
  -- pulse and the operator's status follows with the focused part. A first
  -- press draws the hexagon and beats it, and every later press beats it
  -- again, which is the cycle press that wraps onto the unit already
  -- focused. The index is 0 because the preview's status lists one remote.
  mp.add_key_binding("f", "liken-preview-focus", function()
    focused = true
    send_status(status_json())
    mp.commandv("script-message", "focus-pulse", "0")
  end)
  -- g plays the focus cycling away: the status stops marking the remote,
  -- the hexagon goes, and nothing beats.
  mp.add_key_binding("g", "liken-preview-unfocus", function()
    focused = false
    send_status(status_json())
  end)
  -- s plays the two edges of the fade in turn, the quiet window that
  -- runs out and the press that follows, through the same handlers the
  -- two script messages call.
  mp.add_key_binding("s", "liken-preview-sleep", function()
    asleep = not asleep
    if asleep then
      send_sleep()
    else
      send_wake()
    end
  end)

  -- The volume keys play the idle sidecar's part: on a cluster a press
  -- publishes the unit's next state, and the sidecar sets the properties
  -- and sends volume-changed. Here the key does both, so the indicator
  -- shows on the idle surface the way a press shows it. 9, 0, and m are
  -- mpv's own volume keys.
  local function press_volume(step)
    local level = mp.get_property_number("volume") or 100
    level = math.max(0, math.min(100, level + step))
    mp.set_property_number("volume", level)
    mp.commandv("script-message", "volume-changed")
  end
  mp.add_key_binding("9", "liken-preview-volume-down", function()
    press_volume(-5)
  end, { repeatable = true })
  mp.add_key_binding("0", "liken-preview-volume-up", function()
    press_volume(5)
  end, { repeatable = true })
  mp.add_key_binding("m", "liken-preview-mute", function()
    mp.set_property_bool("mute", not mp.get_property_bool("mute"))
    mp.commandv("script-message", "volume-changed")
  end)

  mp.msg.info("liken display preview keys: p starting, o playing, i idle, d presence, f focus, g unfocus, s sleep, 9/0 volume, m mute")
  enabled = true
end

-- The legend, one dim line at the bottom right, so the keys read on the screen
-- itself and not only in this file. It draws only after enable ran, which only
-- local/idle causes, so an idle pod on a cluster draws no legend. The an3
-- alignment anchors the line at its bottom-right, mirroring the identity block's
-- bottom-left across the screen.
function preview.draw()
  if not enabled then
    return nil
  end
  local legend = "p play starts    o playing    i film ends    d presence    f focus    g unfocus    s sleep    9/0 volume    m mute    q quit"
  return theme.text(
    theme.canvas.w - theme.margin.x, theme.canvas.h - theme.margin.y,
    legend, theme.type.tiny, theme.color.muted, 3, theme.alpha.dim
  )
end

return preview
