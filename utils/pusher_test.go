package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-test/deep"
	"github.com/gorilla/websocket"
)

func TestPusherClientSendReadJSON(t *testing.T) {
	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			mt, message, err := c.ReadMessage()
			if err != nil {
				break
			}
			err = c.WriteMessage(mt, message)
			if err != nil {
				break
			}
		}
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	limiter := time.Tick(time.Millisecond)
	client, err := NewPusherClient(context.Background(), wsURL, "", nil, limiter)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	data, err := json.Marshal("data")
	if err != nil {
		t.Fatal(err)
	}
	expected := PusherEvent{
		Event:   "event",
		Channel: "channel",
		Data:    data,
	}
	if err := client.send(context.Background(), expected.Event, expected.Channel, expected.Data); err != nil {
		t.Fatal(err)
	}

	var event PusherEvent
	if err := client.readJSON(context.Background(), &event); err != nil {
		t.Fatal(err)
	}

	if diff := deep.Equal(event, expected); len(diff) > 0 {
		for _, line := range diff {
			t.Log(line)
		}
		t.Fatalf("expected %+v but got %+v", expected, event)
	}
}

func TestPusherClientNextEvent(t *testing.T) {
	testCases := []struct {
		event   string
		message string
	}{
		{
			event: ConnectionEstablished,
			message: `{
				"event": "pusher:connection_established",
				"data": "{\"socket_id\":\"123.456\"}"}
			}`,
		},
		{
			event: SubscriptionSucceeded,
			message: `{
				"event": "pusher_internal:subscription_succeeded",
				"channel": "presence-example-channel" 
			}`,
		},
		{
			event: PublicSubscriptionSucceeded,
			message: `{
				"event": "pusher:subscription_succeeded",
				"channel": "private-presence-example-channel" 
			}`,
		},
		{
			event: Ping,
			message: `{
				"event": "pusher:ping",
				"data": "" 
			}`,
		},
		{
			event: Error,
			message: `{
				"event": "pusher:error",
				"data": "{\"message\": \"error_message\", \"code\": 42}"
			}`,
		},
		{
			event: Error,
			message: `{
				"event": "pusher:unimplemented",
				"data": ""
			}`,
		},
	}

	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		for _, testCase := range testCases {
			if err = c.WriteMessage(websocket.TextMessage, []byte(testCase.message)); err != nil {
				break
			}
		}
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	limiter := time.Tick(time.Millisecond)
	client, err := NewPusherClient(context.Background(), wsURL, "", nil, limiter)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.channels["presence-example-channel"] = false
	client.channels["private-presence-example-channel"] = false

	for _, testCase := range testCases {
		event, err := client.NextEvent(context.Background())
		if testCase.event == Error {
			if err == nil {
				t.Fatal("expected error but got nil")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			if event.Event != testCase.event {
				t.Fatalf("expected %s but got %s", testCase.event, event.Event)
			}
		}
	}
}

func TestPusherClient_Authenticate(t *testing.T) {
	authPath := "/pusher/auth"
	headers := map[string]string{
		"header": "custom",
	}
	socketID := "123.456"
	channels := []string{"channel"}
	auth := "key:signature"

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == authPath && r.Header.Get("header") == headers["header"] {
			body := struct {
				SocketID string   `json:"socket_id"`
				Channels []string `json:"channels"`
			}{}

			b := bytes.Buffer{}
			if _, err := b.ReadFrom(r.Body); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(b.Bytes(), &body); err != nil {
				t.Fatal(err)
			}

			payload := struct {
				Channels map[string]string
			}{
				Channels: make(map[string]string),
			}
			for _, channel := range body.Channels {
				payload.Channels[channel] = auth
			}
			s, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fmt.Fprint(w, string(s)); err != nil {
				t.Fatal(err)
			}
			return
		}
		t.Fatalf("invalid request: %+v", r)
	}))
	defer authServer.Close()

	upgrader := websocket.Upgrader{}
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
	}))
	defer wsServer.Close()

	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
	authURL := authServer.URL + authPath
	limiter := time.Tick(time.Millisecond)
	client, err := NewPusherClient(context.Background(), wsURL, authURL, headers, limiter)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.socketID = socketID

	s, err := client.Authenticate(context.Background(), channels[0])
	if err != nil {
		t.Fatal(err)
	}
	if s != auth {
		t.Fatalf("expected '%s' but got '%s'", auth, s)
	}
}
