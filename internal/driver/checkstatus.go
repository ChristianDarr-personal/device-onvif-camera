package driver

import (
	"net/http"
	"time"

	"github.com/edgexfoundry/device-sdk-go/v2/pkg/service"
	sdkModel "github.com/edgexfoundry/go-mod-core-contracts/v2/models"
)

// connect loops through all discovered and tries to determine the most accurate operating state
func (d *Driver) checkStatus() {
	d.lc.Info("checkStatus has been called")
	deviceMap := d.makeDeviceMap()
	for _, device := range deviceMap {
		// "higher" degrees of connection are tested first, becuase if they
		// succeed, the "lower" levels of connection will too
		if okAuth := d.testConnectionAuth(device); okAuth {
			continue
		}
		if okNoAuth := d.testConnectionNoAuth(device); okNoAuth {
			continue
		}
		if okProbe := d.httpProbe(device); okProbe {
			continue
		}

		// will only reach here if all other methods fail
		d.updateDeviceStatus(device, Unreachable)
	}
}

// testConnectionAuth will try to send a command to a camera using authentication
// and return a bool indicating success or failure
func (d *Driver) testConnectionAuth(device sdkModel.Device) bool {
	// sends get device information command to device (requires credentials)
	_, edgexErr := d.getDeviceInformation(device)
	if edgexErr != nil {
		d.lc.Debugf("Connection to %s failed when using authentication", device.Name)
		return false
	}
	// update entry in core metadata
	err := d.updateDeviceStatus(device, UpWithAuth)
	if err != nil {
		d.lc.Warn("Could not update device status")
	}
	return true
}

// After failing to get a connection using authentication, it calls this function
// to try to reach the camera using a command that doesn't require authorization,
// and return a bool indicating success or failure
func (d *Driver) testConnectionNoAuth(device sdkModel.Device) bool {
	// sends get capabilities command to device (does not require credentials)
	_, edgexErr := d.newTemporaryOnvifClient(device) // best way?
	if edgexErr != nil {
		d.lc.Debugf("Connection to %s failed when not using authentication", device.Name)
		return false
	}

	// update entry in core metadata
	err := d.updateDeviceStatus(device, UpWithoutAuth)
	if err != nil {
		d.lc.Warn("Could not update device status")
	}
	return true
}

// httpProbe attempts to make a connection to a specific ip and port list to determine
// if there is a service listening at that ip+port.
func (d *Driver) httpProbe(device sdkModel.Device) bool {
	addr := device.Protocols[OnvifProtocol][Address]
	port := device.Protocols[OnvifProtocol][Port]
	host := addr + ":" + port

	// make http call to device
	_, err := http.Get(host)
	if err != nil {
		d.lc.Debugf("Connection to %s failed when using simple http request", device.Name)
		return false
	}

	// update entry in core metadata
	err = d.updateDeviceStatus(device, Reachable)
	if err != nil {
		d.lc.Warn("Could not update device status")
	}
	return true
}

func (d *Driver) updateDeviceStatus(device sdkModel.Device, status string) error {
	device.Protocols[OnvifProtocol][DeviceStatus] = status

	var desc string
	var seen bool

	switch status {
	case UpWithAuth:
		desc = UpWithAuthDesc
		seen = true
	case UpWithoutAuth:
		desc = UpWithoutAuthDesc
		seen = true
	case Reachable:
		desc = ReachableDesc
		seen = true
	case Unreachable:
		desc = UnreachableDesc
	}

	device.Protocols[OnvifProtocol][DeviceStatusDescription] = desc

	if seen {
		device.Protocols[OnvifProtocol][LastSeen] = time.Now().Format(time.UnixDate)
	}

	err := service.RunningService().UpdateDevice(device)
	if err != nil {
		return err
	}
	return nil
}
