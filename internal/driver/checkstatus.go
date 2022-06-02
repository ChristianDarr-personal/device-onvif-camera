package driver

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
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

// taskLoop is our main event loop for async processes
// that can't be modeled within the SDK's pipeline event loop.
//
// Namely, it launches scheduled tasks and configuration changes.
// Since nearly every round through this loop must read or write the inventory,
// this taskLoop ensures the modifications are done safely
// without requiring a ton of lock contention on the inventory itself.
func (d *Driver) taskLoop(ctx context.Context) {
	interval := d.config.CheckStatusInterval
	if interval > maxStatusInterval {
		d.lc.Warnf("Status interval of %d seconds is larger than the maximum value of %d seconds. Status interval has been set to the max value.", interval, maxStatusInterval)
		interval = maxStatusInterval
	}
	// check the interval
	statusTicker := time.NewTicker(time.Duration(interval) * time.Second)
	eventCh := make(chan bool)

	defer func() {
		statusTicker.Stop()
	}()

	d.lc.Info("Starting task loop.")

	for {
		select {
		case <-ctx.Done():
			d.lc.Info("Stopping task loop.")
			close(eventCh)
			d.lc.Info("Task loop stopped.")
			return
		case <-statusTicker.C:
			start := time.Now()
			d.checkStatus() // checks the status of every device
			fmt.Println(time.Since(start))
		}
	}
}

// RunUntilCancelled sets up the function pipeline and runs it. This function will not return
// until the function pipeline is complete unless an error occurred running it.
func (d *Driver) RunUntilCancelled() error {
	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.taskLoop(ctx)
		d.lc.Info("Task loop has exited.")
	}()

	go func() {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
		s := <-signals
		d.lc.Info(fmt.Sprintf("Received '%s' signal from OS.", s.String()))
		cancel() // signal the taskLoop to finish
	}()
	return nil
}
