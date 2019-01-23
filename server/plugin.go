package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mattermost/mattermost-server/mlog"
	"github.com/mattermost/mattermost-server/model"
	"github.com/mattermost/mattermost-server/plugin"
	"github.com/robfig/cron"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
)

const (
	USER_TOKEN_KEY            = "_usertoken"
	CALENDAR_TOKEN_KEY        = "_calendartoken"
	CalendarIconURL    string = "plugins/google-calendar/Google_Calendar_Logo.png"
	BotUsername        string = "Calendar Plugin"
	postPretext        string = "Event starting in 10 min"
)

type Plugin struct {
	plugin.MattermostPlugin

	// configurationLock synchronizes access to the configuration.
	configurationLock sync.RWMutex

	// configuration is the active plugin configuration. Consult getConfiguration and
	// setConfiguration for usage.
	configuration *configuration

	BotUserID string

	ChannelID string
}

// UserInfo captures the UserID and authentication token of a user.
type UserInfo struct {
	UserID string
	Token  *oauth2.Token
}

// CalendarInfo captures the list of events and details of the last event update.
type CalendarInfo struct {
	LastEventUpdate     string
	Events              []EventInfo
	CalendarWatchToken  string
	CalendarWatchExpiry int64
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

func (p *Plugin) createBotDMPost(message string) *model.AppError {
	post := &model.Post{
		UserId:    p.BotUserID,
		ChannelId: p.ChannelID,
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

func (p *Plugin) getDirectChannel(userID string) {
	channel, err := p.API.GetDirectChannel(userID, p.BotUserID)
	if err != nil {
		mlog.Error("Couldn't get bot's DM channel", mlog.String("user_id", userID))
		return
	}
	p.ChannelID = channel.Id
}

func (p *Plugin) createAPostForEvent(e EventInfo) {
	event := generateSlackAttachment(e)

	p.API.CreatePost(&model.Post{
		ChannelId: p.ChannelID,
		Type:      model.POST_SLACK_ATTACHMENT,
		UserId:    p.BotUserID,
		Props: map[string]interface{}{
			"from_webhook":  "true",
			"use_user_icon": "true",
			"attachments":   []*model.SlackAttachment{event},
		},
	})
}

// createCalendarService initialises and returns a Google Calendar service
func createCalendarService(token *oauth2.Token) *calendar.Service {
	client := oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(token))
	calendarService, err := calendar.New(client)
	if err != nil {
		mlog.Error(err.Error())
		return nil
	}
	return calendarService
}

func (p *Plugin) subscribeToCalendar(u *UserInfo) {
	p.fetchEventsFromCalendar(u)

	calendarInfo, _ := p.getCalendarInfo(u.UserID)
	p.setupCalendarWatchService(u, calendarInfo)

	cron := cron.New()
	cron.AddFunc("@every 1m", func() {
		// checkEvents method gets called every minute to check if the
		// start time of any event is after 10 min.
		p.checkEvents(u.UserID)
	})
	cron.Start()
}

func (p *Plugin) setupCalendarWatchService(u *UserInfo, c *CalendarInfo) {
	calendarService := createCalendarService(u.Token)

	config := p.API.GetConfig()

	var id = uuid.New().String()

	eventsWatchCall := calendarService.Events.Watch("primary", &calendar.Channel{
		Address: fmt.Sprintf("%s/plugins/google-calendar/watch?userID=%s", *config.ServiceSettings.SiteURL, u.UserID),
		Id:      id,
		Type:    "web_hook",
	})

	ch, err := eventsWatchCall.Do()
	if err != nil {
		mlog.Error(err.Error())
	}

	c.CalendarWatchToken = id
	c.CalendarWatchExpiry = ch.Expiration
}

// fetchEventsFromCalendar fetches the events set for the next one hour
// and feeds them into the Events attribute of CalendarInfo struct.
func (p *Plugin) fetchEventsFromCalendar(u *UserInfo) {
	var calendarEvents *calendar.Events
	var err error
	var calendarInfo *CalendarInfo

	calendarService := createCalendarService(u.Token)

	if info, _ := p.getCalendarInfo(u.UserID); info != nil {
		calendarInfo = info
	}

	if calendarInfo.LastEventUpdate != "" {
		calendarEvents, err = calendarService.Events.List("primary").UpdatedMin(calendarInfo.LastEventUpdate).TimeMax(time.Now().Add(time.Hour * 1).Format(time.RFC3339)).Do()
	} else {
		calendarEvents, err = calendarService.Events.List("primary").TimeMin(time.Now().Format(time.RFC3339)).TimeMax(time.Now().Add(time.Hour * 1).Format(time.RFC3339)).Do()
	}
	if err != nil {
		mlog.Error(err.Error())
		return
	}

	if len(calendarEvents.Items) > 0 {
		for index, event := range calendarEvents.Items {
			if event.Status == "cancelled" {
				calendarInfo.Events = append(calendarInfo.Events[:index], calendarInfo.Events[index+1:]...)
				continue
			}
			calendarInfo.Events = append(calendarInfo.Events, EventInfo{
				Id:        event.Id,
				HtmlLink:  event.HtmlLink,
				StartTime: formatTime(event.Start.DateTime),
				EndTime:   formatTime(event.End.DateTime),
				Summary:   event.Summary,
				Status:    event.Status,
			})
		}
		calendarInfo.LastEventUpdate = time.Now().Format(time.RFC3339)
		p.storeCalendarInfo(u.UserID, calendarInfo)
	}
}

// checkEvents checks if there is any event 10 min after the current time.
// If there is an event, if triggers a post for it.
func (p *Plugin) checkEvents(userId string) {
	c, err := p.getCalendarInfo(userId)
	if err != nil {
		mlog.Error(err.Error())
	}
	for _, e := range c.Events {
		afterTenMinutes := time.Now().Add(time.Minute * 10).Format("3:04PM")
		if afterTenMinutes == e.StartTime {
			p.createAPostForEvent(e)
		}
	}
}

func (p *Plugin) storeUserInfo(userInfo *UserInfo) error {
	jsonInfo, err := json.Marshal(userInfo)
	if err != nil {
		return err
	}

	if err := p.API.KVSet(userInfo.UserID+USER_TOKEN_KEY, jsonInfo); err != nil {
		return err
	}

	return nil
}

func (p *Plugin) getUserInfo(userID string) (*UserInfo, error) {
	var userInfo UserInfo

	if info, err := p.API.KVGet(userID + USER_TOKEN_KEY); err != nil || info == nil {
		return nil, err
	} else if err := json.Unmarshal(info, &userInfo); err != nil {
		return nil, err
	}

	return &userInfo, nil
}

func (p *Plugin) storeCalendarInfo(userID string, calendarInfo *CalendarInfo) error {
	jsonInfo, err := json.Marshal(calendarInfo)
	if err != nil {
		return err
	}

	if err := p.API.KVSet(userID+CALENDAR_TOKEN_KEY, jsonInfo); err != nil {
		return err
	}

	return nil
}

func (p *Plugin) getCalendarInfo(userID string) (*CalendarInfo, error) {
	var calendarInfo CalendarInfo

	if info, err := p.API.KVGet(userID + CALENDAR_TOKEN_KEY); err != nil || info == nil {
		return nil, err
	} else if err := json.Unmarshal(info, &calendarInfo); err != nil {
		return nil, err
	}

	return &calendarInfo, nil
}
