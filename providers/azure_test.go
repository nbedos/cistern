package providers

import (
	"context"
	"testing"
	"time"
)

func TestAzurePipelinesClient_BuildFromURL(t *testing.T) {
	client := NewAzurePipelinesClient("azure", "azure", "", time.Second/20)

	webURL := "https://dev.azure.com/nicolasbedos/5190ee7b-d826-445e-b19e-6dc098be0436/_build/results?buildId=16"
	build, err := client.BuildFromURL(context.Background(), webURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", build)
}

func TestAzurePipelinesClient_parseAzureWebURL(t *testing.T) {
	webURL := "https://dev.azure.com/nicolasbedos/5190ee7b-d826-445e-b19e-6dc098be0436/_build/results?buildId=16"
	client := NewAzurePipelinesClient("azure", "azure", "", time.Second/20)
	owner, repo, id, err := client.parseAzureWebURL(webURL)
	if err != nil || owner != "nicolasbedos" || repo != "5190ee7b-d826-445e-b19e-6dc098be0436" || id != "16" {
		t.Fatalf("invalid result")
	}

}
