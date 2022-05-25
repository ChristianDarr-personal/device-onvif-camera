package driver

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/IOTechSystems/onvif"
	"github.com/IOTechSystems/onvif/device"
	"github.com/edgexfoundry/device-onvif-camera/pkg/netscan"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/errors"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/models"
)

func (d *Driver) connect() {
	devMap := d.makeDeviceMap()
	for _, dev := range devMap {
		okAuth := d.testConnectionAuth(dev)
		if okAuth { // I feel like this is wrong
			continue
		}
		okNoAuth := d.testConnectionNoAuth(dev)
		if okNoAuth {
			continue
		}
		okProbe := d.probe(dev, dev.Protocols[OnvifProtocol][Address], dev.Protocols[OnvifProtocol][Port])
		if okProbe {
			continue
		}
		dev.Protocols[OnvifProtocol][DeviceStatus] = Down

	}
}

func (d *Driver) testConnectionAuth(dev models.Device) bool {
	_, edgexErr := d.getStreamUri(dev) // chose another function
	if edgexErr != nil {
		d.lc.Warnf("%s did not connect with authentication", dev.Name)
		return false
	} else {
		dev.Protocols[OnvifProtocol][DeviceStatus] = UpWithAuth
		dev.LastConnected = time.Now().Unix() // how to update this automatically
		d.svc.UpdateDevice(dev)
		return true
	}
}

func (d *Driver) testConnectionNoAuth(dev models.Device) bool {
	_, edgexErr := d.getDeviceInformation(dev)
	if edgexErr != nil {
		d.lc.Warnf("%s did not connect without authentication", dev.Name) // TODO: better message
		return false
	} else {
		dev.Protocols[OnvifProtocol][DeviceStatus] = UpWithoutAuth
		dev.LastConnected = time.Now().Unix() // how to update this automatically
		d.svc.UpdateDevice(dev)
		return true
	}
}

func (d *Driver) getStreamUri(dev models.Device) (devInfo *device.GetDeviceInformationResponse, edgexErr errors.EdgeX) { //choose proper func later
	devClient, edgexErr := d.newTemporaryOnvifClient(dev)
	if edgexErr != nil {
		return nil, errors.NewCommonEdgeXWrapper(edgexErr)
	}
	devInfoResponse, edgexErr := devClient.callOnvifFunction(onvif.DeviceWebService, onvif.GetStreamUri, []byte{})
	if edgexErr != nil {
		return nil, errors.NewCommonEdgeXWrapper(edgexErr)
	}
	devInfo, ok := devInfoResponse.(*device.GetDeviceInformationResponse)
	if !ok {
		return nil, errors.NewCommonEdgeX(errors.KindServerError, fmt.Sprintf("invalid GetStreamUri for the camera %s", dev.Name), nil)
	}
	return devInfo, nil
}

// probe attempts to make a connection to a specific ip and port list to determine
// if there is a service listening at that ip+port.
func (d *Driver) probe(dev models.Device, host string, port string) bool {
	addr := host + ":" + port
	params := netscan.Params{
		// split the comma separated string here to avoid issues with EdgeX's Consul implementation
		Subnets:         strings.Split(d.config.DiscoverySubnets, ","),
		AsyncLimit:      d.config.ProbeAsyncLimit,
		Timeout:         time.Duration(d.config.ProbeTimeoutMillis) * time.Millisecond,
		ScanPorts:       []string{wsDiscoveryPort},
		Logger:          d.lc,
		NetworkProtocol: netscan.NetworkUDP,
	}
	params.Logger.Tracef("Dial: %s", addr)
	conn, err := net.DialTimeout(params.NetworkProtocol, addr, params.Timeout)
	if err != nil {
		params.Logger.Tracef(err.Error())
		return false
		// // EHOSTUNREACH specifies that the host is un-reachable or there is no route to host.
		// // EHOSTDOWN specifies that the network or host is down.
		// // If either of these are the error, do not bother probing the host any longer.
		// if netErrors.Is(err, syscall.EHOSTUNREACH) || netErrors.Is(err, syscall.EHOSTDOWN) {
		// 	// quit probing this host
		// 	return err
		// }
	} else {
		dev.Protocols[OnvifProtocol][DeviceStatus] = Reachable
		dev.LastConnected = time.Now().Unix() // how to update this automatically
		d.svc.UpdateDevice(dev)
		defer conn.Close()
		return true
	}
}
