package metainfo

import "testing"

func TestTrackerURLs_Deduplication(t *testing.T) {
	tf := &TorrentFile{
		Announce:     "http://tracker1.example.com/announce",
		AnnounceList: []string{"http://tracker1.example.com/announce", "http://tracker2.example.com/announce"},
	}

	urls := tf.TrackerURLs()
	if len(urls) != 2 {
		t.Fatalf("expected 2 deduplicated URLs, got %d: %v", len(urls), urls)
	}
	if urls[0] != "http://tracker1.example.com/announce" {
		t.Fatalf("expected Announce first, got %s", urls[0])
	}
	if urls[1] != "http://tracker2.example.com/announce" {
		t.Fatalf("expected second tracker, got %s", urls[1])
	}
}

func TestTrackerURLs_NoAnnounceList(t *testing.T) {
	tf := &TorrentFile{
		Announce: "http://tracker.example.com/announce",
	}

	urls := tf.TrackerURLs()
	if len(urls) != 1 || urls[0] != "http://tracker.example.com/announce" {
		t.Fatalf("expected single Announce URL, got %v", urls)
	}
}

func TestTrackerURLs_SkipsEmpty(t *testing.T) {
	tf := &TorrentFile{
		Announce:     "http://tracker1.example.com/announce",
		AnnounceList: []string{"", "http://tracker2.example.com/announce", ""},
	}

	urls := tf.TrackerURLs()
	if len(urls) != 2 {
		t.Fatalf("expected 2 URLs (empty skipped), got %d: %v", len(urls), urls)
	}
}
