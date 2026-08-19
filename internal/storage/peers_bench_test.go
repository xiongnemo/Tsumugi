package storage

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nemo/Tsumugi/internal/secure"
)

func benchDB(b *testing.B) *DB {
	b.Helper()
	db, _, err := Open(context.Background(), b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = db.Close() })
	cipher, err := secure.NewCipher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		b.Fatal(err)
	}
	db.SetCipher(cipher)
	return db
}

func seedPeers(b *testing.B, db *DB, n int) {
	b.Helper()
	ctx := context.Background()
	peers := make([]Peer, 0, n)
	for i := 0; i < n; i++ {
		peers = append(peers, Peer{
			AccountID: "acct", Key: fmt.Sprintf("user:%d", i), Kind: "user", ID: int64(i),
			AccessHash: int64(i + 1), Title: fmt.Sprintf("Peer %d", i),
			LastPreview:   "some recent message preview text",
			LastMessageAt: time.Now().UTC().Add(-time.Duration(i) * time.Minute),
		})
	}
	if err := db.SavePeers(ctx, peers); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkListPeers5000(b *testing.B) {
	db := benchDB(b)
	seedPeers(b, db, 5000)
	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		peers, err := db.ListPeers(ctx, "acct")
		if err != nil || len(peers) != 5000 {
			b.Fatalf("err=%v n=%d", err, len(peers))
		}
	}
}

func BenchmarkSaveMessages100(b *testing.B) {
	db := benchDB(b)
	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		msgs := make([]Message, 0, 100)
		for j := 0; j < 100; j++ {
			msgs = append(msgs, Message{
				AccountID: "acct", PeerKey: "user:1", ID: i*100 + j,
				Date: time.Now().UTC(), Sender: "someone", SenderName: "Someone",
				Text:  "a message of fairly typical length, long enough to be worth encrypting",
				State: "synced",
			})
		}
		if err := db.SaveMessages(ctx, msgs); err != nil {
			b.Fatal(err)
		}
	}
}

// What the backfill round actually needs: twenty recent peers, not five thousand.
func BenchmarkRecentPeersForBackfill(b *testing.B) {
	db := benchDB(b)
	seedPeers(b, db, 5000)
	ctx := context.Background()
	now := time.Now().UTC()
	cutoff := now.Add(-7 * 24 * time.Hour)
	horizon := now.Add(-30 * 24 * time.Hour)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		peers, err := db.RecentPeersForBackfill(ctx, "acct", cutoff, horizon, 20)
		if err != nil || len(peers) != 20 {
			b.Fatalf("err=%v n=%d", err, len(peers))
		}
	}
}
