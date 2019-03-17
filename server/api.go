package main

import (
	"fmt"
	"google.golang.org/api/calendar/v3"
	"net/http"
	"strings"

	"context"
	"github.com/mattermost/mattermost-server/mlog"
	"github.com/mattermost/mattermost-server/model"
	"github.com/mattermost/mattermost-server/plugin"
	"golang.org/x/oauth2"
)

func (p *Plugin) ServeHTTP(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch path := r.URL.Path; path {
	case "/oauth/connect":
		p.connectUserToGoogleCalendar(w, r)
	case "/oauth/complete":
		p.completeGoogleCalendarOauth(w, r)
	case "/watch":
		p.watchGoogleCalendar(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (p *Plugin) connectUserToGoogleCalendar(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("Mattermost-User-ID")
	if userID == "" {
		http.Error(w, "Not authorized", http.StatusUnauthorized)
		return
	}

	state := fmt.Sprintf("%v_%v", model.NewId()[10], userID)

	p.API.KVSet(state, []byte(state))

	googleOauthConfig := p.getOAuthConfig()

	url := googleOauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)

	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func (p *Plugin) completeGoogleCalendarOauth(w http.ResponseWriter, r *http.Request) {
	state := r.FormValue("state")
	storedState, _ := p.API.KVGet(state)

	if string(storedState) != state {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	userID := strings.Split(state, "_")[1]

	code := r.FormValue("code")
	googleOauthConfig := p.getOAuthConfig()
	token, err := googleOauthConfig.Exchange(context.TODO(), code)

	if !token.Valid() {
		fmt.Fprintln(w, "Retreived invalid token")
		return
	}

	if err != nil {
		mlog.Error("oauthConf.Exchange() failed with" + err.Error())
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}

	userInfo := &UserInfo{
		UserID: userID,
		Token:  token,
	}

	userInfo.ChannelID, _ = p.getDirectChannel(userInfo)

	p.storeUserInfo(userInfo)

	var calendarInfo CalendarInfo

	p.storeCalendarInfo(userID, &calendarInfo)

	p.subscribeToCalendar(userInfo)

	p.createBotDMPost(userInfo)

	html := `
<!DOCTYPE html>
<html>
	<head>
		<script>
			window.close();
		</script>
	</head>
	<body>
		<p>Completed connecting to Google Calendar. Please close this window.</p>
	</body>
</html>
`

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func (p *Plugin) watchGoogleCalendar(w http.ResponseWriter, r *http.Request) {
	channelID := r.Header.Get("X-Goog-Channel-ID")
	resourceID := r.Header.Get("X-Goog-Resource-ID")
	state := r.Header.Get("X-Goog-Resource-State")

	userID := r.URL.Query().Get("userID")
	userInfo, _ := p.getUserInfo(userID)
	calendarInfo, _ := p.getCalendarInfo(userID)

	if calendarInfo.CalendarWatchToken == channelID && state == "exists" {
		_ = p.updateCalendarEvents(userInfo, calendarInfo)
	} else {
		calendarService, _ := p.createCalendarService(userInfo)
		stopChannel := calendarService.Channels.Stop(&calendar.Channel{Id: channelID, ResourceId: resourceID})
		stopChannel.Do()
	}
}
