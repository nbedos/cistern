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
	done := make(chan struct{})
	data, err := json.Marshal("data")
	if err != nil {
		t.Fatal(err)
	}
	expected := PusherEvent{
		Event:   "event",
		Channel: "channel",
		Data:    data,
	}

	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		var event PusherEvent
		if err := c.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}

		if diff := deep.Equal(event, expected); len(diff) > 0 {
			for _, line := range diff {
				t.Log(line)
			}
			t.Fatalf("expected %+v but got %+v", expected, event)
		}

		close(done)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	limiter := time.Tick(time.Millisecond)
	client, err := NewPusherClient(context.Background(), wsURL, "", nil, limiter)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if err := client.send(context.Background(), expected.Event, expected.Channel, expected.Data); err != nil {
		t.Fatal(err)
	}

	<-done
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
				t.Fatal(err)
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
				t.Fatalf("expected %q but got %q", testCase.event, event.Event)
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
		t.Fatalf("expected %q but got %q", auth, s)
	}
}

func TestPusherClient_SubscribeUnsubscribe(t *testing.T) {
	channel := "channel"
	done := make(chan struct{})

	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		e := PusherEvent{}
		if err := c.ReadJSON(&e); err != nil {
			t.Fatal(err)
		}
		if e.Event != "pusher:subscribe" {
			t.Fatalf("expected %+v but got %+v", "pusher:subscribe", e.Event)
		}
		payload := fmt.Sprintf(`{"event": "pusher_internal:subscription_succeeded", "channel": "%s"}`, channel)
		if err := c.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
			t.Fatal(err)
		}

		if err := c.ReadJSON(&e); err != nil {
			t.Fatal(err)
		}
		if e.Event != "pusher:unsubscribe" {
			t.Fatalf("expected %q but got %q", "pusher:unsubscribe", e.Event)
		}

		close(done)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	limiter := time.Tick(time.Millisecond)
	client, err := NewPusherClient(context.Background(), wsURL, "", nil, limiter)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if err := client.Subscribe(context.Background(), channel); err != nil {
		t.Fatal(err)
	}

	event, err := client.NextEvent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if event.Event != SubscriptionSucceeded {
		t.Fatalf("expected %q but got %q", SubscriptionSucceeded, event.Event)
	}
	if !client.channels[channel] {
		t.Fatalf("channel %q missing from client.channels following subscription", channel)
	}
	if err := client.Unsubscribe(context.Background(), channel); err != nil {
		t.Fatal(err)
	}
	if _, exists := client.channels[channel]; exists {
		t.Fatalf("channel %q was not deleted from client.channels following unsubscription", channel)
	}

	<-done
}
