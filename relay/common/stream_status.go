package common

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type StreamEndReason string

const (
	StreamEndReasonNone        StreamEndReason = ""
	StreamEndReasonDone        StreamEndReason = "done"
	StreamEndReasonTimeout     StreamEndReason = "timeout"
	StreamEndReasonClientGone  StreamEndReason = "client_gone"
	StreamEndReasonScannerErr  StreamEndReason = "scanner_error"
	StreamEndReasonHandlerStop StreamEndReason = "handler_stop"
	StreamEndReasonEOF         StreamEndReason = "eof"
	StreamEndReasonPanic       StreamEndReason = "panic"
	StreamEndReasonPingFail    StreamEndReason = "ping_fail"
)

const maxStreamErrorEntries = 20

type StreamErrorEntry struct {
	Message   string
	Timestamp time.Time
}

type StreamStatus struct {
	EndReason StreamEndReason
	EndError  error
	endOnce   sync.Once

	mu           sync.Mutex
	Errors       []StreamErrorEntry
	ErrorCount   int
	StartedAt    time.Time
	EndedAt      time.Time
	FirstChunkAt time.Time
	LastChunkAt  time.Time
	LastWriteAt  time.Time
	LastPingAt   time.Time
	ChunkCount   int
	WriteCount   int
	PingCount    int
}

func NewStreamStatus() *StreamStatus {
	return &StreamStatus{StartedAt: time.Now()}
}

func (s *StreamStatus) SetEndReason(reason StreamEndReason, err error) {
	if s == nil {
		return
	}
	s.endOnce.Do(func() {
		s.EndReason = reason
		s.EndError = err
		s.mu.Lock()
		s.EndedAt = time.Now()
		s.mu.Unlock()
	})
}

func (s *StreamStatus) RecordChunk() {
	if s == nil {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.FirstChunkAt.IsZero() {
		s.FirstChunkAt = now
	}
	s.LastChunkAt = now
	s.ChunkCount++
}

func (s *StreamStatus) RecordWrite() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastWriteAt = time.Now()
	s.WriteCount++
}

func (s *StreamStatus) RecordPing() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastPingAt = time.Now()
	s.PingCount++
}

func (s *StreamStatus) TimingInfo(now time.Time) map[string]interface{} {
	if s == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	end := s.EndedAt
	if end.IsZero() {
		end = now
	}

	info := map[string]interface{}{
		"chunk_count": s.ChunkCount,
		"write_count": s.WriteCount,
		"ping_count":  s.PingCount,
	}
	if !s.StartedAt.IsZero() {
		info["duration_ms"] = nonNegativeMilliseconds(end.Sub(s.StartedAt))
	}
	if !s.StartedAt.IsZero() && !s.FirstChunkAt.IsZero() {
		info["first_chunk_ms"] = nonNegativeMilliseconds(s.FirstChunkAt.Sub(s.StartedAt))
	}
	if !s.LastChunkAt.IsZero() {
		info["last_chunk_age_ms"] = nonNegativeMilliseconds(end.Sub(s.LastChunkAt))
	}
	if !s.LastWriteAt.IsZero() {
		info["last_write_age_ms"] = nonNegativeMilliseconds(end.Sub(s.LastWriteAt))
	}
	if !s.LastPingAt.IsZero() {
		info["last_ping_age_ms"] = nonNegativeMilliseconds(end.Sub(s.LastPingAt))
	}
	if s.EndReason == StreamEndReasonClientGone && !s.LastChunkAt.IsZero() {
		info["client_gone_after_last_chunk_ms"] = nonNegativeMilliseconds(end.Sub(s.LastChunkAt))
	}
	return info
}

func nonNegativeMilliseconds(d time.Duration) int64 {
	if d < 0 {
		return 0
	}
	return d.Milliseconds()
}

func (s *StreamStatus) RecordError(msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ErrorCount++
	if len(s.Errors) < maxStreamErrorEntries {
		s.Errors = append(s.Errors, StreamErrorEntry{
			Message:   msg,
			Timestamp: time.Now(),
		})
	}
}

func (s *StreamStatus) HasErrors() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount > 0
}

func (s *StreamStatus) TotalErrorCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount
}

func (s *StreamStatus) IsNormalEnd() bool {
	if s == nil {
		return true
	}
	return s.EndReason == StreamEndReasonDone ||
		s.EndReason == StreamEndReasonEOF ||
		s.EndReason == StreamEndReasonHandlerStop
}

func (s *StreamStatus) Summary() string {
	if s == nil {
		return "StreamStatus<nil>"
	}
	b := &strings.Builder{}
	fmt.Fprintf(b, "reason=%s", s.EndReason)
	if s.EndError != nil {
		fmt.Fprintf(b, " end_error=%q", s.EndError.Error())
	}
	s.mu.Lock()
	if s.ErrorCount > 0 {
		fmt.Fprintf(b, " soft_errors=%d", s.ErrorCount)
	}
	s.mu.Unlock()
	return b.String()
}
