//
// Copyright (C) 2022 Intel Corporation
//
// SPDX-License-Identifier: Apache-2.0

package driver

import (
	"errors"

	"github.com/edgexfoundry/device-sdk-go/v2/pkg/service"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/logger"
	contract "github.com/edgexfoundry/go-mod-core-contracts/v2/models"
)

// ServiceWrapper wraps an EdgeX SDK service so it can be easily mocked in tests.
type ServiceWrapper interface {
	Devices() []contract.Device
	GetDeviceByName(name string) (contract.Device, error)
	UpdateDevice(device contract.Device) error
	UpdateDeviceOperatingState(deviceName string, state string) error
	GetProvisionWatcherByName(name string) (contract.ProvisionWatcher, error)
	AddProvisionWatcher(watcher contract.ProvisionWatcher) (id string, err error)
	AddDevice(device contract.Device) (id string, err error)
	LoadCustomConfig(customConfig service.UpdatableConfig, sectionName string) error
	ListenForCustomConfigChanges(configToWatch interface{}, sectionName string, changedCallback func(interface{})) error

	DriverConfigs() map[string]string
}

type DeviceSDKService struct {
	*service.DeviceService
	lc logger.LoggingClient
}

// DriverConfigs retrieves the driver specific configuration
func (s *DeviceSDKService) DriverConfigs() map[string]string {
	return service.DriverConfigs()
}

func (s *DeviceSDKService) SetDeviceStatus(name string, status string) error {
	// workaround: the device-service-sdk's uses core-contracts for the API URLs,
	// but the metadata service API for opstate updates changed between v1.1.0 and v1.2.0.
	d, err := s.GetDeviceByName(name)
	if err != nil {
		return errors.New("no such device")
	}

	d.Protocols[OnvifProtocol][DeviceStatus] = status
	return s.UpdateDevice(d)
}
