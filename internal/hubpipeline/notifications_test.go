package hubpipeline

import "testing"

func TestRouteRejectsMultipleDestinations(t *testing.T) {
	_, err := (routeConfig{
		Name: "ambiguous",
		Ntfy: &struct {
			TopicURL string `yaml:"topic_url"`
		}{TopicURL: "https://ntfy.example.invalid/topic"},
		Webhook: &struct {
			URL string `yaml:"url"`
		}{URL: "https://webhook.example.invalid"},
	}).route()
	if err == nil {
		t.Fatal("expected route with two destinations to be rejected")
	}
}
