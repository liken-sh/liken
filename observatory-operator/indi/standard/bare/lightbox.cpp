// The LIGHTBOX mixin on a plain INDI::DefaultDevice. The mixin has no
// base class, and it defines its properties in ISGetProperties as well
// as at connect.
#include <defaultdevice.h>
#include <indilightboxinterface.h>
#include <memory>

class BareLightBox : public INDI::DefaultDevice, public INDI::LightBoxInterface
{
  public:
    BareLightBox() : INDI::LightBoxInterface(this) {}
    const char *getDefaultName() override { return "Bare Light Box"; }
    bool initProperties() override
    {
        INDI::DefaultDevice::initProperties();
        LI::initProperties(MAIN_CONTROL_TAB, LI::CAN_DIM);
        setDriverInterface(AUX_INTERFACE | LIGHTBOX_INTERFACE);
        addAuxControls();
        return true;
    }
    void ISGetProperties(const char *dev) override
    {
        INDI::DefaultDevice::ISGetProperties(dev);
        LI::ISGetProperties(dev);
    }
    bool updateProperties() override
    {
        INDI::DefaultDevice::updateProperties();
        LI::updateProperties();
        return true;
    }
    bool ISNewSwitch(const char *dev, const char *name, ISState *states, char *names[], int n) override
    {
        if (LI::processSwitch(dev, name, states, names, n))
            return true;
        return INDI::DefaultDevice::ISNewSwitch(dev, name, states, names, n);
    }
    bool ISNewNumber(const char *dev, const char *name, double values[], char *names[], int n) override
    {
        if (LI::processNumber(dev, name, values, names, n))
            return true;
        return INDI::DefaultDevice::ISNewNumber(dev, name, values, names, n);
    }
    bool ISNewText(const char *dev, const char *name, char *texts[], char *names[], int n) override
    {
        if (LI::processText(dev, name, texts, names, n))
            return true;
        return INDI::DefaultDevice::ISNewText(dev, name, texts, names, n);
    }
    bool ISSnoopDevice(XMLEle *root) override
    {
        LI::snoop(root);
        return INDI::DefaultDevice::ISSnoopDevice(root);
    }
    bool Connect() override { return true; }
    bool Disconnect() override { return true; }
};

static std::unique_ptr<BareLightBox> device(new BareLightBox());
