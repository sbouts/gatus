package rocketchat

import (
	"fmt"
	"net/http"
	"regexp"

	"github.com/TwinProduction/gatus/alerting/alert"
	"github.com/TwinProduction/gatus/alerting/provider/custom"
	"github.com/TwinProduction/gatus/core"
)

// AlertProvider is the configuration necessary for sending an alert using RocketChat
type AlertProvider struct {
	WebhookURL string `yaml:"webhook-url"`

	// DefaultAlert is the default alert configuration to use for services with an alert of the appropriate type
	DefaultAlert *alert.Alert `yaml:"default-alert"`
}

// Replace '<=|>=|<|>' signs because Rocketchat cannot render them correctly as of v3.18.1
func replaceEqChars(toReplace string) string {
	reLtE := regexp.MustCompile(`<=`)
	reGtE := regexp.MustCompile(`>=`)
	reLt := regexp.MustCompile(`<`)
	reGt := regexp.MustCompile(`>`)
	toReplace = reLtE.ReplaceAllString(toReplace, "lte")
	toReplace = reGtE.ReplaceAllString(toReplace, "gte")
	toReplace = reLt.ReplaceAllString(toReplace, "lt")
	toReplace = reGt.ReplaceAllString(toReplace, "gt")
	return toReplace
}

// IsValid returns whether the provider's configuration is valid
func (provider *AlertProvider) IsValid() bool {
	return len(provider.WebhookURL) > 0
}

// ToCustomAlertProvider converts the provider into a custom.AlertProvider
func (provider *AlertProvider) ToCustomAlertProvider(service *core.Service, alert *alert.Alert, result *core.Result, resolved bool) *custom.AlertProvider {
	var message string
	var color string
	if resolved {
		message = fmt.Sprintf("An alert for *%s* has been resolved after passing successfully *%d time(s)* in a row.", service.Name, alert.SuccessThreshold)
		color = "#36A64F"
	} else {
		message = fmt.Sprintf("An alert for *%s* has been triggered due to having failed *%d time(s)* in a row.", service.Name, alert.FailureThreshold)
		color = "#DD0000"
	}
	var results string
	for _, conditionResult := range result.ConditionResults {
		var prefix string
		// https://github.com/RocketChat/Rocket.Chat/pull/23232 emoji supported in upcoming release
		if conditionResult.Success {
			prefix = "Successful check:"
		} else {
			prefix = "Failed check:    "
		}
		results += fmt.Sprintf("%s - `%s`\\n", prefix, conditionResult.Condition)
	}
	results = replaceEqChars(results)
	var description string
	if alertDescription := alert.GetDescription(); len(alertDescription) > 0 {
		description = alert.GetDescription()
	} else {
		description = "No description provided"
	}
	return &custom.AlertProvider{
		URL:    provider.WebhookURL,
		Method: http.MethodPost,
		Body: fmt.Sprintf(`{
  "text": "",
  "alias": "gatus",
  "emoji": ":helmet_with_white_cross:",
  "attachments": [
    {
      "title": "Gatus Alert",
      "fallback": "Gatus - %s",
      "text": "%s",
      "color": "%s",
      "fields": [
        {
          "title": "URL",
          "value": "%s",
          "short": false
        },
        {
          "title": "Description",
          "value": "%s",
          "short": false
        },
        {
          "title": "Condition results",
          "value": "%s",
          "short": false
        }
      ]
    }
  ]
}`, message, message, color, service.URL, description, results),
		Headers: map[string]string{"Content-Type": "application/json"},
	}
}

// GetDefaultAlert returns the provider's default alert configuration
func (provider AlertProvider) GetDefaultAlert() *alert.Alert {
	return provider.DefaultAlert
}
