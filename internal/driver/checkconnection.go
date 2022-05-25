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
		errAuth := d.testConnectionAuth(dev)
		if errAuth == nil { // I feel like this is wrong
			continue
		}
		errNoAuth := d.testConnectionNoAuth(dev)
		if errNoAuth == nil {
			continue
		}
		d.probe(dev.Protocols["Onvif"]["Address"], dev.Protocols["Onvif"]["Port"])
	}
}

func (d *Driver) testConnectionAuth(dev models.Device) errors.EdgeX {
	_, edgexErr := d.getStreamUri(dev) // chose another function
	if edgexErr != nil {
		d.lc.Warnf("%s connected with authentication", dev.Name)
		// update "reachable"
		// update "control level"
	} else {
		dev.LastConnected = time.Now().Unix() // how to update this automatically
	}
	d.svc.UpdateDevice(dev)
	return errors.NewCommonEdgeXWrapper(edgexErr)
}

func (d *Driver) testConnectionNoAuth(dev models.Device) errors.EdgeX {
	_, edgexErr := d.getDeviceInformation(dev)
	if edgexErr != nil {
		d.lc.Warnf("%s connected without authentication", dev.Name)
		// update "reachable"
		// update "control level"
	} else {
		dev.LastConnected = time.Now().Unix() // how to update this automatically
	}
	d.svc.UpdateDevice(dev)
	return errors.NewCommonEdgeXWrapper(edgexErr)
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
func (d *Driver) probe(host string, port string) error {
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
		// update "reachable"
		// update "control level"
		params.Logger.Tracef(err.Error())
		return err
		// // EHOSTUNREACH specifies that the host is un-reachable or there is no route to host.
		// // EHOSTDOWN specifies that the network or host is down.
		// // If either of these are the error, do not bother probing the host any longer.
		// if netErrors.Is(err, syscall.EHOSTUNREACH) || netErrors.Is(err, syscall.EHOSTDOWN) {
		// 	// quit probing this host
		// 	return err
		// }
	} else {
		defer conn.Close()
	}
	return nil
}
