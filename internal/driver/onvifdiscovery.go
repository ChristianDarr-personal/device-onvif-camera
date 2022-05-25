// -*- Mode: Go; indent-tabs-mode: t -*-
//
// Copyright (C) 2022 Intel Corporation
//
// SPDX-License-Identifier: Apache-2.0

package driver

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/IOTechSystems/onvif"
	wsdiscovery "github.com/IOTechSystems/onvif/ws-discovery"
	"github.com/edgexfoundry/device-onvif-camera/pkg/netscan"
	sdkModel "github.com/edgexfoundry/device-sdk-go/v2/pkg/models"
	contract "github.com/edgexfoundry/go-mod-core-contracts/v2/models"
	"github.com/gofrs/uuid"
	"github.com/pkg/errors"
)

const (
	bufSize = 8192
)

// OnvifProtocolDiscovery implements netscan.ProtocolSpecificDiscovery
type OnvifProtocolDiscovery struct {
	driver *Driver
}

func NewOnvifProtocolDiscovery(driver *Driver) *OnvifProtocolDiscovery {
	return &OnvifProtocolDiscovery{driver: driver}
}

// ProbeFilter takes in a host and a slice of ports to be scanned. It should return a slice
// of ports to actually scan, or a nil/empty slice if the host is to not be scanned at all.
// Can be used to filter out known devices from being probed again if required.
func (proto *OnvifProtocolDiscovery) ProbeFilter(_ string, ports []string) []string {
	// For onvif we do not want to do any filtering
	return ports
}

// OnConnectionDialed handles the protocol specific verification if there is actually
// a valid device or devices at the other end of the connection.
func (proto *OnvifProtocolDiscovery) OnConnectionDialed(host string, port string, conn net.Conn, params netscan.Params) ([]netscan.ProbeResult, error) {
	// attempt a basic direct probe approach using the open connection
	devices, err := executeRawProbe(conn, params)
	if err != nil {
		params.Logger.Debugf(err.Error())
	} else if len(devices) > 0 {
		return mapProbeResults(host, port, devices), nil
	}
	return nil, err
}

// ConvertProbeResult takes a raw ProbeResult and transforms it into a
// processed DiscoveredDevice struct.
func (proto *OnvifProtocolDiscovery) ConvertProbeResult(probeResult netscan.ProbeResult, params netscan.Params) (netscan.DiscoveredDevice, error) {
	onvifDevice, ok := probeResult.Data.(onvif.Device)
	if !ok {
		return netscan.DiscoveredDevice{}, fmt.Errorf("unable to cast probe result into onvif.Device. type=%T", probeResult.Data)
	}

	discovered, err := proto.driver.createDiscoveredDevice(onvifDevice)
	if err != nil {
		return netscan.DiscoveredDevice{}, err
	}

	return netscan.DiscoveredDevice{
		Name: discovered.Name,
		Info: discovered,
	}, nil
}

// createDiscoveredDevice will take an onvif.Device that was detected on the network and
// attempt to get more information about the device and create an EdgeX compatible DiscoveredDevice.
func (d *Driver) createDiscoveredDevice(onvifDevice onvif.Device) (sdkModel.DiscoveredDevice, error) {
	xaddr := onvifDevice.GetDeviceParams().Xaddr
	endpointRefAddr := onvifDevice.GetDeviceParams().EndpointRefAddress
	if endpointRefAddr == "" {
		d.lc.Warnf("The EndpointRefAddress is empty from the Onvif camera, unable to add the camera %s", xaddr)
		return sdkModel.DiscoveredDevice{}, fmt.Errorf("empty EndpointRefAddress for XAddr %s", xaddr)
	}
	address, port := addressAndPort(xaddr)
	dev := contract.Device{
		// Using Xaddr as the temporary name
		Name: xaddr,
		Protocols: map[string]contract.ProtocolProperties{
			OnvifProtocol: {
				Address:            address,
				Port:               port,
				AuthMode:           d.config.DefaultAuthMode,
				SecretPath:         d.config.DefaultSecretPath,
				EndpointRefAddress: endpointRefAddr,
			},
		},
	}

	devInfo, edgexErr := d.getDeviceInformation(dev)
	if edgexErr != nil {
		// try again using the device name as the SecretPath
		dev.Protocols[OnvifProtocol][SecretPath] = endpointRefAddr
		devInfo, edgexErr = d.getDeviceInformation(dev)
	}

	var discovered sdkModel.DiscoveredDevice
	if edgexErr != nil {
		d.lc.Warnf("failed to get the device information for the camera %s, %v", endpointRefAddr, edgexErr)
		discovered = sdkModel.DiscoveredDevice{
			Name:        endpointRefAddr,
			Protocols:   dev.Protocols,
			Description: "Auto discovered Onvif camera",
			Labels:      []string{"auto-discovery"},
		}
		d.lc.Debugf("Discovered unknown camera from the address '%s'", xaddr)
	} else {
		dev.Protocols[OnvifProtocol][Manufacturer] = devInfo.Manufacturer
		dev.Protocols[OnvifProtocol][Model] = devInfo.Model
		dev.Protocols[OnvifProtocol][FirmwareVersion] = devInfo.FirmwareVersion
		dev.Protocols[OnvifProtocol][SerialNumber] = devInfo.SerialNumber
		dev.Protocols[OnvifProtocol][HardwareId] = devInfo.HardwareId

		// Spaces are not allowed in the device name
		deviceName := fmt.Sprintf("%s-%s-%s",
			strings.ReplaceAll(devInfo.Manufacturer, " ", "-"),
			strings.ReplaceAll(devInfo.Model, " ", "-"),
			endpointRefAddr)

		discovered = sdkModel.DiscoveredDevice{
			Name:        deviceName,
			Protocols:   dev.Protocols,
			Description: fmt.Sprintf("%s %s Camera", devInfo.Manufacturer, devInfo.Model),
			Labels:      []string{"auto-discovery", devInfo.Manufacturer, devInfo.Model},
		}
		d.lc.Debugf("Discovered camera from the address '%s'", xaddr)
	}
	return discovered, nil
}

// mapProbeResults converts a slice of discovered onvif.Device into the generic
// netscan.ProbeResult.
func mapProbeResults(host, port string, devices []onvif.Device) (res []netscan.ProbeResult) {
	for _, dev := range devices {
		res = append(res, netscan.ProbeResult{
			Host: host,
			Port: port,
			Data: dev,
		})
	}
	return res
}

// executeRawProbe essentially performs a UDP unicast ws-discovery probe by sending the
// probe message directly over the connection and listening for any responses. Those
// responses are then converted into a slice of onvif.Device.
func executeRawProbe(conn net.Conn, params netscan.Params) ([]onvif.Device, error) {
	probeSOAP := wsdiscovery.BuildProbeMessage(uuid.Must(uuid.NewV4()).String(), nil, nil,
		map[string]string{"dn": "http://www.onvif.org/ver10/network/wsdl"})

	addr := conn.RemoteAddr().String()
	if err := conn.SetDeadline(time.Now().Add(params.Timeout)); err != nil {
		return nil, errors.Wrapf(err, "%s: failed to set read/write deadline", addr)
	}

	if _, err := conn.Write([]byte(probeSOAP.String())); err != nil {
		return nil, errors.Wrap(err, "failed to write probe message")
	}

	var responses []string
	buf := make([]byte, bufSize)
	// keep reading from the PacketConn until the read deadline expires or an error occurs
	for {
		n, _, err := (conn.(net.PacketConn)).ReadFrom(buf)
		if err != nil {
			// ErrDeadlineExceeded is expected once the read timeout is expired
			if !errors.Is(err, os.ErrDeadlineExceeded) {
				params.Logger.Debugf("Unexpected error occurred while reading ws-discovery responses: %s", err.Error())
			}
			break
		}
		responses = append(responses, string(buf[0:n]))
	}

	if len(responses) == 0 {
		params.Logger.Tracef("%s: No Response", addr)
		return nil, nil
	}
	for i, resp := range responses {
		params.Logger.Debugf("%s: Response %d of %d: %s", addr, i+1, len(responses), resp)
	}

	devices, err := wsdiscovery.DevicesFromProbeResponses(responses)
	if err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		params.Logger.Debugf("%s: no devices matched from probe response", addr)
		return nil, nil
	}

	return devices, nil
}

// makeDeviceMap creates a lookup table of existing devices by EndpointRefAddress
func (d *Driver) makeDeviceMap() map[string]contract.Device {
	devices := d.svc.Devices()
	deviceMap := make(map[string]contract.Device, len(devices))

	for _, dev := range devices {
		if dev.Name == d.serviceName {
			// skip control plane device
			continue
		}

		onvifInfo := dev.Protocols[OnvifProtocol]
		if onvifInfo == nil {
			d.lc.Warnf("Found registered device %s without %s protocol information.", dev.Name, OnvifProtocol)
			continue
		}

		endpointRef := onvifInfo["EndpointRefAddress"]
		if endpointRef == "" {
			d.lc.Warnf("Registered device %s is missing required %s protocol information: EndpointRefAddress.",
				dev.Name, OnvifProtocol)
			continue
		}

		deviceMap[endpointRef] = dev
	}

	return deviceMap
}

// discoverFilter iterates through the discovered devices, and returns any that are not duplicates
// of devices in metadata or are from an alternate discovery method
// will return an empty slice if no new devices are discovered
func (d *Driver) discoverFilter(discovered []sdkModel.DiscoveredDevice) (filtered []sdkModel.DiscoveredDevice) {
	devMap := d.makeDeviceMap() // create comparison map
	checked := make(map[string]bool)
	for _, dev := range discovered {
		endRef := dev.Protocols["Onvif"]["EndpointRefAddress"]
		_, prevDisc := checked[endRef]
		if !prevDisc {
			if metaDev, found := devMap[endRef]; found {
				// if device is already in metadata, update it if necessary
				checked[endRef] = true
				d.updateExistingDevice(metaDev, dev)
			} else {
				duplicate := false
				// check if a matching device was discovered by another method
				for _, filterDev := range filtered {
					if endRef == filterDev.Protocols["Onvif"]["EndpointRefAddress"] {
						duplicate = true
						break
					}
				}
				// if not a part of metadata or not discovered by another method, send to EdgeX
				if !duplicate {
					filtered = append(filtered, dev) // send new device to edgex if there is no existing match
				}
			}
		}
	}
	return filtered
}

// // checkConnection compares all existing devices and searches for a matching discovered device
// // it updates all disconnected devices with its status
// func (d *Driver) checkConnection(discovered []sdkModel.DiscoveredDevice) {
// 	devMap := d.makeDeviceMap() // create comparison map
// 	var connected bool
// 	for name, dev := range devMap {
// 		connected = false
// 		for _, discDev := range discovered {
// 			if discDev.Protocols["Onvif"]["EndpointRefAddress"] == name {
// 				connected = true
// 				dev.LastConnected = time.Now().Unix()
// 				break
// 			}
// 		}
// 		if !connected {
// 			elapsed := time.Now().Unix() - dev.LastConnected
// 			if elapsed > 200 {
// 				// Decommissioned
// 			} else {
// 				// Maintenance
// 			}
// 			dev.OperatingState = contract.Down
// 			d.svc.UpdateDevice(dev)
// 		}
// 	}
// }
