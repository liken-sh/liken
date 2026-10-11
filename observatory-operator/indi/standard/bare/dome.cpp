// INDI::Dome with every capability, parked by azimuth. DOME has no
// mixin, so the driver inherits the base class. The BARE_ROOF variant is a
// roll-off roof as libindi's roll_off driver declares one: it parks
// and aborts, has no azimuth, and DOME_MOTION opens and closes it.
#include <indidome.h>
#include <memory>

class BareDome : public INDI::Dome
{
  public:
    BareDome()
    {
        setDomeConnection(CONNECTION_NONE);
#ifdef BARE_ROOF
        SetDomeCapability(DOME_CAN_ABORT | DOME_CAN_PARK);
#else
        SetDomeCapability(DOME_CAN_ABORT | DOME_CAN_ABS_MOVE | DOME_CAN_REL_MOVE | DOME_CAN_PARK |
                          DOME_CAN_SYNC | DOME_HAS_SHUTTER | DOME_HAS_VARIABLE_SPEED | DOME_HAS_BACKLASH);
#endif
    }
    const char *getDefaultName() override { return "Bare Dome"; }
    bool initProperties() override
    {
        INDI::Dome::initProperties();
#ifdef BARE_ROOF
        SetParkDataType(PARK_NONE);
#else
        SetParkDataType(PARK_AZ);
#endif
        addAuxControls();
        return true;
    }
    bool Connect() override { return true; }
    bool Disconnect() override { return true; }
};

static std::unique_ptr<BareDome> device(new BareDome());
