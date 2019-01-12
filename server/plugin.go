package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost-server/mlog"
	"github.com/mattermost/mattermost-server/model"
	"github.com/mattermost/mattermost-server/plugin"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
)

const (
	CalendarIconURL string = "plugins/google-calendar/Google_Calendar_Logo.png"
	BotUsername     string = "Calendar Plugin"
	postPretext     string = "Event starting in 10 min"
)

type Plugin struct {
	plugin.MattermostPlugin

	// configurationLock synchronizes access to the configuration.
	configurationLock sync.RWMutex

	// configuration is the active plugin configuration. Consult getConfiguration and
	// setConfiguration for usage.
	configuration *configuration

	BotUserID string
}

// UserInfo captures the UserID and authentication token of a user.
type UserInfo struct {
	UserID string
	Token  *oauth2.Token
}

// CalendarInfo captures the list of events and details of the last event update.
type CalendarInfo struct {
	LastEventUpdate string
	Events          []*EventInfo
}

// EventInfo captures some of the attributes of a Calendar event.
type EventInfo struct {
	Id        string
	HtmlLink  string
	StartTime string
	EndTime   string
	Summary   string
	Status    string
}

// OnActivate is triggered as soon as the plugin is enabled.
func (p *Plugin) OnActivate() error {
	config := p.getConfiguration()

	if err := config.IsValid(); err != nil {
		return err
	}

	p.API.RegisterCommand(getCommand())

	user, err := p.API.GetUserByUsername(config.Username)
	if err != nil {
		mlog.Error(err.Error())
		return fmt.Errorf("Unable to find user with configured username: %v", config.Username)
	}

	p.BotUserID = user.Id

	return nil
}

func (p *Plugin) getOAuthConfig() *oauth2.Config {
	pluginConfig := p.getConfiguration()
	config := p.API.GetConfig()

	return &oauth2.Config{
		ClientID:     pluginConfig.CalendarOAuthClientID,
		ClientSecret: pluginConfig.CalendarOAuthClientSecret,
		RedirectURL:  fmt.Sprintf("%s/plugins/google-calendar/oauth/complete", *config.ServiceSettings.SiteURL),
		Scopes:       []string{"https://www.googleapis.com/auth/calendar.readonly", "https://www.googleapis.com/auth/calendar.events.readonly"},
		Endpoint:     google.Endpoint,
	}
}

func (p *Plugin) ExecuteCommand(c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	split := strings.Fields(args.Command)
	command := split[0]
	action := ""
	if len(split) > 1 {
		action = split[1]
	}

	if command != "/google-calendar" {
		return &model.CommandResponse{}, nil
	}

	if action == "connect" {
		config := p.API.GetConfig()
		if config.ServiceSettings.SiteURL == nil {
			return getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, "Encountered an error connecting to Google Calendar."), nil
		}
		resp := getCommandResponse(model.COMMAND_RESPONSE_TYPE_EPHEMERAL, fmt.Sprintf("[Click here to link your Google Calendar.](%s/plugins/google-calendar/oauth/connect)", *config.ServiceSettings.SiteURL))
		return resp, nil
	}

	return &model.CommandResponse{}, nil
}

func (p *Plugin) createBotDMPost(userID, message string) *model.AppError {
	channel, err := p.API.GetDirectChannel(userID, p.BotUserID)
	if err != nil {
		mlog.Error("Couldn't get bot's DM channel", mlog.String("user_id", userID))
		return err
	}

	post := &model.Post{
		UserId:    p.BotUserID,
		ChannelId: channel.Id,
		Message:   message,
		Type:      "custom_git_welcome",
		Props: map[string]interface{}{
			"from_webhook":      "true",
			"override_username": BotUsername,
			"override_icon_url": CalendarIconURL,
		},
	}

	if _, err := p.API.CreatePost(post); err != nil {
		mlog.Error(err.Error())
		return err
	}

	return nil
}

// createCalendarService initialises and returns a Google Calendar service
func (u *UserInfo) createCalendarService() *calendar.Service {
	client := oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(u.Token))
	calendarService, err := calendar.New(client)
	if err != nil {
		mlog.Error(err.Error())
		return nil
	}
	return calendarService
}

func (p *Plugin) subscribeToCalendar(u *UserInfo, c *CalendarInfo) {
	calendarService := u.createCalendarService()
	c.fetchEventsFromCalendar(calendarService)
}

func (c *CalendarInfo) fetchEventsFromCalendar(calendarService *calendar.Service) {
	calendarEvents, err := calendarService.Events.List("primary").TimeMin(time.Now().Format(time.RFC3339)).TimeMax(time.Now().Add(time.Hour * 1).Format(time.RFC3339)).Do()
	if err != nil {
		mlog.Error(err.Error())
		return
	}

	c.LastEventUpdate = time.Now().Format(time.RFC3339)

	if len(calendarEvents.Items) > 0 {
		for _, event := range calendarEvents.Items {
			c.Events = append(c.Events, &EventInfo{
				Id:        event.Id,
				HtmlLink:  event.HtmlLink,
				StartTime: formatTime(event.Start.DateTime),
				EndTime:   formatTime(event.End.DateTime),
				Summary:   event.Summary,
				Status:    event.Status,
			})
		}
	}
}
