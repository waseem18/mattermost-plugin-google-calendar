# Mattermost Google Calendar plugin

This plugin uses webhooks to post reminders from configured Google Calendar to your Mattermost channel.

# Development status

Initial stage

# Installation

Go to the GitHub releases tab and download the latest release for your server architecture. You can upload this file in the Mattermost system console to install the plugin.


Build your plugin:
```
make
```

This will produce a single plugin file (with support for multiple architectures) for upload to your Mattermost server:

```
dist/my-plugin.tar.gz
```
