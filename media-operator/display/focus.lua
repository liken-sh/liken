-- The input router and the summon state of the OSD. This slice holds one
-- region, the scrubber.
local seekbar = require("seekbar")

local focus = {}

-- Hide the OSD after this many idle seconds of play. A pause does not start
-- the countdown.
local IDLE_HIDE = 4

local summoned = false
local paused = false
local hide_timer = nil
local redraw_cb = function() end

function focus.set_redraw(fn)
  redraw_cb = fn
end

local function cancel_hide()
  if hide_timer then
    hide_timer:kill()
    hide_timer = nil
  end
end

local function arm_hide()
  cancel_hide()
  if paused then
    return
  end
  hide_timer = mp.add_timeout(IDLE_HIDE, function()
    hide_timer = nil
    summoned = false
    redraw_cb()
  end)
end

function focus.summon()
  summoned = true
  arm_hide()
  redraw_cb()
end

function focus.dismiss()
  summoned = false
  cancel_hide()
  redraw_cb()
end

-- main observes pause and reports it here. A pause summons the OSD and holds
-- it, and a resume starts the idle countdown.
function focus.on_pause(p)
  paused = p
  if p then
    summoned = true
    cancel_hide()
  elseif summoned then
    arm_hide()
  end
  redraw_cb()
end

-- Any navigation press summons the OSD. left and right route to the seekbar.
-- up and down do nothing until 07-b adds a second region.
function focus.nav(action)
  if action == "back" then
    focus.dismiss()
    return
  end
  focus.summon()
  if action == "left" then
    seekbar.seek(-1)
  elseif action == "right" then
    seekbar.seek(1)
  end
end

function focus.scrubber_visible()
  return summoned
end

return focus
