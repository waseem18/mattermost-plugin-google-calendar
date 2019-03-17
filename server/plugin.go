package main

import (
	"context"
	"encoding/json"
	"errors"
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
	userTokenKey     = "_usertoken"
	calendarTokenKey = "_calendartoken"
	CalendarIconURL  = "plugins/google-calendar/Google_Calendar_Logo.png"
	BotUsername      = "Calendar Plugin"
	postPretext      = "Event starting in 10 min"
	welcomeMessage   = "Welcome to Google Calendar Plugin"
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
	UserID    string
	Token     *oauth2.Token
	ChannelID string
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

func (p *Plugin) createBotDMPost(userInfo *UserInfo) *model.AppError {
	post := &model.Post{
		UserId:    p.BotUserID,
		ChannelId: userInfo.ChannelID,
		Message:   welcomeMessage,
		Type:      "custom_git_welcome",
		Props: map[string]interface{}{
			"from_webhook":      "true",
			"override_username": BotUsername,
			"override_icon_url": CalendarIconURL,
		},
	}

	if _, err := p.API.CreatePost(post); err != nil {
		mlog.Error("Error while creating bot welcome post" + err.Error())
		return err
	}

	return nil
}

func (p *Plugin) getDirectChannel(userInfo *UserInfo) (string, error) {
	channel, err := p.API.GetDirectChannel(userInfo.UserID, p.BotUserID)
	if err != nil {
		mlog.Error("Couldn't get bot's DM channel", mlog.String("user_id", userInfo.UserID))
		return "", err
	}
	userInfo.ChannelID = channel.Id
	return channel.Id, nil
}

func (p *Plugin) createAPostForEvent(userID string, e EventInfo) error {
	event := generateSlackAttachment(e)
	userInfo, err := p.getUserInfo(userID)

	if err != nil {
		mlog.Error("Error fetching user details" + err.Error())
		return err
	}

	p.API.CreatePost(&model.Post{
		ChannelId: userInfo.ChannelID,
		Type:      model.POST_SLACK_ATTACHMENT,
		UserId:    p.BotUserID,
		Props: map[string]interface{}{
			"from_webhook":  "true",
			"use_user_icon": "true",
			"attachments":   []*model.SlackAttachment{event},
		},
	})
	return nil
}

// createCalendarService initialises and returns a Google Calendar service
func (p *Plugin) createCalendarService(u *UserInfo) (*calendar.Service, error) {
	googleOauthConfig := p.getOAuthConfig()
	tokenSource := googleOauthConfig.TokenSource(context.TODO(), u.Token)
	newToken, err := tokenSource.Token()
	if err != nil {
		mlog.Error("Error fetching token from token source" + err.Error())
		return nil, err
	}

	if newToken.AccessToken != u.Token.AccessToken {
		u.Token = newToken
		err := p.storeUserInfo(u)
		if err != nil {
			mlog.Error("Error storing the new access token " + err.Error())
			return nil, err
		}
	}

	client := oauth2.NewClient(context.TODO(), tokenSource)
	calendarService, err := calendar.New(client)
	if err != nil {
		return nil, err
	}
	return calendarService, nil
}

func (p *Plugin) subscribeToCalendar(u *UserInfo) {
	p.processEventsFromCalendar(u)

	p.setupCalendarWatchService(u)

	cron := cron.New()
	cron.AddFunc("@every 1m", func() {
		// checkEvents method gets called every minute to check if the
		// start time of any event is after 10 min.
		p.checkEvents(u.UserID)
	})
	cron.Start()
}

func (p *Plugin) setupCalendarWatchService(u *UserInfo) error {
	calendarInfo, calendarInfoErr := p.getCalendarInfo(u.UserID)
	if calendarInfoErr != nil {
		return calendarInfoErr
	}

	calendarService, calendarServiceErr := p.createCalendarService(u)
	if calendarServiceErr != nil {
		return calendarServiceErr
	}

	config := p.API.GetConfig()

	uuid := uuid.New().String()

	eventsWatchCall := calendarService.Events.Watch("primary", &calendar.Channel{
		Address: fmt.Sprintf("%s/plugins/google-calendar/watch?userID=%s", *config.ServiceSettings.SiteURL, u.UserID),
		Id:      uuid,
		Type:    "web_hook",
	})

	channel, err := eventsWatchCall.Do()
	if err != nil {
		return err
	}

	calendarInfo.CalendarWatchToken = uuid
	calendarInfo.CalendarWatchExpiry = channel.Expiration
	p.storeCalendarInfo(u.UserID, calendarInfo)

	return nil
}

func (p *Plugin) setupWatchRenewal(userID string) error {
	calendarInfo, calendarInfoErr := p.getCalendarInfo(userID)
	if calendarInfoErr != nil {
		return calendarInfoErr
	}

	userInfo, userInfoErr := p.getUserInfo(userID)
	if userInfoErr != nil {
		return userInfoErr
	}

	diff := calendarInfo.CalendarWatchExpiry - (time.Now().UnixNano()/int64(time.Millisecond) + 60000)
	if diff <= 60000 {
		time.AfterFunc(time.Millisecond*time.Duration(diff), func() {
			p.setupCalendarWatchService(userInfo)
		})
	}
	return nil
}

// checkIfTheEventAlreadyExists checks if event with given eventID already exists
func (p *Plugin) checkIfTheEventAlreadyExists(eventID, userID string) bool {
	calendarInfo, _ := p.getCalendarInfo(userID)
	for _, event := range calendarInfo.Events {
		if event.Id == eventID {
			return true
		}
	}
	return false
}

func (p *Plugin) fetchEventsFromCalendar(u *UserInfo) (*calendar.Events, error) {
	calendarService, _ := p.createCalendarService(u)

	var calendarInfo *CalendarInfo
	var calendarEvents *calendar.Events
	var err error

	if info, _ := p.getCalendarInfo(u.UserID); info != nil {
		calendarInfo = info
	}

	if calendarInfo.LastEventUpdate != "" {
		calendarEvents, err = calendarService.Events.List("primary").UpdatedMin(calendarInfo.LastEventUpdate).TimeMax(time.Now().Add(time.Hour * 1).Format(time.RFC3339)).Do()
	} else {
		calendarEvents, err = calendarService.Events.List("primary").TimeMin(time.Now().Format(time.RFC3339)).TimeMax(time.Now().Add(time.Hour * 1).Format(time.RFC3339)).Do()
	}

	if err != nil {
		return nil, err
	}

	return calendarEvents, nil
}

func (p *Plugin) processEventsFromCalendar(u *UserInfo) error {
	var calendarInfo *CalendarInfo
	info, err := p.getCalendarInfo(u.UserID)

	if info != nil {
		calendarInfo = info
	}

	if err != nil {
		return err
	}

	calendarEvents, err := p.fetchEventsFromCalendar(u)
	if err != nil {
		return err
	}

	if len(calendarEvents.Items) > 0 {
		for _, event := range calendarEvents.Items {
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
	return nil
}

func (p *Plugin) updateCalendarEvents(u *UserInfo, calendarInfo *CalendarInfo) error {
	calendarEvents, err := p.fetchEventsFromCalendar(u)
	if err != nil {
		return err
	}

	for _, event := range calendarEvents.Items {
		if event.Status == "cancelled" {
			calendarInfo, _ = p.removeAnEvent(u.UserID, event)
		} else if p.checkIfTheEventAlreadyExists(event.Id, u.UserID) == true {
			e := EventInfo{
				Id:        event.Id,
				HtmlLink:  event.HtmlLink,
				StartTime: formatTime(event.Start.DateTime),
				EndTime:   formatTime(event.End.DateTime),
				Summary:   event.Summary,
				Status:    event.Status,
			}
			calendarInfo, _ = p.updateEvent(event.Id, u.UserID, e)
		} else {
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
	return nil
}

// checkEvents checks if there is any event 10 min after the current time.
// If there is an event, if triggers a post for it.
func (p *Plugin) checkEvents(userID string) error {
	calendarInfo, err := p.getCalendarInfo(userID)
	if err != nil {
		return err
	}
	for _, e := range calendarInfo.Events {
		afterTenMinutes := time.Now().Add(time.Minute * 10).Format("3:04PM")
		if afterTenMinutes == e.StartTime {
			_ = p.createAPostForEvent(userID, e)
		}
	}
	p.setupWatchRenewal(userID)
	return nil
}

func (p *Plugin) storeUserInfo(userInfo *UserInfo) error {
	jsonInfo, err := json.Marshal(userInfo)
	if err != nil {
		return err
	}

	if err := p.API.KVSet(userInfo.UserID+userTokenKey, jsonInfo); err != nil {
		return err
	}

	return nil
}

func (p *Plugin) getUserInfo(userID string) (*UserInfo, error) {
	var userInfo UserInfo

	if info, err := p.API.KVGet(userID + userTokenKey); err != nil || info == nil {
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

	if err := p.API.KVSet(userID+calendarTokenKey, jsonInfo); err != nil {
		return err
	}

	return nil
}

func (p *Plugin) getCalendarInfo(userID string) (*CalendarInfo, error) {
	var calendarInfo CalendarInfo

	if info, err := p.API.KVGet(userID + calendarTokenKey); err != nil || info == nil {
		return nil, err
	} else if err := json.Unmarshal(info, &calendarInfo); err != nil {
		return nil, err
	}

	return &calendarInfo, nil
}

func (p *Plugin) updateEvent(eventID, userID string, updatedEvent EventInfo) (*CalendarInfo, error) {
	calendarInfo, err := p.getCalendarInfo(userID)
	if err != nil {
		return nil, err
	}
	for index := range calendarInfo.Events {
		event := &calendarInfo.Events[index]
		if event.Id == eventID {
			event.StartTime = updatedEvent.StartTime
			event.EndTime = updatedEvent.EndTime
			event.Status = updatedEvent.Status
			event.HtmlLink = updatedEvent.HtmlLink
			event.Id = updatedEvent.Id
			event.Summary = updatedEvent.Summary
			break
		}
	}
	return calendarInfo, nil
}

func (p *Plugin) removeAnEvent(userID string, e *calendar.Event) (*CalendarInfo, error) {
	calendarInfo, err := p.getCalendarInfo(userID)
	if err != nil {
		return nil, errors.New("Failed to get calendar information")
	}
	for index, event := range calendarInfo.Events {
		if event.Id == e.Id {
			calendarInfo.Events = append(calendarInfo.Events[:index], calendarInfo.Events[index+1:]...)
			break
		}
	}
	return calendarInfo, nil
}
