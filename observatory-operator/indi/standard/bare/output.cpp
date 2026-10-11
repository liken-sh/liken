// The OUTPUT mixin on a plain INDI::DefaultDevice, with one digital
// output. The mixin defines one property for each output, so the
// number of outputs is the driver's choice.
#include <defaultdevice.h>
#include <indioutputinterface.h>
#include <cstring>
#include <memory>

class BareOutput : public INDI::DefaultDevice, public INDI::OutputInterface
{
  public:
    BareOutput() : INDI::OutputInterface(this) {}
    const char *getDefaultName() override { return "Bare Output"; }
    bool initProperties() override
    {
        INDI::DefaultDevice::initProperties();
        INDI::OutputInterface::initProperties("Outputs", 1);
        setDriverInterface(AUX_INTERFACE | OUTPUT_INTERFACE);
        addAuxControls();
        return true;
    }
    bool updateProperties() override
    {
        INDI::DefaultDevice::updateProperties();
        INDI::OutputInterface::updateProperties();
        return true;
    }
    bool ISNewSwitch(const char *dev, const char *name, ISState *states, char *names[], int n) override
    {
        if (dev && !strcmp(dev, getDeviceName()) && INDI::OutputInterface::processSwitch(dev, name, states, names, n))
            return true;
        return INDI::DefaultDevice::ISNewSwitch(dev, name, states, names, n);
    }
    bool ISNewNumber(const char *dev, const char *name, double values[], char *names[], int n) override
    {
        if (dev && !strcmp(dev, getDeviceName()) && INDI::OutputInterface::processNumber(dev, name, values, names, n))
            return true;
        return INDI::DefaultDevice::ISNewNumber(dev, name, values, names, n);
    }
    bool ISNewText(const char *dev, const char *name, char *texts[], char *names[], int n) override
    {
        if (dev && !strcmp(dev, getDeviceName()) && INDI::OutputInterface::processText(dev, name, texts, names, n))
            return true;
        return INDI::DefaultDevice::ISNewText(dev, name, texts, names, n);
    }
    bool Connect() override { return true; }
    bool Disconnect() override { return true; }
    bool UpdateDigitalOutputs() override { return true; }
    bool CommandOutput(uint32_t, OutputState) override { return true; }
};

static std::unique_ptr<BareOutput> device(new BareOutput());
