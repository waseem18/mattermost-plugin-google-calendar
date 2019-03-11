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

# Local setup

1. Clone the repo and make sure `mattermost server` is up and running.
2. Use `ngrok` or any other tunnel provider to expose the mattermost server port (8065) to Internet. The command to create a tunnel is `ngrok http 8065`.
3. Above command provides a URL accessible from Internet. In `plugin.go` in [line #91](https://github.com/waseem18/mattermost-plugin-google-calendar/blob/master/server/plugin.go#L91), replace `p.API.GetConfig()` with the URL.
4. In the same method, in [line #96](https://github.com/waseem18/mattermost-plugin-google-calendar/blob/master/server/plugin.go#L96), replace `*config.ServiceSettings.SiteURL` with `config`.
5. Follow point 4 again in the lines [line #202](https://github.com/waseem18/mattermost-plugin-google-calendar/blob/master/server/plugin.go#L202) and [line #207](https://github.com/waseem18/mattermost-plugin-google-calendar/blob/master/server/plugin.go#L202).
6. Login to [Google Cloud Console](https://console.cloud.google.com) and create a new project.
7. Go to [API library](https://console.cloud.google.com/apis/library) and make sure Google Calendar API is enabled.
8. Go to [API and Services](https://console.cloud.google.com/apis/dashboard) and select `Credentials` tab from the left menu.
9. Now click on `Create Credentials` dropdown and select `Oauth client ID` option.
10. While creating the Oauth credentials, enter the values of `Authorized Javascript Origins` as `http://localhost:8065` and the value of `Authorised redirect URIs` as `http://localhost:8064/plugins/google-calendar/oauth/complete`.
11. After creating the Oauth client, copy the Client ID and secret.
12. Upload the plugin to Mattermost and go to `Google Calendar Plugin settings`. Paste the client id and secret and select a user for the plugin to post event messages with.
13. Enable the plugin and you should be able to see event reminder notifications.
