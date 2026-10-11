// The properties that every driver defines through INDI::DefaultDevice
// and its connection plugins, and that belong to no interface. The
// capture of each interface leaves out every property this driver
// defines. The serial and the TCP plugins are both registered, so the
// capture holds the properties of both transports.
#include <defaultdevice.h>
#include <connectionplugins/connectionserial.h>
#include <connectionplugins/connectiontcp.h>
#include <memory>

class BareDevice : public INDI::DefaultDevice
{
  public:
    const char *getDefaultName() override { return "Bare Device"; }
    bool initProperties() override
    {
        INDI::DefaultDevice::initProperties();
        registerConnection(new Connection::Serial(this));
        registerConnection(new Connection::TCP(this));
        setDriverInterface(GENERAL_INTERFACE);
        addAuxControls();
        return true;
    }
    bool Connect() override { return true; }
    bool Disconnect() override { return true; }
};

static std::unique_ptr<BareDevice> device(new BareDevice());
