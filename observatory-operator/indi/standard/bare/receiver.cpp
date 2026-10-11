// INDI::Receiver with every capability. RECEIVER_SETTINGS comes from
// INDI::Receiver, which the receiver simulator and every receiver
// driver in indi and indi-3rdparty inherit. INDI::Spectrograph sets the
// same SPECTROGRAPH interface bit and defines SPECTROGRAPH_SETTINGS
// instead, and the one driver that inherits it, indi-ahp-xc, is a
// correlator. Neither class has a mixin.
#include <indireceiver.h>
#include <memory>

class BareReceiver : public INDI::Receiver
{
  public:
    BareReceiver() { setSensorConnection(CONNECTION_NONE); }
    const char *getDefaultName() override { return "Bare Receiver"; }
    bool initProperties() override
    {
        // The capabilities go after Receiver::initProperties for the
        // same reason as a CCD's: the stream manager copies the device
        // name and the driver interface when the capabilities are set.
        INDI::Receiver::initProperties();
        SetReceiverCapability(SENSOR_CAN_ABORT | SENSOR_HAS_STREAMING | SENSOR_HAS_SHUTTER | SENSOR_HAS_COOLER |
                              SENSOR_HAS_DSP);
        addAuxControls();
        return true;
    }
    bool Connect() override { return true; }
    bool Disconnect() override { return true; }
};

static std::unique_ptr<BareReceiver> device(new BareReceiver());
