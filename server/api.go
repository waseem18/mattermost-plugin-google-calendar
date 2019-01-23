package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost-server/model"
	"github.com/mattermost/mattermost-server/plugin"
	"golang.org/x/oauth2"
)

const API_ERROR_ID_NOT_CONNECTED = "not_connected"

type APIErrorResponse struct {
	ID         string `json:"id"`
	Message    string `json:"message"`
	StatusCode int    `json:"status_code"`
}

func writeAPIError(w http.ResponseWriter, err *APIErrorResponse) {
	b, _ := json.Marshal(err)
	w.WriteHeader(err.StatusCode)
	w.Write(b)
}

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

	url := googleOauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline)

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
	token, err := googleOauthConfig.Exchange(oauth2.NoContext, code)

	if err != nil {
		fmt.Printf("oauthConf.Exchange() failed with '%s'\n", err)
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}

	userInfo := &UserInfo{
		UserID: userID,
		Token:  token,
	}

	p.storeUserInfo(userInfo)

	p.getDirectChannel(userID)

	var calendarInfo CalendarInfo

	p.storeCalendarInfo(userID, &calendarInfo)

	p.subscribeToCalendar(userInfo)

	message := "Welcome to Google Calendar Plugin"
	p.createBotDMPost(message)

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
	userID := r.URL.Query().Get("userID")
	userInfo, _ := p.getUserInfo(userID)
	p.fetchEventsFromCalendar(userInfo)
}
