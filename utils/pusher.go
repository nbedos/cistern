package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	ConnectionEstablished       = "pusher:connection_established"
	Error                       = "pusher:error"
	Subscribe                   = "pusher:subscribe"
	Unsubscribe                 = "pusher:unsubscribe"
	SubscriptionSucceeded       = "pusher_internal:subscription_succeeded"
	PublicSubscriptionSucceeded = "pusher:subscription_succeeded"
	Ping                        = "pusher:ping"
	Pong                        = "pusher:pong"
	MemberAdded                 = "pusher_internal:member_added"
	MemberRemoved               = "pusher_internal:member_removed"
)

type ConnectionEstablishedPayload struct {
	SocketID        string `json:"socket_id"`
	ActivityTimeout int    `json:"activity_timeout"`
}

type ErrorPayload struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

type SubscribePayload struct {
	Channel     string `json:"channel"`
	Auth        string `json:"auth,omitempty"`
	ChannelData string `json:"channel_data,omitempty"`
}

type UnsubscribePayload struct {
	Channel string `json:"channel"`
}

type PusherEvent struct {
	Event   string          `json:"event"`
	Channel string          `json:"channel,omitempty"`
	Data    json.RawMessage `json:"data"`
}

func unmarshalPayload(data []byte, v interface{}) error {
	// data might be a valid JSON object...
	if err := json.Unmarshal(data, v); err == nil {
		return nil
	}

	// ... or a JSON object encoded as a string
	var s string
	err := json.Unmarshal(data, &s)
	if err != nil {
		return err
	}

	return json.Unmarshal([]byte(s), v)
}

type PusherClient struct {
	conn        *websocket.Conn
	httpClient  *http.Client
	authURL     string
	authHeader  map[string]string
	authLimiter <-chan time.Time
	connected   bool
	channels    map[string]bool
	timeout     int
	socketID    string
}

func NewPusherClient(ctx context.Context, wsURL string, authURL string, authHeader map[string]string, authLimiter <-chan time.Time) (PusherClient, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return PusherClient{}, err
	}

	return PusherClient{
		conn:        conn,
		authURL:     authURL,
		authHeader:  authHeader,
		authLimiter: authLimiter,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		connected:   false,
		channels:    map[string]bool{},
	}, nil
}

func (p *PusherClient) readJSON(ctx context.Context, e *PusherEvent) error {
	errc := make(chan error)
	go func() {
		// Will return once a message is received or the connection is closed
		errc <- p.conn.ReadJSON(&e)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errc:
		return err
	}
}

func (p *PusherClient) send(ctx context.Context, eventType string, channel string, payload interface{}) error {
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	event := PusherEvent{
		Event:   eventType,
		Channel: channel,
		Data:    jsonPayload,
	}

	errc := make(chan error)
	go func() {
		// Will return once a message is received or the connection is closed
		errc <- p.conn.WriteJSON(event)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errc:
		return err
	}
}

// FIXME No need to implement this interface. Replace by 'Finish' or something...
func (p *PusherClient) Close() error {
	if p.connected {
		return p.conn.Close()
	}

	return nil
}

func (p *PusherClient) NextEvent(ctx context.Context) (PusherEvent, error) {
	var err error
	var event PusherEvent
	if err = p.readJSON(ctx, &event); err != nil {
		return event, err
	}

	switch event.Event {
	case ConnectionEstablished:
		payload := ConnectionEstablishedPayload{}
		if err = unmarshalPayload(event.Data, &payload); err != nil {
			return event, err
		}
		p.connected = true
		p.timeout = payload.ActivityTimeout
		p.socketID = payload.SocketID
	case SubscriptionSucceeded, PublicSubscriptionSucceeded:
		if _, exists := p.channels[event.Channel]; !exists {
			return event, fmt.Errorf("received unexpected event %v", event)
		}
	case Ping:
		err = p.send(ctx, Pong, "", "{}")
	case MemberAdded:
	case MemberRemoved:
	case Error:
		payload := ErrorPayload{}
		if err = unmarshalPayload(event.Data, &payload); err != nil {
			return event, err
		}
		return event, fmt.Errorf("received error %d: '%s'", payload.Code, payload.Message)
	default:
		if strings.HasPrefix(event.Event, "pusher:") || strings.HasPrefix(event.Event, "pusher_internal:") {
			return event, fmt.Errorf("unhandled event type: '%v'", event.Event)
		}
	}

	return event, err
}

func (p *PusherClient) Subscribe(ctx context.Context, channel string) (err error) {
	auth := ""
	if strings.HasPrefix(channel, "private-") {
		if auth, err = p.Authenticate(ctx, channel); err != nil {
			return
		}
	}

	if err = p.send(ctx, Subscribe, "", SubscribePayload{Channel: channel, Auth: auth}); err != nil {
		return
	}

	p.channels[channel] = false
	return
}

func (p *PusherClient) Unsubscribe(ctx context.Context, channel string) error {
	err := p.send(ctx, Unsubscribe, "", SubscribePayload{Channel: channel})
	if err != nil {
		return err
	}
	delete(p.channels, channel)

	return nil
}

func (p *PusherClient) Expect(ctx context.Context, eventType string) (event PusherEvent, err error) {
	event, err = p.NextEvent(ctx)
	if err != nil {
		return
	}

	if event.Event != eventType {
		err = fmt.Errorf("expected '%s' but received '%s'", eventType, event.Event)
		return
	}

	return event, err
}

func (p *PusherClient) Authenticate(ctx context.Context, channel string) (string, error) {
	payload := struct {
		SocketID string   `json:"socket_id"`
		Channels []string `json:"channels"`
	}{SocketID: p.socketID, Channels: []string{channel}}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", p.authURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return "", err
	}
	req.WithContext(ctx)

	req.Header.Add("Content-type", "application/json; charset=utf-8")
	for k, v := range p.authHeader {
		req.Header.Add(k, v)
	}

	select {
	case <-p.authLimiter:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("invalid response (status %d)", resp.StatusCode)
	}

	body := new(bytes.Buffer)
	if _, err := body.ReadFrom(resp.Body); err != nil {
		return "", err
	}
	m := struct {
		Channels map[string]string
	}{}

	if err := json.Unmarshal(body.Bytes(), &m); err != nil {
		return "", err
	}

	return m.Channels[channel], nil
}

func PusherURL(host string, appKey string) string {
	parameters := url.Values{}
	parameters.Add("protocol", "7")
	parameters.Add("client", "citop") // FIXME
	parameters.Add("version", "0.1")  // FIXME

	if !strings.Contains(host, ":") {
		host += ":443"
	}

	pathFormat := "/app/%s"

	u := url.URL{
		Scheme:   "wss",
		Host:     host,
		Path:     fmt.Sprintf(pathFormat, appKey),
		RawPath:  fmt.Sprintf(pathFormat, url.PathEscape(appKey)),
		RawQuery: parameters.Encode(),
	}
	return u.String()
}
