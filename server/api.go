package main

import (
	"fmt"
	"net/http"
	"strings"

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

	_ = &UserInfo{
		UserID: userID,
		Token:  token,
	}
	if err != nil {
		fmt.Printf("oauthConf.Exchange() failed with '%s'\n", err)
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}

	message := "Welcome to Google Calendar Plugin"
	p.createBotDMPost(userID, message)

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
