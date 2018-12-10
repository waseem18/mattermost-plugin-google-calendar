package main

import (
	"fmt"
	"reflect"

	"github.com/pkg/errors"
)

// configuration captures the plugin's external configuration as exposed
// in Mattermost server configuration.
type configuration struct {
	BotUserID                 string
	Username                  string
	CalendarOAuthClientID     string
	CalendarOAuthClientSecret string
	Secret                    string
}

// IsValid validates if all the required fields are set.
func (c *configuration) IsValid() error {
	if c.Username == "" {
		return fmt.Errorf("Need a user to make posts as")
	}

	if c.CalendarOAuthClientID == "" {
		return fmt.Errorf("Must have Google Calendar oauth client id")
	}

	if c.CalendarOAuthClientSecret == "" {
		return fmt.Errorf("Must have Google Calendar oauth client secret")
	}

	if c.Secret == "" {
		return fmt.Errorf("Must have secret key")
	}

	return nil
}

// getConfiguration retrieves the active configuration under lock, making it safe to use
// concurrently. The active configuration may change underneath the client of this method, but
// the struct returned by this API call is considered immutable.
func (p *Plugin) getConfiguration() *configuration {
	p.configurationLock.RLock()
	defer p.configurationLock.RUnlock()

	if p.configuration == nil {
		return &configuration{}
	}

	return p.configuration
}

// setConfiguration replaces the active configuration under lock.
//
// Do not call setConfiguration while holding the configurationLock, as sync.Mutex is not
// reentrant. In particular, avoid using the plugin API entirely, as this may in turn trigger a
// hook back into the plugin. If that hook attempts to acquire this lock, a deadlock may occur.
//
// This method panics if setConfiguration is called with the existing configuration. This almost
// certainly means that the configuration was modified without being cloned and may result in
// an unsafe access.
func (p *Plugin) setConfiguration(configuration *configuration) {
	p.configurationLock.Lock()
	defer p.configurationLock.Unlock()

	if configuration != nil && p.configuration == configuration {
		// Ignore assignment if the configuration struct is empty. Go will optimize the
		// allocation for same to point at the same memory address, breaking the check
		// above.
		if reflect.ValueOf(*configuration).NumField() == 0 {
			return
		}

		panic("setConfiguration called with the existing configuration")
	}

	p.configuration = configuration
}

// OnConfigurationChange is invoked when configuration changes may have been made.
func (p *Plugin) OnConfigurationChange() error {
	var configuration = new(configuration)

	// Load the public configuration fields from the Mattermost server configuration.
	if err := p.API.LoadPluginConfiguration(configuration); err != nil {
		return errors.Wrap(err, "failed to load plugin configuration")
	}

	p.setConfiguration(configuration)

	return nil
}
