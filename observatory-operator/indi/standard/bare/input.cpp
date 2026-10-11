// The INPUT mixin on a plain INDI::DefaultDevice, with one digital and
// one analog input. The mixin defines one property for each input, so
// the number of inputs is the driver's choice.
#include <defaultdevice.h>
#include <indiinputinterface.h>
#include <cstring>
#include <memory>

class BareInput : public INDI::DefaultDevice, public INDI::InputInterface
{
  public:
    BareInput() : INDI::InputInterface(this) {}
    const char *getDefaultName() override { return "Bare Input"; }
    bool initProperties() override
    {
        INDI::DefaultDevice::initProperties();
        INDI::InputInterface::initProperties("Inputs", 1, 1);
        setDriverInterface(AUX_INTERFACE | INPUT_INTERFACE);
        addAuxControls();
        return true;
    }
    bool updateProperties() override
    {
        INDI::DefaultDevice::updateProperties();
        INDI::InputInterface::updateProperties();
        return true;
    }
    bool ISNewText(const char *dev, const char *name, char *texts[], char *names[], int n) override
    {
        if (dev && !strcmp(dev, getDeviceName()) && INDI::InputInterface::processText(dev, name, texts, names, n))
            return true;
        return INDI::DefaultDevice::ISNewText(dev, name, texts, names, n);
    }
    bool Connect() override { return true; }
    bool Disconnect() override { return true; }
    bool UpdateDigitalInputs() override { return true; }
    bool UpdateAnalogInputs() override { return true; }
};

static std::unique_ptr<BareInput> device(new BareInput());
