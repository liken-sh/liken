// The DUSTCAP mixin on a plain INDI::DefaultDevice. The mixin has no
// base class.
#include <defaultdevice.h>
#include <indidustcapinterface.h>
#include <memory>

class BareDustCap : public INDI::DefaultDevice, public INDI::DustCapInterface
{
  public:
    BareDustCap() : INDI::DustCapInterface(this) {}
    const char *getDefaultName() override { return "Bare Dust Cap"; }
    bool initProperties() override
    {
        INDI::DefaultDevice::initProperties();
        DI::initProperties(MAIN_CONTROL_TAB, DI::CAN_ABORT | DI::CAN_SET_POSITION | DI::CAN_SET_LIMITS);
        setDriverInterface(AUX_INTERFACE | DUSTCAP_INTERFACE);
        addAuxControls();
        return true;
    }
    bool updateProperties() override
    {
        INDI::DefaultDevice::updateProperties();
        DI::updateProperties();
        return true;
    }
    bool ISNewSwitch(const char *dev, const char *name, ISState *states, char *names[], int n) override
    {
        if (DI::processSwitch(dev, name, states, names, n))
            return true;
        return INDI::DefaultDevice::ISNewSwitch(dev, name, states, names, n);
    }
    bool Connect() override { return true; }
    bool Disconnect() override { return true; }
};

static std::unique_ptr<BareDustCap> device(new BareDustCap());
