package ytm

import (
	"context"
	"testing"
	"time"
)

func TestLiveSearch(t *testing.T) {
	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tracks, err := client.Search(ctx, "Daft Punk")
	if err != nil {
		t.Skipf("Search skipped due to network/firewall: %v", err)
		return
	}

	if len(tracks) == 0 {
		t.Fatalf("Expected tracks in search result, got 0")
	}

	t.Logf("Found %d tracks for 'Daft Punk':", len(tracks))
	for i, tr := range tracks {
		if i >= 3 {
			break
		}
		t.Logf("  [%d] %s - %s (%s, ID: %s)", i+1, tr.Title, tr.Artist, tr.DurationStr, tr.ID)
	}

	if tracks[0].ID == "" {
		t.Errorf("First track missing VideoID")
	}
	if tracks[0].Title == "" {
		t.Errorf("First track missing Title")
	}
}

func TestLiveHomeFeed(t *testing.T) {
	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	feed, err := client.GetHomeFeed(ctx)
	if err != nil {
		t.Skipf("GetHomeFeed skipped due to network/firewall: %v", err)
		return
	}

	t.Logf("Home feed: %d tracks, %d mixes", len(feed.Tracks), len(feed.Mixes))
	if feed.HeroTrack != nil {
		t.Logf("Hero track: %s - %s (ID: %s)", feed.HeroTrack.Title, feed.HeroTrack.Artist, feed.HeroTrack.ID)
	}
}
