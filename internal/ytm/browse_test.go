package ytm

import (
	"context"
	"testing"
	"time"
)

func TestParseAlbumBrowseSyntheticFallback(t *testing.T) {
	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	album, err := client.GetAlbum(ctx, "MPREb_synthetic_test")
	if err != nil {
		t.Fatalf("GetAlbum failed: %v", err)
	}

	if album == nil {
		t.Fatal("expected non-nil album details")
	}
	if album.ID != "MPREb_synthetic_test" {
		t.Errorf("expected ID MPREb_synthetic_test, got %s", album.ID)
	}
	if len(album.Tracks) == 0 {
		t.Fatal("expected album tracks, got 0")
	}
	if album.Tracks[0].Album == "" {
		t.Errorf("expected non-empty album on track, got empty")
	}
}

func TestParseArtistBrowseSyntheticFallback(t *testing.T) {
	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	artist, err := client.GetArtist(ctx, "UC_artist_test")
	if err != nil {
		t.Fatalf("GetArtist failed: %v", err)
	}

	if artist == nil {
		t.Fatal("expected non-nil artist details")
	}
	if len(artist.TopSongs) == 0 {
		t.Errorf("expected top songs, got 0")
	}
	if len(artist.Albums) == 0 {
		t.Errorf("expected albums, got 0")
	}
}

func TestParsePlaylistBrowseSyntheticFallback(t *testing.T) {
	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	playlist, err := client.GetPlaylist(ctx, "PL_chill_vibes")
	if err != nil {
		t.Fatalf("GetPlaylist failed: %v", err)
	}

	if playlist == nil {
		t.Fatal("expected non-nil playlist details")
	}
	if len(playlist.Tracks) == 0 {
		t.Errorf("expected playlist tracks, got 0")
	}
}

func TestParseAlbumBrowseWithMockJSON(t *testing.T) {
	mockRaw := map[string]interface{}{
		"header": map[string]interface{}{
			"musicDetailHeaderRenderer": map[string]interface{}{
				"title": map[string]interface{}{
					"runs": []interface{}{
						map[string]interface{}{"text": "Discovery"},
					},
				},
				"subtitle": map[string]interface{}{
					"runs": []interface{}{
						map[string]interface{}{"text": "Album"},
						map[string]interface{}{"text": " • "},
						map[string]interface{}{
							"text": "Daft Punk",
							"navigationEndpoint": map[string]interface{}{
								"browseEndpoint": map[string]interface{}{
									"browseId": "UCdaftpunk",
								},
							},
						},
						map[string]interface{}{"text": " • "},
						map[string]interface{}{"text": "2001"},
					},
				},
				"secondSubtitle": map[string]interface{}{
					"runs": []interface{}{
						map[string]interface{}{"text": "14 songs • 1 hour"},
					},
				},
			},
		},
		"contents": map[string]interface{}{
			"singleColumnBrowseResultsRenderer": map[string]interface{}{
				"tabs": []interface{}{
					map[string]interface{}{
						"tabRenderer": map[string]interface{}{
							"content": map[string]interface{}{
								"sectionListRenderer": map[string]interface{}{
									"contents": []interface{}{
										map[string]interface{}{
											"musicShelfRenderer": map[string]interface{}{
												"contents": []interface{}{
													map[string]interface{}{
														"musicResponsiveListItemRenderer": map[string]interface{}{
															"flexColumns": []interface{}{
																map[string]interface{}{
																	"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																		"text": map[string]interface{}{
																			"runs": []interface{}{
																				map[string]interface{}{"text": "One More Time"},
																			},
																		},
																	},
																},
																map[string]interface{}{
																	"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																		"text": map[string]interface{}{
																			"runs": []interface{}{
																				map[string]interface{}{"text": "Daft Punk • 5:20"},
																			},
																		},
																	},
																},
															},
															"navigationEndpoint": map[string]interface{}{
																"watchEndpoint": map[string]interface{}{
																	"videoId": "FGBhQbmPwH8",
																},
															},
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	details := parseAlbumBrowse("MPREb_mock", mockRaw)
	if details.Title != "Discovery" {
		t.Errorf("expected title Discovery, got %q", details.Title)
	}
	if details.Artist != "Daft Punk" {
		t.Errorf("expected artist Daft Punk, got %q", details.Artist)
	}
	if details.ArtistID != "UCdaftpunk" {
		t.Errorf("expected artist ID UCdaftpunk, got %q", details.ArtistID)
	}
	if details.Year != "2001" {
		t.Errorf("expected year 2001, got %q", details.Year)
	}
	if len(details.Tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(details.Tracks))
	}
	if details.Tracks[0].Title != "One More Time" {
		t.Errorf("expected track title 'One More Time', got %q", details.Tracks[0].Title)
	}
	if details.Tracks[0].Album != "Discovery" {
		t.Errorf("expected track album 'Discovery', got %q", details.Tracks[0].Album)
	}
}
