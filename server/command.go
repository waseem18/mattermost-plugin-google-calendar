package main

import (
	"github.com/mattermost/mattermost-server/model"
)

func getCommand() *model.Command {
	return &model.Command{
		Trigger:          "google-calendar",
		Description:      "Mattermost Google Calendar integration",
		DisplayName:      "Google Calendar bot",
		AutoComplete:     true,
		AutoCompleteDesc: "Available commands: connect",
		AutoCompleteHint: "[command]",
	}
}

func getCommandResponse(responseType, text string) *model.CommandResponse {
	return &model.CommandResponse{
		ResponseType: responseType,
		Text:         text,
		Username:     BotUsername,
		IconURL:      CalendarIconURL,
		Type:         model.POST_DEFAULT,
	}
}
