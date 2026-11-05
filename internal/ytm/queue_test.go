package ytm

import (
	"context"
	"testing"
	"time"
)

func TestLiveRadioQueue(t *testing.T) {
	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	seedVideoID := "qPRNIHxLhmc" // The Weeknd - I Feel It Coming
	res, err := client.GetRadio(ctx, seedVideoID)
	if err != nil {
		t.Skipf("GetRadio skipped due to network/firewall: %v", err)
		return
	}

	if len(res.Tracks) == 0 {
		t.Fatalf("Expected radio tracks in result, got 0")
	}

	t.Logf("Radio generated %d tracks (Continuation token: len=%d)", len(res.Tracks), len(res.ContinuationToken))
	for i, tr := range res.Tracks {
		if i >= 4 {
			break
		}
		t.Logf("  [%d] %s - %s (%s, ID: %s)", i+1, tr.Title, tr.Artist, tr.DurationStr, tr.ID)
	}

	if res.Tracks[0].ID == "" {
		t.Errorf("First track missing ID")
	}

	// Test Continuation if token present
	if res.ContinuationToken != "" {
		contCtx, contCancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer contCancel()

		contRes, err := client.GetRadioContinuation(contCtx, res.ContinuationToken)
		if err != nil {
			t.Logf("Continuation fetch notice: %v", err)
		} else {
			t.Logf("Continuation returned %d additional tracks", len(contRes.Tracks))
		}
	}
}
