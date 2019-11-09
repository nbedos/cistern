package utils

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func Test_RecentRepoBuilds(t *testing.T) {
	token := os.Getenv("TRAVIS_API_TOKEN")
	if token == "" {
		t.Fatal("Environment variable TRAVIS_API_TOKEN is not set")
	}

	authURL := "https://api.travis-ci.org/pusher/auth"
	wsURL := PusherURL("ws.pusherapp.com", "5df8ac576dcccf4fd076")
	//channel := "private-user-1842548"
	channel := "repo-25564643"

	authHeader := map[string]string{
		"Authorization": fmt.Sprintf("token %s", token),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer cancel()
	p, err := NewPusherClient(ctx, wsURL, authURL, authHeader, ticker.C)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close() // FIXME Check return value

forever:
	for {
		event, err := p.NextEvent(ctx)
		switch err {
		case nil:
			// Do nothing
		case context.DeadlineExceeded:
			break forever
		default:
			t.Fatal(err)
		}
		switch event.Event {
		case ConnectionEstablished:
			err := p.Subscribe(ctx, channel)
			switch err {
			case nil:
				// Do nothing
			case context.DeadlineExceeded:
				break forever
			default:
				t.Fatal(err)
			}
		}
	}
}
