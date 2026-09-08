-- The fake mp table the display's tests run against. mpv is the only host
-- that defines the mp global. This one records the commands a module sends and
-- holds the timeouts it arms, so a test reads what the display did and fires a
-- timeout without a wait.
-- It also answers the properties a module reads, and it holds the periodic
-- timers a fade runs on.

local harness = {}

-- One fake mp table per test, so no test reads another test's commands.
-- properties is the table every property read answers from.
function harness.new(properties)
  local fake = {
    commands = {},
    timeouts = {},
    periodics = {},
    properties = properties or {},
    time = 0,
  }

  -- mpv runs the command. This records it, in order.
  function fake.command_native(command)
    fake.commands[#fake.commands + 1] = command
    return true
  end

  -- commandv takes the same command as separate arguments, and it records
  -- the same shape command_native records.
  function fake.commandv(...)
    fake.commands[#fake.commands + 1] = { ... }
    return true
  end

  function fake.get_property_native(name, default)
    local value = fake.properties[name]
    if value == nil then
      return default
    end
    return value
  end

  fake.get_property = fake.get_property_native
  fake.get_property_number = fake.get_property_native

  -- The scrubber measures a hold against this clock. A test moves it by
  -- setting fake.time.
  function fake.get_time()
    return fake.time
  end

  -- The handle mpv returns carries kill, and the display kills a timer it no
  -- longer needs. There is no clock here, so the fake records the kill.
  function fake.add_timeout(seconds, fn)
    local handle = { seconds = seconds, fn = fn, killed = false, fired = false }
    function handle:kill()
      self.killed = true
    end
    fake.timeouts[#fake.timeouts + 1] = handle
    return handle
  end

  -- A fade runs on a periodic timer. settle below steps every live one until
  -- it stops.
  function fake.add_periodic_timer(seconds, fn)
    local handle = { seconds = seconds, fn = fn, killed = false }
    function handle:kill()
      self.killed = true
    end
    fake.periodics[#fake.periodics + 1] = handle
    return handle
  end

  -- Run the newest timeout that is neither killed nor fired, which is what
  -- mpv's event loop does when the seconds pass with no reply.
  function fake.fire_timeout()
    for i = #fake.timeouts, 1, -1 do
      local handle = fake.timeouts[i]
      if not handle.killed and not handle.fired then
        handle.fired = true
        handle.fn()
        return handle
      end
    end
    return nil
  end

  -- Run every live periodic timer until all of them stop, the way a fade
  -- stops when it reaches its target. A timer that never stops fails the
  -- test.
  function fake.settle(limit)
    for _ = 1, limit or 400 do
      local live = false
      for _, handle in ipairs(fake.periodics) do
        if not handle.killed then
          live = true
          handle.fn()
        end
      end
      if not live then
        return
      end
    end
    error("a periodic timer never stopped")
  end

  return fake
end

return harness
