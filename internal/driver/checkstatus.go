// -*- Mode: Go; indent-tabs-mode: t -*-
//
// Copyright (C) 2022 Intel Corporation
//
// SPDX-License-Identifier: Apache-2.0

package driver

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/edgexfoundry/device-sdk-go/v2/pkg/service"
	sdkModel "github.com/edgexfoundry/go-mod-core-contracts/v2/models"
)

// checkStatuses loops through all discovered and tries to determine the most accurate operating state
func (d *Driver) checkStatuses() {
	d.lc.Debug("checkStatuses has been called")
	for _, device := range service.RunningService().Devices() { // TODO: ensure this returns the proper value
		// "higher" degrees of connection are tested first, becuase if they
		// succeed, the "lower" levels of connection will too
		if device.Name == d.serviceName { // skip control plane device
			continue
		}
		status := Unreachable
		if d.testConnectionAuth(device) {
			status = UpWithAuth
		} else if d.testConnectionNoAuth(device) {
			status = UpWithoutAuth
		} else if d.httpProbe(device) {
			status = Reachable
		}

		if err := d.updateDeviceStatus(device, status); err != nil {
			d.lc.Warnf("Could not update device status for device %s: %s", device.Name, err.Error())
		}
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
	// // update entry in core metadata
	// err := d.updateDeviceStatus(device, UpWithAuth)
	// if err != nil {
	// 	d.lc.Warn("Could not update device status")
	// }
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

	// // update entry in core metadata
	// err := d.updateDeviceStatus(device, UpWithoutAuth)
	// if err != nil {
	// 	d.lc.Warn("Could not update device status")
	// }
	return true
}

// httpProbe attempts to make a connection to a specific ip and port list to determine
// if there is a service listening at that ip+port.
func (d *Driver) httpProbe(device sdkModel.Device) bool {
	addr := device.Protocols[OnvifProtocol][Address]
	port := device.Protocols[OnvifProtocol][Port]
	if addr == "" || port == "" {
		d.lc.Warnf("Device %s has no network address, cannot send probe.", device.Name)
		return false
	}
	host := addr + ":" + port

	net.DialTCP(host, nil, nil) // TODO: im
	// make http call to device
	_, err := http.Get(host)
	if err != nil {
		d.lc.Debugf("Connection to %s failed when using simple http request", device.Name)
		return false
	}

	// // update entry in core metadata
	// err = d.updateDeviceStatus(device, Reachable)
	// if err != nil {
	// 	d.lc.Warn("Could not update device status")
	// }
	return true
}

func (d *Driver) updateDeviceStatus(device sdkModel.Device, status string) error {
	device.Protocols[OnvifProtocol][DeviceStatus] = status

	if status != Unreachable {
		device.Protocols[OnvifProtocol][LastSeen] = time.Now().Format(time.UnixDate)
	}

	return service.RunningService().UpdateDevice(device)
}

// taskLoop is the driver for the checking the status at regular intervals
func (d *Driver) taskLoop(ctx context.Context) {
	interval := d.config.CheckStatusInterval
	if interval > maxStatusInterval { // TODO: Update with issue #75
		d.lc.Warnf("Status interval of %d seconds is larger than the maximum value of %d seconds. Status interval has been set to the max value.", interval, maxStatusInterval)
		interval = maxStatusInterval
	}
	// check the interval
	statusTicker := time.NewTicker(time.Duration(interval) * time.Second)

	defer func() {
		statusTicker.Stop()
	}()

	d.lc.Info("Starting task loop.")

	for {
		select {
		case <-ctx.Done():
			d.lc.Info("Task loop stopped.")
			return
		case <-statusTicker.C:
			start := time.Now()
			d.checkStatuses() // checks the status of every device
			d.lc.Debugf("Time elapsed for checkStatuses: %s", time.Since(start))
		}
	}
}

// RunUntilCancelled sets up the taskLoop and will continually run it until cancelled
func (d *Driver) StartTaskLoop() error {
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		d.taskLoop(ctx)
		d.lc.Info("Task loop has exited.")
	}()

	go func() {
		signals := make(chan os.Signal, 1) // TODO: determine if this should be 2
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
		s := <-signals
		d.lc.Infof("Received '%s' signal from OS.", s.String())
		cancel() // signal the taskLoop to finish
	}()
	return nil
}
