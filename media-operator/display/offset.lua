-- One delay offset control. The strip carries two, one for the audio delay and
-- one for the subtitle delay, so each offset reads beside the track it shifts.
-- offset.new builds one bound to a property.
local theme = require("theme")
local presentation = require("presentation")

local offset = {}

-- The nudge step and the clamp range for a delay, in seconds. One press moves
-- the delay by the step, and the clamp holds it within the range.
local STEP = 0.05
local RANGE = 5

local function clamp(v)
  return math.max(-RANGE, math.min(RANGE, v))
end

-- Format a delay in seconds with a sign, for the panel.
local function fmt_seconds(v)
  if math.abs(v) < 0.001 then
    return "0.00 s"
  end
  local sign = v > 0 and "+" or "-"
  return string.format("%s%.2f s", sign, math.abs(v))
end

-- Format a delay in milliseconds, for the strip cell.
local function fmt_ms(v)
  local ms = math.floor(math.abs(v) * 1000 + 0.5)
  if ms == 0 then
    return "0 ms"
  end
  local sign = v > 0 and "+" or "-"
  return string.format("%s%d ms", sign, ms)
end

-- Build one offset control bound to a property. cfg carries the property, the
-- panel label, and an optional track type the offset needs.
function offset.new(cfg)
  local self = {}
  local open = false
  local redraw_cb = function() end

  function self.set_redraw(fn)
    redraw_cb = fn
  end

  -- The offset shows for a playing video. An offset bound to a track type also
  -- needs a track of that type, so the subtitle offset hides with no subtitles.
  function self.available()
    if presentation.type() == "image" then
      return false
    end
    if cfg.track then
      for _, t in ipairs(mp.get_property_native("track-list") or {}) do
        if t.type == cfg.track then
          return true
        end
      end
      return false
    end
    return true
  end

  function self.value()
    return fmt_ms(mp.get_property_number(cfg.prop) or 0)
  end

  function self.draw(x, y, focused)
    local color = focused and theme.color.fill or theme.color.text
    local alpha = focused and theme.alpha.opaque or theme.alpha.subdued
    return theme.text(x, y, self.value(), theme.type.tiny, color, 4, alpha)
  end

  function self.activate()
    open = true
    return self
  end

  function self.is_open()
    return open
  end

  function self.close()
    open = false
  end

  -- left and right nudge the delay by a step, and the nudge applies at once, so
  -- there is nothing to confirm. up resets the delay to zero. down and select
  -- close the adjuster, and back closes it through focus.
  function self.handle(action)
    local v = mp.get_property_number(cfg.prop) or 0
    if action == "left" then
      mp.set_property_number(cfg.prop, clamp(v - STEP))
    elseif action == "right" then
      mp.set_property_number(cfg.prop, clamp(v + STEP))
    elseif action == "up" then
      mp.set_property_number(cfg.prop, 0)
    elseif action == "down" or action == "select" then
      open = false
    end
  end

  -- A small panel: the label, the delay in large type, and a nudge hint.
  function self.draw_chooser()
    local X = theme.margin.x
    local W = 720
    local PAD = 24
    local BOTTOM = 876
    local H = 200
    local top = BOTTOM - H
    local v = mp.get_property_number(cfg.prop) or 0
    local parts = {}
    parts[#parts + 1] = theme.panel(X, top, W, H)
    parts[#parts + 1] = theme.text(X + PAD, top + PAD, cfg.label, theme.type.small, theme.color.muted, 7)
    parts[#parts + 1] = theme.text(X + PAD, top + PAD + 54, fmt_seconds(v), theme.type.title, theme.color.text, 7)
    parts[#parts + 1] =
      theme.text(X + PAD, top + H - PAD - 20, "left and right nudge, up resets, down done", theme.type.small, theme.color.muted, 7)
    return table.concat(parts, "\n")
  end

  return self
end

return offset
