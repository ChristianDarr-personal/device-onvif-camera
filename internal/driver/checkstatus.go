package driver

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
