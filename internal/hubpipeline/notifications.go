package hubpipeline

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/bitacora-dev/bitacora/internal/notify"
)

type notificationConfig struct {
	RateLimit struct {
		RPS   float64 `yaml:"rps"`
		Burst int     `yaml:"burst"`
	} `yaml:"rate_limit"`
	Routes []routeConfig `yaml:"routes"`
}

type routeConfig struct {
	Name       string            `yaml:"name"`
	Severities []string          `yaml:"severities"`
	Labels     map[string]string `yaml:"labels"`
	Ntfy       *struct {
		TopicURL string `yaml:"topic_url"`
	} `yaml:"ntfy"`
	Webhook *struct {
		URL string `yaml:"url"`
	} `yaml:"webhook"`
	Telegram *struct {
		BotToken string `yaml:"bot_token"`
		ChatID   string `yaml:"chat_id"`
	} `yaml:"telegram"`
	SMTP *struct {
		Host     string   `yaml:"host"`
		Port     int      `yaml:"port"`
		Username string   `yaml:"username"`
		Password string   `yaml:"password"`
		From     string   `yaml:"from"`
		To       []string `yaml:"to"`
	} `yaml:"smtp"`
}

func loadRouter(path string) (*notify.Router, error) {
	routes := []notify.Route{{Name: "log", Notifier: &notify.LogNotifier{}}}
	if path == "" {
		return notify.NewRouter(routes, 0, 0), nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return notify.NewRouter(routes, 0, 0), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading notifications config: %w", err)
	}
	var cfg notificationConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing notifications config: %w", err)
	}
	for _, configured := range cfg.Routes {
		route, err := configured.route()
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	return notify.NewRouter(routes, cfg.RateLimit.RPS, cfg.RateLimit.Burst), nil
}

func (c routeConfig) route() (notify.Route, error) {
	route := notify.Route{Name: c.Name, Severities: c.Severities, Labels: c.Labels}
	destinations := 0
	for _, configured := range []bool{c.Ntfy != nil, c.Webhook != nil, c.Telegram != nil, c.SMTP != nil} {
		if configured {
			destinations++
		}
	}
	if destinations != 1 {
		return notify.Route{}, fmt.Errorf("notification route %q needs exactly one destination", c.Name)
	}
	switch {
	case c.Ntfy != nil:
		if c.Ntfy.TopicURL == "" {
			return notify.Route{}, fmt.Errorf("notification route %q: ntfy.topic_url is required", c.Name)
		}
		route.Notifier = &notify.NtfyNotifier{TopicURL: c.Ntfy.TopicURL}
	case c.Webhook != nil:
		if c.Webhook.URL == "" {
			return notify.Route{}, fmt.Errorf("notification route %q: webhook.url is required", c.Name)
		}
		route.Notifier = &notify.WebhookNotifier{URL: c.Webhook.URL}
	case c.Telegram != nil:
		if c.Telegram.BotToken == "" || c.Telegram.ChatID == "" {
			return notify.Route{}, fmt.Errorf("notification route %q: telegram.bot_token and telegram.chat_id are required", c.Name)
		}
		route.Notifier = &notify.TelegramNotifier{BotToken: c.Telegram.BotToken, ChatID: c.Telegram.ChatID}
	case c.SMTP != nil:
		if c.SMTP.Host == "" || c.SMTP.Port < 1 || c.SMTP.From == "" || len(c.SMTP.To) == 0 {
			return notify.Route{}, fmt.Errorf("notification route %q: smtp host, port, from and to are required", c.Name)
		}
		route.Notifier = notify.NewSMTPNotifier(c.SMTP.Host, c.SMTP.Port, c.SMTP.Username, c.SMTP.Password, c.SMTP.From, c.SMTP.To)
	default:
		return notify.Route{}, fmt.Errorf("notification route %q needs one destination", c.Name)
	}
	if route.Name == "" {
		return notify.Route{}, fmt.Errorf("notification route name is required")
	}
	return route, nil
}
