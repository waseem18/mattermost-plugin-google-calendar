package main

import (
	"fmt"
	"time"

	"github.com/mattermost/mattermost-server/model"
)

// formatTime formats time to the format HH:MM
func formatTime(eventTime string) string {
	timeObject, _ := time.Parse(time.RFC3339, eventTime)
	formattedTime := timeObject.Format("3:04PM")
	return formattedTime
}

func generateSlackAttachment(e EventInfo) *model.SlackAttachment {
	eventMessage := fmt.Sprintf("Today from %s to %s", e.StartTime, e.EndTime)

	event := &model.SlackAttachment{
		Pretext:   postPretext,
		Title:     e.Summary,
		TitleLink: e.HtmlLink,
		Text:      eventMessage,
		Color:     "#7FC1EE",
	}
	return event
}
