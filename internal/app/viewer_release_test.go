package app

import (
	"context"
	"testing"
	"time"
)

func TestLastViewerReleasesSlotWithoutStoppingOtherViewers(t *testing.T) {
	a := testApp(t)
	now := time.Now()
	a.streams.items[1] = &managedStream{ChannelID: 1, LastAccess: now, Viewers: 2, viewerActivity: map[string]time.Time{"one": now, "two": now}}
	a.streams.removeViewer(1, "unknown")
	if got := a.streams.list(); len(got) != 1 || got[0].Viewers != 2 {
		t.Fatalf("unknown viewer changed active playback: %#v", got)
	}
	a.streams.removeViewer(1, "one")
	if got := a.streams.list(); len(got) != 1 || got[0].Viewers != 1 {
		t.Fatalf("closing one viewer affected another: %#v", got)
	}
	if a.streams.reserve(1) {
		t.Fatal("an actually active stream must still count towards the limit")
	}
	a.streams.removeViewer(1, "two")
	if got := a.streams.list(); len(got) != 0 {
		t.Fatalf("last viewer left a zombie stream: %#v", got)
	}
	if !a.streams.reserve(1) {
		t.Fatal("last viewer did not release the slot immediately")
	}
	a.streams.finishReservation()
}

func TestRapidIPTVSwitchesDoNotExhaustReleasedSlots(t *testing.T) {
	a := testApp(t)
	_, err := a.db.Exec(`INSERT INTO sources(id,name,kind) VALUES(1,'IPTV','url');
INSERT INTO channels(id,source_id,name,acestream_id) VALUES
(1,1,'One','https://example.com/one.m3u8'),(2,1,'Two','https://example.com/two.m3u8');
UPDATE settings SET value='1' WHERE key='max_streams'`)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		id := int64(i%2 + 1)
		if _, err := a.ensurePlaybackViewer(context.Background(), id, false, "browser"); err != nil {
			t.Fatalf("switch %d failed: %v", i, err)
		}
		a.streams.removeViewer(id, "browser")
	}
}
