// INDI::Telescope with every capability. TELESCOPE has no mixin, so the
// driver inherits the base class. BARE_PARK names the park type, which sets
// the members of TELESCOPE_PARK_POSITION, so each park type is its own
// executable.
#include <inditelescope.h>
#include <memory>

class BareTelescope : public INDI::Telescope
{
  public:
    BareTelescope()
    {
        setTelescopeConnection(CONNECTION_NONE);
        // TELESCOPE_SLEW_RATE has one member for each rate, and a driver
        // that states 4 gets the members 1x to 4x.
        SetTelescopeCapability(TELESCOPE_CAN_GOTO | TELESCOPE_CAN_SYNC | TELESCOPE_CAN_PARK | TELESCOPE_CAN_ABORT |
                               TELESCOPE_HAS_TIME | TELESCOPE_HAS_LOCATION | TELESCOPE_HAS_PIER_SIDE |
                               TELESCOPE_HAS_PEC | TELESCOPE_HAS_TRACK_MODE | TELESCOPE_CAN_CONTROL_TRACK |
                               TELESCOPE_HAS_TRACK_RATE | TELESCOPE_HAS_PIER_SIDE_SIMULATION |
                               TELESCOPE_CAN_TRACK_SATELLITE | TELESCOPE_CAN_FLIP | TELESCOPE_CAN_HOME_FIND |
                               TELESCOPE_CAN_HOME_SET | TELESCOPE_CAN_HOME_GO,
                               4);
    }
    const char *getDefaultName() override { return "Bare Telescope"; }
    bool initProperties() override
    {
        INDI::Telescope::initProperties();
        // TELESCOPE_TRACK_MODE has one member for each mode the driver
        // adds. These are the four that libindi's own drivers add.
        AddTrackMode("TRACK_SIDEREAL", "Sidereal", true);
        AddTrackMode("TRACK_SOLAR", "Solar");
        AddTrackMode("TRACK_LUNAR", "Lunar");
        AddTrackMode("TRACK_CUSTOM", "Custom");
        SetParkDataType(BARE_PARK);
        addAuxControls();
        return true;
    }
    bool Connect() override { return true; }
    bool Disconnect() override { return true; }
    bool ReadScopeStatus() override
    {
        NewRaDec(0, 0);
        return true;
    }
};

static std::unique_ptr<BareTelescope> device(new BareTelescope());
