package common

import (
	"context"
	"testing"
	"time"
)

func TestStreamStatusTimingInfo(t *testing.T) {
	status := NewStreamStatus()
	status.StartedAt = time.Unix(100, 0)
	status.FirstChunkAt = status.StartedAt.Add(1200 * time.Millisecond)
	status.LastChunkAt = status.StartedAt.Add(2 * time.Second)
	status.LastWriteAt = status.StartedAt.Add(2500 * time.Millisecond)
	status.LastPingAt = status.StartedAt.Add(3 * time.Second)
	status.ChunkCount = 2
	status.WriteCount = 2
	status.PingCount = 1
	status.SetEndReason(StreamEndReasonEOF, nil)

	end := status.StartedAt.Add(4 * time.Second)
	status.EndedAt = end

	info := status.TimingInfo(end)
	if got := info["duration_ms"]; got != int64(4000) {
		t.Fatalf("duration_ms = %v, want 4000", got)
	}
	if got := info["first_chunk_ms"]; got != int64(1200) {
		t.Fatalf("first_chunk_ms = %v, want 1200", got)
	}
	if got := info["last_chunk_age_ms"]; got != int64(2000) {
		t.Fatalf("last_chunk_age_ms = %v, want 2000", got)
	}
	if got := info["last_write_age_ms"]; got != int64(1500) {
		t.Fatalf("last_write_age_ms = %v, want 1500", got)
	}
	if got := info["last_ping_age_ms"]; got != int64(1000) {
		t.Fatalf("last_ping_age_ms = %v, want 1000", got)
	}
	if got := info["chunk_count"]; got != 2 {
		t.Fatalf("chunk_count = %v, want 2", got)
	}
	if got := info["write_count"]; got != 2 {
		t.Fatalf("write_count = %v, want 2", got)
	}
	if got := info["ping_count"]; got != 1 {
		t.Fatalf("ping_count = %v, want 1", got)
	}
}

func TestStreamStatusClientGoneTimingInfo(t *testing.T) {
	status := NewStreamStatus()
	status.StartedAt = time.Unix(200, 0)
	status.LastChunkAt = status.StartedAt.Add(10 * time.Second)
	status.SetEndReason(StreamEndReasonClientGone, context.Canceled)
	status.EndedAt = status.StartedAt.Add(12500 * time.Millisecond)

	info := status.TimingInfo(status.EndedAt)
	if got := info["client_gone_after_last_chunk_ms"]; got != int64(2500) {
		t.Fatalf("client_gone_after_last_chunk_ms = %v, want 2500", got)
	}
}

func TestStreamStatusTimingInfoClampsNegativeAges(t *testing.T) {
	status := NewStreamStatus()
	status.StartedAt = time.Unix(300, 0)
	status.LastWriteAt = status.StartedAt.Add(2 * time.Second)
	status.SetEndReason(StreamEndReasonEOF, nil)
	status.EndedAt = status.StartedAt.Add(time.Second)

	info := status.TimingInfo(status.EndedAt)
	if got := info["last_write_age_ms"]; got != int64(0) {
		t.Fatalf("last_write_age_ms = %v, want 0", got)
	}
}
