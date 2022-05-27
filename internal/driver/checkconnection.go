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

	"github.com/edgexfoundry/go-mod-core-contracts/v2/models"
)

// connect loops through all discovered and tries to determine the most accurate operating state
// test Auth mode first becuase that is expected use case
func (d *Driver) connect() {
	d.lc.Info("Connect has been called")
	devMap := d.makeDeviceMap()
	for _, dev := range devMap {
		okAuth := d.testConnectionAuth(dev)
		if okAuth {
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
		d.svc.UpdateDevice(dev)
	}
}

// testConnectionAuth will try to send a command to a camera using authentication
// and return a bool indicating success or failure
func (d *Driver) testConnectionAuth(dev models.Device) bool {
	_, edgexErr := d.getDeviceInformation(dev)
	if edgexErr != nil {
		d.lc.Debugf("Connection to %s failed when using authentication", dev.Name)
		return false
	} else {
		dev.Protocols[OnvifProtocol][DeviceStatus] = UpWithAuth
		dev.LastConnected = time.Now().Unix() // how to update this automatically
		d.svc.UpdateDevice(dev)
		return true
	}
}

// After failing to get a connection using authentication, it calls this function
// to try to reach the camera using a command that doesn't require authorization,
// and return a bool indicating success or failure
func (d *Driver) testConnectionNoAuth(dev models.Device) bool {
	_, edgexErr := d.newTemporaryOnvifClient(dev)
	if edgexErr != nil {
		d.lc.Debugf("Connection to %s failed when not using authentication", dev.Name)
		return false
	} else {
		dev.Protocols[OnvifProtocol][DeviceStatus] = UpWithoutAuth
		dev.LastConnected = time.Now().Unix() // how to update this automatically
		d.svc.UpdateDevice(dev)
		return true
	}
}

// probe attempts to make a connection to a specific ip and port list to determine
// if there is a service listening at that ip+port.
func (d *Driver) probe(dev models.Device, host string, port string) bool {
	addr := host + ":" + port
	_, err := http.Get(addr)
	if err != nil {
		d.lc.Debugf("Connection to %s failed when using simple http request", dev.Name)
	}
	dev.Protocols[OnvifProtocol][DeviceStatus] = Reachable
	dev.LastConnected = time.Now().Unix() // how to update this automatically
	d.svc.UpdateDevice(dev)
	return true
}

// taskLoop is our main event loop for async processes
// that can't be modeled within the SDK's pipeline event loop.
//
// Namely, it launches scheduled tasks and configuration changes.
// Since nearly every round through this loop must read or write the inventory,
// this taskLoop ensures the modifications are done safely
// without requiring a ton of lock contention on the inventory itself.
func (d *Driver) taskLoop(ctx context.Context) {
	interval := d.config.CheckConnectionInterval
	connectionTicker := time.NewTicker(time.Duration(interval) * time.Second)
	eventCh := make(chan bool)

	defer func() {
		connectionTicker.Stop()
	}()

	d.lc.Info("Starting task loop.")
	for {
		select {
		case <-ctx.Done():
			d.lc.Info("Stopping task loop.")
			close(eventCh)
			d.lc.Info("Task loop stopped.")
			return
		case <-connectionTicker.C:
			d.connect()
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
